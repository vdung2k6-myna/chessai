// Package xiangqi wraps the godogpaw engine as a position/game-rule library.
//
// It is the only package in this project that imports godogpaw, so the engine's
// sharp edges stay contained here:
//
//   - godogpaw has no FEN encoder (see fen.go).
//   - PositionNG.Set calls log.Fatalf on a malformed FEN, so every FEN is
//     validated before it gets there.
//   - Searches print "info ..." lines straight to stdout (see info.go).
//   - The engine's search state is package-global, so searches must never
//     overlap. Callers serialize them; see game.Game.searchMu.
package xiangqi

import (
	"fmt"
	"strings"

	"github.com/hmgle/godogpaw/engine"
)

// StartFEN is the standard Xiangqi opening position.
const StartFEN = "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNR w - - 0 1"

// Sides. godogpaw calls the first player WHITE; in Xiangqi that side is red.
const (
	Red   = engine.WHITE
	Black = engine.BLACK
)

type (
	Move   = engine.MoveNG
	Square = engine.Square
	Side   = engine.Color
)

// Status describes how a game ended, if it has.
type Status int

const (
	Ongoing Status = iota
	Checkmate
	Stalemate
	RepetitionWin
	RepetitionLoss
	RepetitionDraw
	Draw60
)

func (s Status) String() string {
	switch s {
	case Checkmate:
		return "checkmate"
	case Stalemate:
		return "stalemate"
	case RepetitionWin:
		return "win by repetition"
	case RepetitionLoss:
		return "loss by repetition"
	case RepetitionDraw:
		return "draw by repetition"
	case Draw60:
		return "draw by 60-move rule"
	default:
		return "ongoing"
	}
}

// IsOver reports whether the game has finished.
func (s Status) IsOver() bool { return s != Ongoing }

// Reason identifies why a game ended.
//
// These values are part of the HTTP API's contract: the client renders its own
// text from them rather than displaying server prose, so an existing code must
// keep its meaning and a new kind of ending gets a new code.
//
// ReasonPerpetual is deliberately one code for both directions. Which side was
// repeating the check or chase is already carried by Result.Winner, so encoding
// fault in the reason would duplicate it and force every locale to reconstruct
// whose win it is describing.
type Reason string

const (
	ReasonCheckmate  Reason = "checkmate"
	ReasonStalemate  Reason = "stalemate"
	ReasonPerpetual  Reason = "perpetual_check"
	ReasonRepetition Reason = "repetition"
	ReasonSixtyMove  Reason = "sixty_move"
)

// Result is the outcome of a game, resolved to a side.
type Result struct {
	Over   bool
	Winner Side // meaningful only when Over && !Draw
	Draw   bool
	Reason Reason
}

// record pairs a played move with the state the engine pushed for it. UndoMove
// needs that StateInfo back, so we keep it alongside the move.
type record struct {
	move Move
	st   engine.StateInfo
}

// Position is a single Xiangqi position plus the move history needed to undo.
type Position struct {
	pos     engine.PositionNG
	history []record
	start   string
}

// New returns a position set to startFEN, or to StartFEN when startFEN is empty.
func New(startFEN string) (*Position, error) {
	if startFEN == "" {
		startFEN = StartFEN
	}
	if err := ValidateFEN(startFEN); err != nil {
		return nil, err
	}
	p := &Position{start: startFEN}
	p.pos.Set(startFEN)
	return p, nil
}

// FEN renders the current position as FEN.
func (p *Position) FEN() string { return EncodeFEN(&p.pos) }

// SideToMove is the side whose turn it is.
func (p *Position) SideToMove() Side { return p.pos.SideToMove }

// Board is a copy of the 90-square board, indexed by square
// (square = file + rank*9). Zero means empty.
func (p *Position) Board() [engine.SQUARE_NB]int { return p.pos.Board }

// InCheck reports whether the side to move is in check.
func (p *Position) InCheck() bool { return p.pos.Checkers().IsNotZero() }

// PieceOn returns the piece at a square, or zero when empty.
func (p *Position) PieceOn(sq Square) int { return p.pos.Board[sq] }

// LegalMoves returns every legal move in the position.
func (p *Position) LegalMoves() []Move {
	var list [engine.MAX_MOVES]Move
	n := p.pos.GenerateLEGAL(list[:])
	out := make([]Move, n)
	copy(out, list[:n])
	return out
}

// LegalTargetsFrom returns the destination squares reachable from sq, for the
// UI's move dots.
func (p *Position) LegalTargetsFrom(sq Square) []Square {
	var list [engine.MAX_MOVES]Move
	n := p.pos.GenerateLEGAL(list[:])
	out := make([]Square, 0, 8)
	for i := uint8(0); i < n; i++ {
		if engine.FromSQ(list[i]) == sq {
			out = append(out, engine.ToSQ(list[i]))
		}
	}
	return out
}

// Play applies a move, rejecting anything the engine does not consider legal.
func (p *Position) Play(m Move) error {
	if !engine.IsOKMove(m) {
		return fmt.Errorf("not a playable move")
	}
	if !p.isLegal(m) {
		// Phrased as a statement about the move rather than starting with
		// "illegal move", so callers that wrap this in their own illegal-move
		// error do not produce "illegal move: illegal move ...".
		return fmt.Errorf("%s is not legal in %s", engine.Move2Str(m), p.FEN())
	}
	var st engine.StateInfo
	p.pos.DoMove(m, &st)
	p.history = append(p.history, record{move: m, st: st})
	return nil
}

// PlayUCI applies a move given in UCCI coordinate notation (e.g. "b0c2"), which
// is also the notation chessdb.cn uses.
func (p *Position) PlayUCI(s string) (Move, error) {
	// engine.ParseUCIMove only requires len >= 4 and looks at the first four
	// characters, so it would silently read "c3c4c5" as "c3c4". Moves here can
	// come from a remote server, so require an exact match rather than trusting
	// a prefix. Xiangqi has no drops or promotions that would add a suffix.
	norm := strings.ToLower(strings.TrimSpace(s))
	if len(norm) != 4 {
		return engine.MOVE_NONE, fmt.Errorf("move %q must be exactly 4 characters, like %q", s, "b0c2")
	}
	m, err := engine.ParseUCIMove(&p.pos, norm)
	if err != nil {
		return engine.MOVE_NONE, err
	}
	if err := p.Play(m); err != nil {
		return engine.MOVE_NONE, err
	}
	return m, nil
}

// Undo takes back the most recent move, reporting whether there was one.
func (p *Position) Undo() bool {
	if len(p.history) == 0 {
		return false
	}
	last := p.history[len(p.history)-1]
	p.pos.UndoMove(last.move)
	p.history = p.history[:len(p.history)-1]
	return true
}

// HistoryLen is the number of moves played since the start position.
func (p *Position) HistoryLen() int { return len(p.history) }

// MoveHistoryUCI returns the moves played so far in UCCI notation.
func (p *Position) MoveHistoryUCI() []string {
	out := make([]string, len(p.history))
	for i, r := range p.history {
		out[i] = engine.Move2Str(r.move)
	}
	return out
}

// LastMove returns the most recent move, or MOVE_NONE when none has been played.
func (p *Position) LastMove() Move {
	if len(p.history) == 0 {
		return engine.MOVE_NONE
	}
	return p.history[len(p.history)-1].move
}

func (p *Position) isLegal(m Move) bool {
	// A move is legal exactly when it appears in the generated legal list, so
	// there is no need for a separate rule check.
	var list [engine.MAX_MOVES]Move
	n := p.pos.GenerateLEGAL(list[:])
	for i := uint8(0); i < n; i++ {
		if list[i] == m {
			return true
		}
	}
	return false
}

// Status classifies the position.
func (p *Position) Status() Status {
	// In Xiangqi a side with no legal move loses, whether it is checkmate (将死)
	// or stalemate (困毙) — unlike international chess, where stalemate draws.
	if len(p.LegalMoves()) == 0 {
		if p.InCheck() {
			return Checkmate
		}
		return Stalemate
	}
	// ClassifyRepetition must only be called when IsRepetition is true.
	if p.pos.IsRepetition() {
		switch p.pos.ClassifyRepetition() {
		case engine.REP_WIN:
			return RepetitionWin
		case engine.REP_LOSE:
			return RepetitionLoss
		case engine.REP_DRAW:
			return RepetitionDraw
		}
	}
	if p.pos.IsDraw() {
		return Draw60
	}
	return Ongoing
}

// Result resolves Status to a winner, from the perspective of the side to move.
func (p *Position) Result() Result {
	us := p.SideToMove()
	them := other(us)

	switch p.Status() {
	case Checkmate:
		return Result{Over: true, Winner: them, Reason: ReasonCheckmate}
	case Stalemate:
		// 困毙 — no legal move and not in check, which in Xiangqi loses for the
		// side to move just as checkmate does. Only the reason differs.
		return Result{Over: true, Winner: them, Reason: ReasonStalemate}
	case RepetitionWin:
		// The opponent is the side repeating the check or chase, so it loses.
		return Result{Over: true, Winner: us, Reason: ReasonPerpetual}
	case RepetitionLoss:
		return Result{Over: true, Winner: them, Reason: ReasonPerpetual}
	case RepetitionDraw:
		return Result{Over: true, Draw: true, Reason: ReasonRepetition}
	case Draw60:
		return Result{Over: true, Draw: true, Reason: ReasonSixtyMove}
	}
	return Result{}
}

// Evaluate is the engine's static evaluation, in centipawns from the side to
// move's perspective.
func (p *Position) Evaluate() engine.Value { return p.pos.Evaluate() }

// Search depth bounds. Depth 0 means "no limit" to the engine, which without a
// time limit would search essentially forever; and a large depth risks running
// GamePly past the end of the engine's PvLength table once check extensions pile
// up. The difficulty presets top out well below the ceiling.
const (
	minSearchDepth = 1
	maxSearchDepth = 32
)

// clampDepth keeps a requested depth inside the range the engine can survive.
func clampDepth(d uint8) uint8 {
	if d < minSearchDepth {
		return minSearchDepth
	}
	if d > maxSearchDepth {
		return maxSearchDepth
	}
	return d
}

// Search runs the engine's search and returns the move plus search statistics.
//
// The search runs on a copy of the position, which matters for two reasons.
// SearchPositionWithLimits calls clearSearch, and clearSearch resets GamePly —
// which drives both the FEN's move counter and the engine's repetition
// walk-back — so searching in place corrupts the position it searched. And
// because the copy is private, a caller can keep reading the live position
// through /api/state and /api/legal while a search is in flight.
//
// Callers must still serialize searches: the engine's stop flag, PV tables, and
// transposition table are shared package state, so two concurrent searches
// corrupt each other.
func (p *Position) Search(limits engine.SearchLimits) (res SearchResult, err error) {
	// A panic deep in the engine must not take the process down; a server that
	// cannot search should still be able to play a legal move.
	defer func() {
		if r := recover(); r != nil {
			res, err = SearchResult{}, fmt.Errorf("engine panicked during search: %v", r)
		}
	}()

	limits.Depth = clampDepth(limits.Depth)
	// Time limits are deliberately refused. SearchPositionWithLimits starts an
	// uncancelled timer goroutine that sets the global stop flag when it fires,
	// so a limit that outlives its search aborts the next, unrelated one. Depth
	// is the only safe limit here.
	limits.TimeLimit = 0

	clone, err := p.Clone()
	if err != nil {
		return SearchResult{}, err
	}

	res = searchWithInfo(&clone.pos, limits)

	// A search cut short before its first iteration completes leaves MOVE_NONE.
	// Hand back a legal move instead of a move no caller can play.
	if !engine.IsOKMove(res.BestMove) {
		if legal := p.LegalMoves(); len(legal) > 0 {
			res.BestMove = legal[0]
			res.Fallback = true
		}
	}
	return res, nil
}

// Clone returns an independent copy of the position, with the move history
// replayed into a fresh engine state.
//
// Replay rather than a struct copy: PositionNG's state stack is a slice of
// pointers, so copying the struct would leave the clone sharing a backing array
// with the original, and the two would overwrite each other's entries as they
// pushed states. Replaying gives the clone its own history — which the engine's
// repetition detection needs — and costs a few microseconds.
func (p *Position) Clone() (*Position, error) {
	c := &Position{start: p.start}
	c.pos.Set(p.start)
	var st engine.StateInfo
	c.history = make([]record, 0, len(p.history))
	for _, r := range p.history {
		c.pos.DoMove(r.move, &st)
		c.history = append(c.history, record{move: r.move, st: st})
	}
	// A clone that disagreed with the live position would mean the replay and
	// the played history had diverged, and the clone would answer a question
	// nobody asked. Cheap to check, and silent when it is right.
	if c.pos.SideToMove != p.pos.SideToMove || c.pos.Board != p.pos.Board {
		return nil, fmt.Errorf("clone diverged from the live position at %s", p.FEN())
	}
	return c, nil
}

func other(c Side) Side {
	if c == engine.WHITE {
		return engine.BLACK
	}
	return engine.WHITE
}
