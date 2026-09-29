package game

import (
	"context"
	"fmt"

	"github.com/hmgle/godogpaw/engine"

	"chessai/internal/cloud"
	"chessai/internal/xiangqi"
)

// HintDepth is how deeply a hint is searched. A hint deliberately ignores the
// difficulty setting — telling a beginner-level player what a beginner-level
// engine would play is not a hint.
const HintDepth = 8

// AIMove plays the computer's turn and returns what it decided.
//
// The book lookup and the search both run with no lock held, on a clone of the
// position, so neither a slow engine nor an unreachable cloudbook can block the
// UI. The generation is re-checked before the move is applied: if the player hit
// New Game or Undo while the computer was thinking, the move is dropped rather
// than played into a game it no longer belongs to.
func (g *Game) AIMove(ctx context.Context) (*AIDecision, error) {
	// One turn at a time, and refuse rather than queue: a second request waiting
	// behind a thirty-second search would just be a stale move by the time it ran.
	if !g.aiMu.TryLock() {
		return nil, ErrAIBusy
	}
	defer g.aiMu.Unlock()

	g.mu.Lock()
	if g.pos.Result().Over {
		g.mu.Unlock()
		return nil, ErrGameOver
	}
	if g.pos.SideToMove() == g.cfg.PlayerSide {
		g.mu.Unlock()
		return nil, ErrNotAITurn
	}
	gen := g.gen
	side := g.pos.SideToMove()
	fen := g.pos.FEN()
	cfg := g.cfg
	lvl := g.lvl
	// Clone while still holding the lock: the clone carries the move history,
	// which the engine's repetition detection needs, and it lets the search run
	// without touching the live position.
	clone, err := g.pos.Clone()
	g.mu.Unlock()
	if err != nil {
		return nil, err
	}

	decision, err := g.decide(ctx, clone, fen, cfg, lvl)
	if err != nil {
		return nil, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.gen != gen || g.pos.SideToMove() != side {
		return nil, ErrStale
	}
	// PlayUCI validates against the live position, so a decision that went stale
	// in some way this check missed is still refused rather than applied.
	if _, err := g.pos.PlayUCI(decision.Move); err != nil {
		return nil, fmt.Errorf("%w: computer chose unplayable move %q: %v", ErrBadMove, decision.Move, err)
	}
	g.lastAI = decision
	return decision, nil
}

// Hint suggests a move for the human. It uses the cloudbook when it has an
// answer and the book is enabled, and searches otherwise.
func (g *Game) Hint(ctx context.Context) (*AIDecision, error) {
	g.mu.Lock()
	if g.pos.Result().Over {
		g.mu.Unlock()
		return nil, ErrGameOver
	}
	if g.pos.SideToMove() != g.cfg.PlayerSide {
		g.mu.Unlock()
		return nil, ErrNotYourTurn
	}
	gen := g.gen
	fen := g.pos.FEN()
	cfg := g.cfg
	// A hint advises rather than plays: it gives the best move the engine can
	// find at HintDepth, whatever window the level being advised lets it play
	// with.
	lvl := g.lvl.withDepth(HintDepth)
	lvl.Window = 0
	clone, err := g.pos.Clone()
	g.mu.Unlock()
	if err != nil {
		return nil, err
	}

	decision, err := g.decide(ctx, clone, fen, cfg, lvl)
	if err != nil {
		return nil, err
	}
	// A hint is advisory, so it is not applied — only checked for staleness, so
	// the UI does not show a suggestion for a position that has moved on.
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.gen != gen {
		return nil, ErrStale
	}
	return decision, nil
}

// decide picks a move: the cloudbook's when it has one for this position,
// otherwise a local search. lvl is the strength to play at, which a hint
// overrides.
func (g *Game) decide(ctx context.Context, pos *xiangqi.Position, fen string, cfg Config, lvl level) (*AIDecision, error) {
	if cfg.UseBook && g.cloud != nil {
		decision, err := g.bookDecision(ctx, pos, fen, lvl)
		if err != nil {
			return nil, err
		}
		if decision != nil {
			return decision, nil
		}
	}
	return g.searchDecision(pos, lvl)
}

// bookDecision asks the cloudbook for a move. It returns (nil, nil) when the
// position is not in the book or the lookup failed — both of which simply mean
// "search instead". pos is a throwaway clone, which is what makes it safe to
// test the book's moves against it.
//
// What the book is asked for depends on whether the level is allowed to err. A
// level with no window has nothing to choose between, so it plays the book's own
// answer, which costs one query. A level with a window chooses among the book's
// *ranked* moves instead of among every legal move, and that is the whole point
// of asking: an opening position is flat enough that a window over its legal
// move list holds every move in it, so a weak level would open by shuffling a
// rook. The book's ranking is the shorter, curated list the window was meant to
// see.
func (g *Game) bookDecision(ctx context.Context, pos *xiangqi.Position, fen string, lvl level) (*AIDecision, error) {
	ctx, cancel := context.WithTimeout(ctx, BookTimeout)
	defer cancel()

	if lvl.Window <= 0 {
		return g.bookTopMove(ctx, pos, fen)
	}
	return g.bookChoice(ctx, pos, fen, lvl)
}

// bookTopMove plays the book's own best move, which is what a level with no
// window does in the opening: the database has already ranked these positions,
// and such a level has no weaker move to weigh against it.
func (g *Game) bookTopMove(ctx context.Context, pos *xiangqi.Position, fen string) (*AIDecision, error) {
	mv, err := g.cloud.BestMove(ctx, fen)
	if err != nil {
		// A dead cloudbook is a degraded experience, not a failure: the local
		// engine still plays.
		g.log.Debug("cloudbook lookup failed, searching locally", "fen", fen, "err", err)
		return nil, nil
	}
	if mv == "" {
		return nil, nil
	}
	// The move comes from a third party, so confirm this position actually
	// allows it before trusting it.
	if _, err := pos.PlayUCI(mv); err != nil {
		g.log.Warn("cloudbook offered an illegal move, searching locally",
			"move", mv, "fen", fen, "err", err)
		return nil, nil
	}
	return &AIDecision{Move: mv, Source: SourceBook}, nil
}

// bookChoice chooses among the moves the book ranks for this position, using the
// level's window and the level's own scoring.
//
// Scoring the book's moves locally rather than trusting the numbers chessdb
// sends keeps one definition of "close to the best" across the whole ladder: the
// window is measured against the same engine, at the same depth, in a book
// position and an open one alike. It also gives an entry with no score of its
// own — the database leaves many unscored — a value it can be compared by.
func (g *Game) bookChoice(ctx context.Context, pos *xiangqi.Position, fen string, lvl level) (*AIDecision, error) {
	a, err := g.cloud.Analysis(ctx, fen)
	if err != nil {
		g.log.Debug("cloudbook lookup failed, searching locally", "fen", fen, "err", err)
		return nil, nil
	}
	if a == nil || !a.Known {
		return nil, nil
	}

	moves := legalBookMoves(pos, bookRanking(a))
	if len(moves) == 0 {
		// The book knows the position but lists nothing playable in it: an entry
		// this position does not agree with. There is nothing to choose from, so
		// search as if the book had been silent.
		g.log.Debug("cloudbook offered no playable move, searching locally", "fen", fen)
		return nil, nil
	}

	// The decision starts as the book's own first choice, so it is a playable
	// move even in the case the window cannot choose — which cannot happen with
	// a non-empty candidate list, and is not a reason to play nothing.
	decision := &AIDecision{Move: engine.Move2Str(moves[0]), Source: SourceBook}
	cands := g.candidatesForMoves(pos, moves, lvl.Depth)
	g.replaceWithChoice(cands, lvl, decision)
	// The ranking ran whether or not the window chose from it, so its cost is
	// reported either way: it is what the turn spent.
	addPassCost(decision, cands)
	return decision, nil
}

// bookRanking returns the cloudbook's ranking of a position's moves, best first.
//
// querybest is authoritative when the database answers at all, so its move
// leads; queryall's ranked list follows in the order chessdb sent it, which is
// by rank. A move both sources name appears once.
func bookRanking(a *cloud.Analysis) []string {
	ranked := make([]string, 0, len(a.Moves)+1)
	seen := make(map[string]bool, len(a.Moves)+1)
	add := func(mv string) {
		if mv == "" || seen[mv] {
			return
		}
		seen[mv] = true
		ranked = append(ranked, mv)
	}
	add(a.Best)
	for _, m := range a.Moves {
		add(m.Move)
	}
	return ranked
}

// legalBookMoves turns the book's ranking into moves this position allows,
// keeping the book's order.
//
// The book is a third party: an entry can be stale, or indexed against a move
// history this position does not have. So a ranked move becomes a candidate only
// once the position is known to permit it — the same distrust the single book
// move has always been checked with.
func legalBookMoves(pos *xiangqi.Position, ranked []string) []xiangqi.Move {
	legal := pos.LegalMoves()
	byUCI := make(map[string]xiangqi.Move, len(legal))
	for _, m := range legal {
		byUCI[engine.Move2Str(m)] = m
	}

	out := make([]xiangqi.Move, 0, len(ranked))
	for _, uci := range ranked {
		if m, ok := byUCI[uci]; ok {
			out = append(out, m)
		}
	}
	return out
}

// searchDecision picks a move at the level's strength.
//
// A level with a window ranks its legal moves at its depth and plays one of the
// near-equal ones; that ranking is the level's only search. There is deliberately
// no second search to hand the ranking a "best move" to measure against: it
// would cost as much as the ranking itself, at a depth whose values are not
// comparable with the ranking's, and the window — which has the last word on the
// move played — would throw its answer away.
//
// A level with no window searches and plays what it finds. That is the strongest
// level's contract, and it is also what keeps the cost sane: ranking
// alternatives is one search per legal move, which no turn should pay for when
// the answer cannot change.
func (g *Game) searchDecision(pos *xiangqi.Position, lvl level) (*AIDecision, error) {
	if lvl.Window > 0 {
		decision := &AIDecision{Source: SourceSearch}
		if g.pickWithin(pos, lvl, decision) {
			return decision, nil
		}
		// Not one candidate could be scored, which is every search in the pass
		// cut short — an Undo or a New Game arrived while it ran. The turn is
		// about to be discarded as stale, and it is reported the way a search
		// that produced nothing is.
		return g.playALegalMove(pos, "no candidate could be scored, playing a legal one",
			"depth", lvl.Depth)
	}

	// Every search in the process is serialized here, because the engine's
	// search state is global.
	g.searchMu.Lock()
	res, err := pos.Search(engine.SearchLimits{Depth: lvl.Depth})
	g.searchMu.Unlock()

	if err == nil && engine.IsOKMove(res.BestMove) {
		return &AIDecision{
			Move:     engine.Move2Str(res.BestMove),
			Source:   SourceSearch,
			Depth:    res.Depth,
			Score:    int(res.Score),
			Nodes:    res.Nodes,
			TimeMs:   res.TimeMs,
			PV:       movesToStrings(res.PV),
			Fallback: res.Fallback,
		}, nil
	}

	// The search failed or was cut short — an Undo or a New Game, or an engine
	// that produced nothing usable. Its score is meaningless and the turn is
	// about to be discarded as stale.
	return g.playALegalMove(pos, "engine search produced no move, playing a legal one",
		"depth", lvl.Depth, "err", err)
}

// playALegalMove plays the first legal move and flags the decision as a fallback.
//
// It is what a turn does when nothing produced a move to play — the search
// failed or was cut short, or a windowed level's ranking could not score a
// single candidate. The game is not over, so a legal move exists and the
// computer should still play one rather than forfeit. The flag is what tells the
// interface the move is not a judgement, so the numbers that normally accompany
// one are left off.
//
// msg and attrs are the caller's log line, which is the one thing that differs
// between them.
func (g *Game) playALegalMove(pos *xiangqi.Position, msg string, attrs ...any) (*AIDecision, error) {
	legal := pos.LegalMoves()
	if len(legal) == 0 {
		return nil, ErrNoMove
	}
	g.log.Error(msg, append(attrs, "fallback", engine.Move2Str(legal[0]))...)
	return &AIDecision{
		Move:     engine.Move2Str(legal[0]),
		Source:   SourceSearch,
		Fallback: true,
	}, nil
}

// movesToStrings renders moves in UCCI notation. Move2Str panics on MOVE_NONE,
// so moves that are not real are skipped rather than rendered.
func movesToStrings(moves []xiangqi.Move) []string {
	out := make([]string, 0, len(moves))
	for _, m := range moves {
		if engine.IsOKMove(m) {
			out = append(out, engine.Move2Str(m))
		}
	}
	return out
}
