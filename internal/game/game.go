// Package game is the playable game: the position, whose turn it is, how strong
// the computer plays, and the policy that decides the computer's moves.
//
// The engine itself lives behind internal/xiangqi, and only game decides *when*
// to search. Two rules hold the whole thing together:
//
//   - No lock is held across a network call or a search. The computer's turn
//     snapshots what it needs under a short lock, releases it, thinks, then
//     re-acquires to apply the move — and drops the move if the game changed
//     while it was thinking.
//   - Every engine search in the process goes through searchMu, because the
//     engine's search state is global.
package game

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"

	"github.com/hmgle/godogpaw/engine"

	"chessai/internal/cloud"
	"chessai/internal/xiangqi"
)

// BookTimeout bounds a cloudbook lookup. The computer's turn must not hang on a
// slow third-party service, so when this expires we simply search locally.
const BookTimeout = 2500 * time.Millisecond

// Depth bounds. A level's search depth has to sit inside them; they are the
// range the engine survives. The table's own levels sit far below the ceiling
// and above the floor.
const (
	MinDepth = 1
	MaxDepth = 16
)

// Errors the HTTP layer maps onto status codes. Each is a distinct situation the
// UI can say something useful about, rather than a generic failure.
var (
	ErrGameOver      = errors.New("the game is over")
	ErrNotYourTurn   = errors.New("it is not your turn")
	ErrNotAITurn     = errors.New("it is not the computer's turn")
	ErrAIBusy        = errors.New("the computer is already thinking")
	ErrStale         = errors.New("the game changed while the computer was thinking")
	ErrNoUndo        = errors.New("nothing to undo")
	ErrBadMove       = errors.New("illegal move")
	ErrOffline       = errors.New("the opening book is unavailable")
	ErrNoMove        = errors.New("no legal move is available")
	ErrBadSquare     = errors.New("square is out of range")
	ErrBadLevel      = errors.New("unknown difficulty level")
	ErrBadPlayerSide = errors.New("player side must be red or black")
)

// Config is the settable part of a game.
type Config struct {
	// PlayerSide is the side the human plays; the computer takes the other.
	PlayerSide xiangqi.Side `json:"playerSide"`

	// Level is how strong the computer plays, by name.
	//
	// Only the name travels. How deep a level searches, how far below the best
	// move it will settle — none of that is here, and no client ever sees it, so
	// retuning the ladder is an edit to the level table and nothing else.
	Level string `json:"level"`

	// UseBook lets the computer follow the cloudbook when the position is in it.
	UseBook bool `json:"useBook"`
}

// DefaultConfig is what a new game starts with: the human plays red, the
// computer plays the default level, and the opening book is on.
func DefaultConfig() Config {
	return Config{PlayerSide: xiangqi.Red, Level: DefaultLevel, UseBook: true}
}

// ConfigPatch is a partial configuration change. Nil fields are left alone, so a
// caller can change the book toggle without restating the level.
type ConfigPatch struct {
	Level   *string `json:"level"`
	UseBook *bool   `json:"useBook"`
}

// AIDecision records the computer's move and where it came from, so the UI can
// tell the player whether it followed the book or searched.
type AIDecision struct {
	Move   string `json:"move"`
	Source string `json:"source"` // SourceBook or SourceSearch

	Depth  uint8    `json:"depth,omitempty"`
	Score  int      `json:"score,omitempty"`
	Nodes  uint64   `json:"nodes,omitempty"`
	TimeMs int      `json:"timeMs,omitempty"`
	PV     []string `json:"pv,omitempty"`

	// Fallback marks a move played without a completed search, so the UI does
	// not present a placeholder as an engine result.
	Fallback bool `json:"fallback,omitempty"`
}

// Move sources.
const (
	SourceBook   = "book"
	SourceSearch = "search"
)

// State is the view of a game the web UI renders from. It is a value, so a
// handler can hold it without holding the game's lock.
//
// Reason is a language-neutral code and is the field a UI should render its own
// text from. Status is a coarse English description kept for reading raw
// responses with curl; it duplicates Reason and no client should display it.
type State struct {
	FEN        string         `json:"fen"`
	Board      []int          `json:"board"`
	SideToMove xiangqi.Side   `json:"sideToMove"`
	InCheck    bool           `json:"inCheck"`
	Status     string         `json:"status"`
	Over       bool           `json:"over"`
	Winner     *xiangqi.Side  `json:"winner"`
	Draw       bool           `json:"draw"`
	Reason     xiangqi.Reason `json:"reason,omitempty"`
	LastMove   string         `json:"lastMove,omitempty"`
	History    []string       `json:"history"`

	PlayerSide xiangqi.Side `json:"playerSide"`
	YourTurn   bool         `json:"yourTurn"`
	CanUndo    bool         `json:"canUndo"`
	Level      string       `json:"level"`
	UseBook    bool         `json:"useBook"`

	AI *AIDecision `json:"ai,omitempty"`
}

// Game is a single game of Xiangqi against the computer.
type Game struct {
	// mu guards everything below it. It is never held across a network call or
	// a search.
	mu     sync.Mutex
	pos    *xiangqi.Position
	cfg    Config
	lvl    level  // the resolved strength, kept with cfg and changed with it
	gen    uint64 // bumped by NewGame and Undo to invalidate an in-flight turn
	lastAI *AIDecision

	// searchMu serializes engine searches. This is mandatory, not defensive:
	// godogpaw's stop flag, principal-variation tables, and transposition table
	// are package-level state, so overlapping searches corrupt each other.
	searchMu sync.Mutex

	// aiMu admits one computer turn at a time. TryLock rather than Lock, so a
	// duplicate request is refused instead of queued behind a long search.
	aiMu sync.Mutex

	// rng chooses among candidate moves when a level's window admits more than
	// one. It is reached only through randomIndex, which serializes access — its
	// own lock rather than mu, because selection runs outside mu: a hint and a
	// computer turn can be in flight at once, and rand.Rand is not safe for
	// concurrent use.
	rng   *rand.Rand
	rngMu sync.Mutex

	cloud cloud.Client // nil means offline: the computer always searches
	log   *slog.Logger
}

// Option configures a Game at construction.
type Option func(*Game)

// WithRand supplies the source of randomness used to choose among candidate
// moves. It exists so a test, or a self-play run, can make selection
// reproducible; production leaves it unset and takes a source seeded from the
// clock.
func WithRand(r *rand.Rand) Option {
	return func(g *Game) { g.rng = r }
}

// randomIndex returns a value in [0,n), or 0 when there is nothing to choose
// between.
//
// The lock is the game's own rather than mu, because selection happens outside
// mu — a hint and a computer turn can overlap — and rand.Rand is not safe for
// concurrent use.
func (g *Game) randomIndex(n int) int {
	if n <= 1 {
		return 0
	}
	g.rngMu.Lock()
	defer g.rngMu.Unlock()
	return g.rng.Intn(n)
}

// New starts a game from the opening position. A nil cloud client is allowed and
// means the opening book is unavailable, in which case the computer always
// searches locally.
func New(cfg Config, c cloud.Client, log *slog.Logger, opts ...Option) (*Game, error) {
	pos, err := xiangqi.New("")
	if err != nil {
		return nil, err
	}
	return newFrom(pos, cfg, c, log, opts...)
}

// newFrom builds a game around an existing position.
//
// The public constructors only ever pass the opening position — the web client
// never supplies a FEN — but tests need to start from constructed positions to
// reach checkmate, stalemate, and repetition without playing them out.
func newFrom(pos *xiangqi.Position, cfg Config, c cloud.Client, log *slog.Logger, opts ...Option) (*Game, error) {
	if log == nil {
		log = slog.Default()
	}
	cfg, lvl, err := normalizeConfig(cfg)
	if err != nil {
		return nil, err
	}
	g := &Game{
		pos:   pos,
		cfg:   cfg,
		lvl:   lvl,
		cloud: c,
		log:   log,
		rng:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	for _, opt := range opts {
		opt(g)
	}
	return g, nil
}

// normalizeConfig fills in defaults, resolves the level, and rejects values that
// would make the engine misbehave.
//
// The resolved level comes back alongside the config so a caller cannot end up
// with a name it has not resolved: the two are produced together, from the same
// lookup, and travel together from here on.
func normalizeConfig(cfg Config) (Config, level, error) {
	if cfg.PlayerSide != xiangqi.Red && cfg.PlayerSide != xiangqi.Black {
		return Config{}, level{}, ErrBadPlayerSide
	}
	lvl, err := normalizeLevel(cfg.Level)
	if err != nil {
		return Config{}, level{}, err
	}
	// An unnamed level is written back as the default it resolved to, so the
	// state reports the level actually being played rather than an empty string.
	cfg.Level = lvl.Name
	return cfg, lvl, nil
}

// State returns the current view of the game.
func (g *Game) State() State {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.snapshotLocked()
}

// snapshotLocked builds the State. The caller must hold g.mu.
func (g *Game) snapshotLocked() State {
	result := g.pos.Result()

	st := State{
		FEN:        g.pos.FEN(),
		Board:      boardSlice(g.pos.Board()),
		SideToMove: g.pos.SideToMove(),
		InCheck:    g.pos.InCheck(),
		Status:     g.pos.Status().String(),
		Over:       result.Over,
		Draw:       result.Draw,
		Reason:     result.Reason,
		History:    g.pos.MoveHistoryUCI(),
		PlayerSide: g.cfg.PlayerSide,
		YourTurn:   !result.Over && g.pos.SideToMove() == g.cfg.PlayerSide,
		CanUndo:    g.pos.HistoryLen() > 0,
		Level:      g.lvl.Name,
		UseBook:    g.cfg.UseBook,
		AI:         g.lastAI,
	}
	if result.Over && !result.Draw {
		winner := result.Winner
		st.Winner = &winner
	}
	// Move2Str panics on MOVE_NONE, so only the move that exists is rendered.
	if m := g.pos.LastMove(); engine.IsOKMove(m) {
		st.LastMove = engine.Move2Str(m)
	}
	return st
}

func boardSlice(board [engine.SQUARE_NB]int) []int {
	out := make([]int, len(board))
	copy(out, board[:])
	return out
}

// LegalTargets returns the squares the piece on sq may move to, for the UI's
// move dots.
func (g *Game) LegalTargets(sq xiangqi.Square) ([]xiangqi.Square, error) {
	if sq < 0 || sq >= engine.SQUARE_NB {
		return nil, fmt.Errorf("%w: %d", ErrBadSquare, sq)
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.pos.LegalTargetsFrom(sq), nil
}

// PlayHuman applies the human's move and returns the resulting state.
func (g *Game) PlayHuman(from, to xiangqi.Square) (State, error) {
	if from < 0 || from >= engine.SQUARE_NB || to < 0 || to >= engine.SQUARE_NB {
		return State{}, fmt.Errorf("%w: %d->%d", ErrBadSquare, from, to)
	}
	if from == to {
		// Caught here rather than in the engine, which would call it an illegal
		// move — a true but unhelpful description of clicking one square twice.
		return State{}, fmt.Errorf("%w: from and to are both square %d", ErrBadSquare, from)
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	if g.pos.Result().Over {
		return State{}, ErrGameOver
	}
	if g.pos.SideToMove() != g.cfg.PlayerSide {
		return State{}, ErrNotYourTurn
	}
	if err := g.pos.Play(engine.MakeMove(from, to)); err != nil {
		return State{}, fmt.Errorf("%w: %v", ErrBadMove, err)
	}
	// The computer's previous decision no longer describes this position.
	g.lastAI = nil
	return g.snapshotLocked(), nil
}

// Undo takes back one of the human's moves, along with the computer's reply.
//
// It rewinds until it is the human's turn again rather than popping a fixed
// number of plies, because "two plies" is wrong whenever the computer has not
// replied yet and whenever the human is playing black and the computer moved
// first.
func (g *Game) Undo() (State, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if g.pos.HistoryLen() == 0 {
		return State{}, ErrNoUndo
	}
	// Invalidate any computer turn already in flight, and stop a search that is
	// running right now, so it cannot play into the position being rewound.
	g.gen++
	engine.StopSearch()

	for {
		if !g.pos.Undo() {
			break
		}
		if g.pos.SideToMove() == g.cfg.PlayerSide {
			break
		}
	}
	g.lastAI = nil
	return g.snapshotLocked(), nil
}

// NewGame resets to the opening position with the given configuration.
func (g *Game) NewGame(cfg Config) (State, error) {
	cfg, lvl, err := normalizeConfig(cfg)
	if err != nil {
		return State{}, err
	}
	pos, err := xiangqi.New("")
	if err != nil {
		return State{}, err
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	g.gen++
	engine.StopSearch()

	g.pos = pos
	g.cfg = cfg
	g.lvl = lvl
	g.lastAI = nil
	return g.snapshotLocked(), nil
}

// SetConfig applies a partial configuration change, which may take effect
// mid-game: raising the difficulty or turning the book off affects the
// computer's next move, not the moves already played.
func (g *Game) SetConfig(patch ConfigPatch) (State, error) {
	g.mu.Lock()
	defer g.mu.Unlock()

	if patch.Level != nil {
		lvl, err := normalizeLevel(*patch.Level)
		if err != nil {
			return State{}, err
		}
		g.cfg.Level = lvl.Name
		g.lvl = lvl
	}
	if patch.UseBook != nil {
		g.cfg.UseBook = *patch.UseBook
	}
	return g.snapshotLocked(), nil
}

// Analysis returns the cloudbook's view of the current position for the sidebar.
func (g *Game) Analysis(ctx context.Context) (*cloud.Analysis, error) {
	g.mu.Lock()
	fen := g.pos.FEN()
	c := g.cloud
	g.mu.Unlock()

	if c == nil {
		return nil, ErrOffline
	}
	return c.Analysis(ctx, fen)
}
