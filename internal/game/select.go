package game

import (
	"github.com/hmgle/godogpaw/engine"

	"chessai/internal/xiangqi"
)

// Choosing among near-equal moves.
//
// The engine has no MultiPV — SearchLimits is {Depth, TimeLimit, Infinite} and a
// search returns one move — so a ranked list of alternatives has to be built
// here, by playing each root move on a clone and searching the position it
// leads to. That is one search per legal move, and it is the only search a level
// with a window makes: the ranking is what decides the move, which is why such a
// level's depth is bounded by what a whole move may cost. A level with no window
// skips the pass altogether and plays the move its own search returned.

// scoreUnsearchable stands in for a candidate whose search did not produce a
// usable value — a clone that failed, or a search stopped mid-iteration by an
// Undo. It is far below any real score (the engine's values run to +/-32001), so
// such a move is never inside a level's window and is chosen only when nothing
// else could be scored at all, which is a game that should have been discarded
// as stale anyway.
const scoreUnsearchable = -1_000_000

// candidate is one root move, the value the engine gives it — both expressed for
// the side that is choosing the move — and what the search that valued it cost.
type candidate struct {
	Move   xiangqi.Move
	Score  int
	Nodes  uint64
	TimeMs int
}

// candidatesFor scores every legal root move at depth.
//
// Every legal move gets an entry, including moves the search could not score:
// the list is a ranking of what is available, and dropping entries would make
// "nothing is within the window" indistinguishable from "the position is
// forced".
func (g *Game) candidatesFor(pos *xiangqi.Position, depth uint8) []candidate {
	return g.candidatesForMoves(pos, pos.LegalMoves(), depth)
}

// candidatesForMoves scores an explicit list of root moves, in the order given.
//
// It exists for the opening, where the candidate list is not the legal move list
// but the cloudbook's ranking of it — a shorter list of moves a player would
// recognize, which is what stops a wide window from holding an entire flat
// position. Scoring is the same either way: the window is one dial, and it has
// to mean the same thing in a book position as in any other.
func (g *Game) candidatesForMoves(pos *xiangqi.Position, moves []xiangqi.Move, depth uint8) []candidate {
	out := make([]candidate, 0, len(moves))
	for _, m := range moves {
		out = append(out, g.scoreMove(pos, m, depth))
	}
	return out
}

// scoreMove returns the candidate for playing m: its value from the point of
// view of the side making the move, and what the search that valued it cost.
//
// pos is only read: the move is played on a clone, the same way bookDecision
// tests a book move without committing to it.
func (g *Game) scoreMove(pos *xiangqi.Position, m xiangqi.Move, depth uint8) candidate {
	c := candidate{Move: m, Score: scoreUnsearchable}
	clone, err := pos.Clone()
	if err != nil {
		return c
	}
	if err := clone.Play(m); err != nil {
		return c
	}

	// Serialized like every other search in the process, because the engine's
	// search state is global. The lock is taken here rather than by the caller:
	// candidatesFor runs outside searchDecision's own lock, and a search nested
	// inside one would deadlock on a mutex that does not re-enter.
	g.searchMu.Lock()
	res, err := clone.Search(engine.SearchLimits{Depth: depth})
	g.searchMu.Unlock()

	// The cost is recorded before the result is judged: the search ran whatever
	// came of it, and what a turn reports spending should be what it spent.
	c.Nodes, c.TimeMs = res.Nodes, res.TimeMs
	if err != nil || res.Fallback {
		return c
	}
	// A search scores from the point of view of the side to move, which after
	// playing m is the opponent. Negating puts the value in the frame of the
	// side choosing — the same frame as the score of the move the level's own
	// search returned, and the frame the window is measured in.
	c.Score = -int(res.Score)
	return c
}

// chooseWithin picks the move to play from a scored candidate list.
//
// Every candidate within Window centipawns of the best score is eligible, and
// one of them is taken uniformly. That single dial is the level's whole
// strength: a move just outside the window is never played however weak the
// level, and in a forced position only one move is eligible, so every level
// finds it. A weak level should look like a player who settles for a reasonable
// move, not like an engine that has lost the plot.
//
// Window 0 admits only candidates tied with the best score. In production that
// case never arrives — a level with no window skips the candidate pass entirely
// and plays the move its own search returned (see searchDecision) — so it is
// here for the arithmetic to be complete, not because a level uses it.
func (g *Game) chooseWithin(cands []candidate, window int) (candidate, bool) {
	if len(cands) == 0 {
		return candidate{}, false
	}

	best := cands[0].Score
	for _, c := range cands[1:] {
		if c.Score > best {
			best = c.Score
		}
	}

	// Nothing could be scored: every candidate is scoreUnsearchable, so they are
	// all equally eligible and choosing among them would play a move nothing
	// valued. That is not a choice, and it is not a property of the window — it
	// means every search was cut short, which is an Undo or a New Game arriving
	// mid-pass. Reporting it is the caller's job, because only the caller knows
	// what a turn with no usable search is supposed to look like.
	if best == scoreUnsearchable {
		return candidate{}, false
	}

	// The floor is inclusive: a move exactly Window below the best is still
	// close enough to be worth playing, which is what makes Window 0 mean "the
	// best moves" rather than "nothing".
	floor := best - window
	eligible := make([]candidate, 0, len(cands))
	for _, c := range cands {
		if c.Score >= floor {
			eligible = append(eligible, c)
		}
	}
	if len(eligible) == 0 {
		// Unreachable while windows are non-negative: the best candidate is
		// always at or above its own floor, and the level table is checked to
		// keep windows non-negative.
		return candidate{}, false
	}
	return eligible[g.randomIndex(len(eligible))], true
}

// pickWithin replaces a decision's move with one the level's window admits,
// modifying the decision in place, and reports whether one was chosen. The
// candidates are the position's legal moves, which is the case for every
// position outside the opening book.
//
// It fills in what the turn cost, because for a windowed level this pass is the
// whole of the turn's work: the ranking is the only search the level makes, so
// the candidate searches together are what the move spent.
func (g *Game) pickWithin(pos *xiangqi.Position, lvl level, decision *AIDecision) bool {
	cands := g.candidatesFor(pos, lvl.Depth)
	if !g.replaceWithChoice(cands, lvl, decision) {
		// Nothing was chosen, so nothing was spent on a move.
		return false
	}
	addPassCost(decision, cands)
	return true
}

// addPassCost adds what ranking cands cost to the decision.
//
// The sum is over every candidate and not only the ones the window admitted: the
// searches that were paid for are the searches that ran. Both ways a windowed
// move can be chosen run a pass — the position's legal moves, and the book's
// shorter ranked list — so both report a cost, and a turn that shows none is one
// that ran no searches at all.
func addPassCost(decision *AIDecision, cands []candidate) {
	for _, c := range cands {
		decision.Nodes += c.Nodes
		decision.TimeMs += c.TimeMs
	}
}

// replaceWithChoice rewrites a decision to a move the window chooses from cands,
// reporting whether one was chosen.
//
// The decision afterwards describes the move that will actually be played, not
// the one the search preferred, so the depth, score and line that accompany it
// are the chosen move's. A level that errs should not also misreport what it
// thinks of the move it made. Nodes and time are not rewritten here: the caller
// that ran the searches is the one that knows what they cost.
//
// Both ways a move can be chosen end here — the position's legal moves, and the
// opening book's ranked list — so a substituted decision has one shape wherever
// it came from. A candidate list the window cannot choose from leaves the
// decision as it stands.
func (g *Game) replaceWithChoice(cands []candidate, lvl level, decision *AIDecision) bool {
	chosen, ok := g.chooseWithin(cands, lvl.Window)
	if !ok {
		return false
	}
	decision.Move = engine.Move2Str(chosen.Move)
	decision.Depth = lvl.Depth
	decision.Score = chosen.Score
	// The line the search produced belongs to a move that is no longer being
	// played, so it is replaced by the one move that is known to follow.
	decision.PV = []string{decision.Move}
	return true
}
