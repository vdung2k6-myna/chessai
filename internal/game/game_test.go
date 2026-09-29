package game

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"math/rand"
	"sync"
	"testing"
	"time"

	"chessai/internal/cloud"
	"chessai/internal/xiangqi"
)

// fakeCloud stands in for chessdb.cn.
type fakeCloud struct {
	best    string
	bestErr error

	analysis    *cloud.Analysis
	analysisErr error

	// entered, when non-nil, receives once when a book lookup is reached, so a
	// test can act while one is in flight. release, when non-nil, blocks that
	// lookup until it is closed.
	entered chan struct{}
	release chan struct{}

	mu    sync.Mutex
	calls int
}

func (f *fakeCloud) BestMove(ctx context.Context, fen string) (string, error) {
	if err := f.enter(ctx); err != nil {
		return "", err
	}
	return f.best, f.bestErr
}

// enter counts a book lookup and holds it until the test releases it.
//
// Both of the book's calls go through here, so a test that needs a lookup in
// flight does not depend on which one the level takes. A level with no window
// plays the book's best move; a level with a window weighs the book's whole
// ranking, which is a different call. Either is a lookup, and a test about what
// the game does during one should not have to know which it got.
func (f *fakeCloud) enter(ctx context.Context) error {
	f.mu.Lock()
	f.calls++
	entered, release := f.entered, f.release
	f.mu.Unlock()

	if entered != nil {
		select {
		case entered <- struct{}{}:
		default:
		}
	}
	if release != nil {
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return nil
}

func (f *fakeCloud) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

// Analysis serves the book's other call from the same fake book.
//
// A level with a window weighs the book's whole ranking, and a level without one
// plays the book's best move; a test about the book should not have to say which
// of those the level it built happens to be. So the fake models one book that
// answers both calls: an explicit analysis wins when a test wants one, and
// otherwise the book knows the position iff it has a best move, and ranks that
// move alone.
func (f *fakeCloud) Analysis(ctx context.Context, fen string) (*cloud.Analysis, error) {
	if err := f.enter(ctx); err != nil {
		return nil, err
	}
	if f.analysis != nil || f.analysisErr != nil {
		return f.analysis, f.analysisErr
	}
	if f.bestErr != nil {
		return nil, f.bestErr
	}
	return &cloud.Analysis{Known: f.best != "", Best: f.best}, nil
}

// discardLogger keeps test output readable; the game logs recoverable problems
// at debug and error level on paths several tests exercise deliberately.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fixedRand makes a game's move selection reproducible.
//
// Selection is random by design — a weaker level chooses among near-equal moves
// — so a game left on its production source would decide differently between
// runs, and an assertion on a specific move would be flaky rather than wrong.
// Every game a test builds takes this, so the tests stay about the rules and the
// policy instead of about the dice.
func fixedRand() Option {
	return WithRand(rand.New(rand.NewSource(1)))
}

// mustPosition builds a position from a FEN and a list of moves.
func mustPosition(t *testing.T, fen string, moves ...string) *xiangqi.Position {
	t.Helper()
	p, err := xiangqi.New(fen)
	if err != nil {
		t.Fatalf("xiangqi.New(%q): %v", fen, err)
	}
	for _, mv := range moves {
		if _, err := p.PlayUCI(mv); err != nil {
			t.Fatalf("PlayUCI(%q): %v", mv, err)
		}
	}
	return p
}

// sq converts file and rank to a square, matching the engine's encoding.
func sq(file, rank int) xiangqi.Square { return xiangqi.Square(file + rank*9) }

// A legal opening move for each side, used to drive positions forward.
var (
	redPawnMove   = [2]xiangqi.Square{sq(2, 3), sq(2, 4)} // c3c4
	blackPawnMove = [2]xiangqi.Square{sq(2, 6), sq(2, 5)} // c6c5
)

func TestAIMoveUsesBookWhenAvailable(t *testing.T) {
	fake := &fakeCloud{best: "b0c2"}
	// The human plays black, so it is the computer's turn from the start.
	g, err := New(Config{PlayerSide: xiangqi.Black, Level: LevelBeginner, UseBook: true}, fake, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	decision, err := g.AIMove(context.Background())
	if err != nil {
		t.Fatalf("AIMove: %v", err)
	}
	if decision.Source != SourceBook {
		t.Errorf("Source = %q, want %q", decision.Source, SourceBook)
	}
	if decision.Move != "b0c2" {
		t.Errorf("Move = %q, want %q", decision.Move, "b0c2")
	}

	// The book move must actually be on the board, not just reported.
	st := g.State()
	if len(st.History) != 1 || st.History[0] != "b0c2" {
		t.Errorf("History = %v, want [b0c2]", st.History)
	}
	if st.SideToMove != xiangqi.Black {
		t.Errorf("SideToMove = %v, want black (the human)", st.SideToMove)
	}
	if st.AI == nil || st.AI.Source != SourceBook {
		t.Errorf("State().AI = %+v, want the book decision", st.AI)
	}
}

func TestAIMoveSearchesWhenOutOfBook(t *testing.T) {
	fake := &fakeCloud{best: ""}
	g, err := New(Config{PlayerSide: xiangqi.Black, Level: LevelBeginner, UseBook: true}, fake, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	decision, err := g.AIMove(context.Background())
	if err != nil {
		t.Fatalf("AIMove: %v", err)
	}
	if decision.Source != SourceSearch {
		t.Errorf("Source = %q, want %q", decision.Source, SourceSearch)
	}
	if decision.Fallback {
		t.Error("Fallback = true, but every search in this position should have completed")
	}
	if decision.Depth == 0 {
		t.Error("Depth = 0, want the depth the level judges its moves at")
	}

	st := g.State()
	if len(st.History) != 1 {
		t.Fatalf("History = %v, want one move", st.History)
	}
	if st.FEN == xiangqi.StartFEN {
		t.Error("the position did not change")
	}
}

func TestAIMoveRejectsIllegalBookMove(t *testing.T) {
	// a0a3 runs the red rook into its own pawn. The book is a third party, so a
	// move we cannot play must fall back to a search rather than be trusted.
	fake := &fakeCloud{best: "a0a3"}
	g, err := New(Config{PlayerSide: xiangqi.Black, Level: LevelBeginner, UseBook: true}, fake, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	decision, err := g.AIMove(context.Background())
	if err != nil {
		t.Fatalf("AIMove: %v", err)
	}
	if decision.Source != SourceSearch {
		t.Errorf("Source = %q, want %q after an illegal book move", decision.Source, SourceSearch)
	}
	if decision.Move == "a0a3" {
		t.Error("the illegal book move was played anyway")
	}
	if got := g.State().History; len(got) != 1 || got[0] == "a0a3" {
		t.Errorf("History = %v, want one legal move", got)
	}
}

func TestAIMoveSearchesWhenBookFails(t *testing.T) {
	fake := &fakeCloud{bestErr: errors.New("cloudbook unreachable")}
	g, err := New(Config{PlayerSide: xiangqi.Black, Level: LevelBeginner, UseBook: true}, fake, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	decision, err := g.AIMove(context.Background())
	if err != nil {
		t.Fatalf("a dead cloudbook must not fail the turn: %v", err)
	}
	if decision.Source != SourceSearch {
		t.Errorf("Source = %q, want %q", decision.Source, SourceSearch)
	}
}

func TestBookIsSkippedWhenDisabled(t *testing.T) {
	fake := &fakeCloud{best: "b0c2"}
	g, err := New(Config{PlayerSide: xiangqi.Black, Level: LevelBeginner, UseBook: false}, fake, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	decision, err := g.AIMove(context.Background())
	if err != nil {
		t.Fatalf("AIMove: %v", err)
	}
	if decision.Source != SourceSearch {
		t.Errorf("Source = %q, want %q with the book off", decision.Source, SourceSearch)
	}
	if n := fake.callCount(); n != 0 {
		t.Errorf("the cloudbook was queried %d times with the book off, want 0", n)
	}
}

// bookGame builds a game the computer opens, so the book is the first thing
// consulted. The human plays black, which leaves red — and the first move — to
// the computer.
func bookGame(t *testing.T, fake cloud.Client, seed int64) (*Game, *xiangqi.Position) {
	t.Helper()
	pos := mustPosition(t, xiangqi.StartFEN)
	g, err := newFrom(pos, Config{PlayerSide: xiangqi.Black, Level: LevelBeginner, UseBook: true},
		fake, discardLogger(), WithRand(rand.New(rand.NewSource(seed))))
	if err != nil {
		t.Fatalf("newFrom: %v", err)
	}
	return g, pos
}

// TestBookedLevelChoosesAmongTheBooksRankedMoves checks what the book is for at
// a level that is allowed to err: the candidates are the moves the book ranks,
// and the level chooses among them rather than among every legal move.
//
// This is the opening's whole problem. An opening position is flat enough that a
// window over its legal move list holds every move in it, so such a level would
// open by shuffling a rook. The book's ranking is the shorter list that stops
// it.
func TestBookedLevelChoosesAmongTheBooksRankedMoves(t *testing.T) {
	// Red's three most ordinary opening moves, in the book's order.
	ranked := []cloud.Move{
		{Move: "b0c2", Rank: 1, Score: 30},
		{Move: "c3c4", Rank: 2, Score: 20},
		{Move: "h2e2", Rank: 3, Score: 10},
	}
	fake := &fakeCloud{analysis: &cloud.Analysis{
		FEN: xiangqi.StartFEN, Known: true, Best: "b0c2", Moves: ranked,
	}}

	// A window wide enough to hold every candidate, so what is played is limited
	// by the candidate list rather than by the window.
	lvl := level{Name: "test-book", Depth: 2, Window: 100000}

	allowed := make(map[string]bool, len(ranked))
	for _, m := range ranked {
		allowed[m.Move] = true
	}

	seen := make(map[string]bool, len(ranked))
	for seed := range int64(20) {
		g, pos := bookGame(t, fake, seed)

		decision, err := g.decide(context.Background(), pos, pos.FEN(), g.cfg, lvl)
		if err != nil {
			t.Fatalf("seed %d: decide: %v", seed, err)
		}
		if decision.Source != SourceBook {
			t.Errorf("seed %d: source = %q, want %q", seed, decision.Source, SourceBook)
		}
		if !allowed[decision.Move] {
			t.Errorf("seed %d played %s, which is not one of the book's ranked moves", seed, decision.Move)
		}
		// The book's moves were ranked to choose among them, so the turn cost
		// that ranking and the decision says so. A book position is the one place
		// a level has always run a pass and nothing else, and a turn that showed
		// no cost while the interface displays one would read as a free move.
		if decision.Nodes == 0 {
			t.Errorf("seed %d reported no nodes, want what ranking the book's moves cost", seed)
		}
		seen[decision.Move] = true
	}
	if len(seen) < 2 {
		t.Errorf("a wide window over the book's list produced %d distinct move(s); the level "+
			"is not choosing among the book's moves", len(seen))
	}
}

// TestBookCandidatesExcludeMovesThePositionDoesNotAllow checks the book's list is
// filtered rather than trusted: a ranked move the position does not permit is
// not a candidate, however wide the window is.
func TestBookCandidatesExcludeMovesThePositionDoesNotAllow(t *testing.T) {
	// a0a3 runs the red rook into its own pawn. It leads the book's ranking, so a
	// level that believed the book would play it first.
	fake := &fakeCloud{analysis: &cloud.Analysis{Known: true, Best: "a0a3", Moves: []cloud.Move{
		{Move: "a0a3", Rank: 1},
		{Move: "b0c2", Rank: 2},
	}}}
	lvl := level{Name: "test-book", Depth: 2, Window: 100000}

	for seed := range int64(10) {
		g, pos := bookGame(t, fake, seed)

		decision, err := g.decide(context.Background(), pos, pos.FEN(), g.cfg, lvl)
		if err != nil {
			t.Fatalf("seed %d: decide: %v", seed, err)
		}
		if decision.Move != "b0c2" {
			t.Errorf("seed %d played %s, want the only ranked move this position allows (b0c2)",
				seed, decision.Move)
		}
		if decision.Source != SourceBook {
			t.Errorf("seed %d: source = %q, want %q", seed, decision.Source, SourceBook)
		}
	}
}

// TestBookedLevelSearchesWhenTheBookListsNothingPlayable checks the book degrades
// to a search rather than failing the turn: a position the book has an entry for
// but no usable move in is just a position to search.
func TestBookedLevelSearchesWhenTheBookListsNothingPlayable(t *testing.T) {
	fake := &fakeCloud{analysis: &cloud.Analysis{
		Known: true, Best: "a0a3", Moves: []cloud.Move{{Move: "a0a3", Rank: 1}},
	}}
	lvl := level{Name: "test-book", Depth: 2, Window: 100000}

	g, pos := bookGame(t, fake, 1)
	decision, err := g.decide(context.Background(), pos, pos.FEN(), g.cfg, lvl)
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if decision.Source != SourceSearch {
		t.Errorf("source = %q, want %q when the book lists nothing playable",
			decision.Source, SourceSearch)
	}
}

// TestWindowlessBookLevelPlaysTheBooksBestMove covers the other half of the
// book's contract: a level with no window has no weaker move to weigh, so it
// plays the move the database itself ranks first and never touches the ranking.
//
// It is called directly because the level table no longer reaches this path from
// a name a test can afford: master is the only windowless level and it searches
// to depth 8. The path still has to work — it is what master plays in the
// opening — so it is tested where it is written rather than left to a level.
func TestWindowlessBookLevelPlaysTheBooksBestMove(t *testing.T) {
	fake := &fakeCloud{best: "b0c2", analysis: &cloud.Analysis{Known: true, Best: "b0c2", Moves: []cloud.Move{
		{Move: "b0c2", Rank: 1},
		{Move: "c3c4", Rank: 2},
	}}}
	lvl := level{Name: "test-windowless", Depth: 2, Window: 0}

	for seed := range int64(10) {
		g, pos := bookGame(t, fake, seed)

		decision, err := g.decide(context.Background(), pos, pos.FEN(), g.cfg, lvl)
		if err != nil {
			t.Fatalf("seed %d: decide: %v", seed, err)
		}
		if decision.Move != "b0c2" {
			t.Errorf("seed %d played %s, want the book's own best move (b0c2) however the "+
				"ranking varies", seed, decision.Move)
		}
		if decision.Source != SourceBook {
			t.Errorf("seed %d: source = %q, want %q", seed, decision.Source, SourceBook)
		}
	}
}

func TestOfflineGameStillPlays(t *testing.T) {
	// A nil client is the offline case: no book at all, but the game still works.
	g, err := New(Config{PlayerSide: xiangqi.Black, Level: LevelBeginner, UseBook: true}, nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	decision, err := g.AIMove(context.Background())
	if err != nil {
		t.Fatalf("AIMove offline: %v", err)
	}
	if decision.Source != SourceSearch {
		t.Errorf("Source = %q, want %q offline", decision.Source, SourceSearch)
	}
	if _, err := g.Analysis(context.Background()); !errors.Is(err, ErrOffline) {
		t.Errorf("Analysis offline = %v, want %v", err, ErrOffline)
	}
}

func TestAIMoveRefusedOnThePlayersTurn(t *testing.T) {
	g, err := New(DefaultConfig(), nil, discardLogger(), fixedRand()) // human plays red, red to move
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.AIMove(context.Background()); !errors.Is(err, ErrNotAITurn) {
		t.Errorf("AIMove on the player's turn = %v, want %v", err, ErrNotAITurn)
	}
}

func TestAIMoveRefusedWhileAlreadyThinking(t *testing.T) {
	release := make(chan struct{})
	fake := &fakeCloud{best: "b0c2", entered: make(chan struct{}, 1), release: release}
	g, err := New(Config{PlayerSide: xiangqi.Black, Level: LevelBeginner, UseBook: true}, fake, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	first := make(chan error, 1)
	go func() {
		_, err := g.AIMove(context.Background())
		first <- err
	}()

	// Wait until the first turn is genuinely in flight inside the book lookup.
	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the first AI turn never reached the cloudbook")
	}

	// A second turn must be refused, not queued behind the first.
	if _, err := g.AIMove(context.Background()); !errors.Is(err, ErrAIBusy) {
		t.Errorf("second AIMove = %v, want %v", err, ErrAIBusy)
	}

	close(release)
	if err := <-first; err != nil {
		t.Fatalf("first AIMove: %v", err)
	}
	if got := g.State().History; len(got) != 1 {
		t.Errorf("History = %v, want exactly one move", got)
	}
}

func TestAITurnIsDiscardedWhenTheGameChanges(t *testing.T) {
	release := make(chan struct{})
	fake := &fakeCloud{best: "b0c2", entered: make(chan struct{}, 1), release: release}
	g, err := New(Config{PlayerSide: xiangqi.Red, Level: LevelBeginner, UseBook: true}, fake, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	// The human moves, handing the turn to the computer.
	if _, err := g.PlayHuman(redPawnMove[0], redPawnMove[1]); err != nil {
		t.Fatal(err)
	}

	thinking := make(chan error, 1)
	go func() {
		_, err := g.AIMove(context.Background())
		thinking <- err
	}()
	select {
	case <-fake.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the AI turn never reached the cloudbook")
	}

	// The human takes their move back while the computer is still thinking.
	st, err := g.Undo()
	if err != nil {
		t.Fatalf("Undo: %v", err)
	}
	if len(st.History) != 0 || st.SideToMove != xiangqi.Red {
		t.Fatalf("after Undo: history %v, side %v; want empty and red", st.History, st.SideToMove)
	}

	close(release)
	if err := <-thinking; !errors.Is(err, ErrStale) {
		t.Errorf("the in-flight AI turn = %v, want %v", err, ErrStale)
	}
	// The discarded move must not have landed on the board.
	if got := g.State().History; len(got) != 0 {
		t.Errorf("History = %v, want empty: the stale move was played anyway", got)
	}
}

func TestUndoRewindsToThePlayersTurn(t *testing.T) {
	// The four shapes of "undo": with and without the computer's reply, for a
	// human playing red and playing black. Popping a fixed two plies gets three
	// of these wrong.
	tests := []struct {
		name        string
		player      xiangqi.Side
		play        func(t *testing.T, g *Game)
		wantHistory int
	}{
		{
			name:   "red, computer has not replied",
			player: xiangqi.Red,
			play: func(t *testing.T, g *Game) {
				t.Helper()
				mustPlayHuman(t, g, redPawnMove)
			},
			wantHistory: 0,
		},
		{
			name:   "red, computer has replied",
			player: xiangqi.Red,
			play: func(t *testing.T, g *Game) {
				t.Helper()
				mustPlayHuman(t, g, redPawnMove)
				mustAIMove(t, g)
			},
			wantHistory: 0,
		},
		{
			name:   "black, computer has not replied to the human yet",
			player: xiangqi.Black,
			play: func(t *testing.T, g *Game) {
				t.Helper()
				mustAIMove(t, g) // red, the computer, opens
				mustPlayHuman(t, g, blackPawnMove)
			},
			wantHistory: 1,
		},
		{
			name:   "black, computer has replied",
			player: xiangqi.Black,
			play: func(t *testing.T, g *Game) {
				t.Helper()
				mustAIMove(t, g)
				mustPlayHuman(t, g, blackPawnMove)
				mustAIMove(t, g)
			},
			wantHistory: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := &fakeCloud{} // out of book, so the computer searches
			g, err := New(Config{PlayerSide: tc.player, Level: LevelBeginner, UseBook: true}, fake, discardLogger(), fixedRand())
			if err != nil {
				t.Fatal(err)
			}
			tc.play(t, g)

			st, err := g.Undo()
			if err != nil {
				t.Fatalf("Undo: %v", err)
			}
			if st.SideToMove != tc.player {
				t.Errorf("SideToMove = %v after Undo, want %v (the player)", st.SideToMove, tc.player)
			}
			if len(st.History) != tc.wantHistory {
				t.Errorf("History = %v after Undo, want %d moves", st.History, tc.wantHistory)
			}
			if !st.YourTurn {
				t.Error("YourTurn = false after Undo, want true")
			}
			if st.AI != nil {
				t.Errorf("AI = %+v after Undo, want nil", st.AI)
			}
		})
	}
}

func TestUndoWithNothingToUndo(t *testing.T) {
	g, err := New(DefaultConfig(), nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Undo(); !errors.Is(err, ErrNoUndo) {
		t.Errorf("Undo at the start = %v, want %v", err, ErrNoUndo)
	}
}

func TestPlayHumanRejectsBadMoves(t *testing.T) {
	g, err := New(Config{PlayerSide: xiangqi.Red, Level: LevelBeginner}, nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	// Red's own knight sits on b0.
	if _, err := g.PlayHuman(sq(0, 0), sq(1, 0)); !errors.Is(err, ErrBadMove) {
		t.Errorf("moving onto your own piece = %v, want %v", err, ErrBadMove)
	}
	if _, err := g.PlayHuman(-1, 5); !errors.Is(err, ErrBadSquare) {
		t.Errorf("negative square = %v, want %v", err, ErrBadSquare)
	}
	if _, err := g.PlayHuman(sq(0, 0), 99); !errors.Is(err, ErrBadSquare) {
		t.Errorf("out-of-range square = %v, want %v", err, ErrBadSquare)
	}
	// A move from a square to itself is a mistake in the request, not an
	// illegal move in the game, so it must not be reported as the latter.
	if _, err := g.PlayHuman(sq(4, 4), sq(4, 4)); !errors.Is(err, ErrBadSquare) {
		t.Errorf("same from and to = %v, want %v", err, ErrBadSquare)
	}

	// A legal move succeeds, and then it is not the human's turn any more.
	if _, err := g.PlayHuman(redPawnMove[0], redPawnMove[1]); err != nil {
		t.Fatalf("legal move rejected: %v", err)
	}
	if _, err := g.PlayHuman(blackPawnMove[0], blackPawnMove[1]); !errors.Is(err, ErrNotYourTurn) {
		t.Errorf("moving for the other side = %v, want %v", err, ErrNotYourTurn)
	}
}

func TestStateReportsCheckmateAsALoss(t *testing.T) {
	// Red rooks on a9 and d0; black's king on e9 has no escape.
	pos := mustPosition(t, "R3k4/9/9/9/9/9/9/9/9/3KR4 b - - 0 1")
	g, err := newFrom(pos, Config{PlayerSide: xiangqi.Black, Level: LevelBeginner}, nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	st := g.State()
	if !st.Over {
		t.Fatal("Over = false, want true")
	}
	if st.Draw {
		t.Error("Draw = true, want false")
	}
	if st.Winner == nil || *st.Winner != xiangqi.Red {
		t.Errorf("Winner = %v, want red", st.Winner)
	}
	if st.YourTurn {
		t.Error("YourTurn = true in a finished game")
	}
	if st.Status != "checkmate" {
		t.Errorf("Status = %q, want %q", st.Status, "checkmate")
	}
	if st.Reason != xiangqi.ReasonCheckmate {
		t.Errorf("Reason = %q, want %q", st.Reason, xiangqi.ReasonCheckmate)
	}
	if _, err := g.AIMove(context.Background()); !errors.Is(err, ErrGameOver) {
		t.Errorf("AIMove in a finished game = %v, want %v", err, ErrGameOver)
	}
	if _, err := g.PlayHuman(sq(4, 9), sq(4, 8)); !errors.Is(err, ErrGameOver) {
		t.Errorf("PlayHuman in a finished game = %v, want %v", err, ErrGameOver)
	}
}

func TestStateReportsStalemateAsALoss(t *testing.T) {
	// Black's king on e9 is not attacked but has no legal move: 困毙, which
	// Xiangqi scores as a loss for the side to move, not a draw.
	pos := mustPosition(t, "4k4/R8/9/9/9/9/9/9/9/2KR1R3 b - - 0 1")
	g, err := newFrom(pos, Config{PlayerSide: xiangqi.Black, Level: LevelBeginner}, nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	st := g.State()
	if !st.Over {
		t.Fatal("Over = false, want true")
	}
	if st.Draw {
		t.Error("Draw = true, but stalemate is a loss in Xiangqi")
	}
	if st.Winner == nil || *st.Winner != xiangqi.Red {
		t.Errorf("Winner = %v, want red", st.Winner)
	}
	if st.Status != "stalemate" {
		t.Errorf("Status = %q, want %q", st.Status, "stalemate")
	}
	if st.Reason != xiangqi.ReasonStalemate {
		t.Errorf("Reason = %q, want %q", st.Reason, xiangqi.ReasonStalemate)
	}
}

func TestStateReportsPerpetualCheckAgainstTheOffender(t *testing.T) {
	// Red shuttles its rook onto the e-file, checking the black king, while the
	// king shuffles: red is the side repeating and loses.
	pos := mustPosition(t, "4k4/9/9/9/9/9/9/9/R8/5K3 w - - 0 1",
		"a1e1", "e9d9", "e1d1", "d9e9", "d1e1", "e9d9", "e1d1", "d9e9")
	g, err := newFrom(pos, Config{PlayerSide: xiangqi.Red, Level: LevelBeginner}, nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	st := g.State()
	if !st.Over {
		t.Fatal("Over = false, want true")
	}
	if st.Winner == nil || *st.Winner != xiangqi.Black {
		t.Errorf("Winner = %v, want black (red is the perpetual checker)", st.Winner)
	}
	if st.Status != "loss by repetition" {
		t.Errorf("Status = %q, want %q", st.Status, "loss by repetition")
	}
	// One code covers both directions of a perpetual check; Winner above says
	// which side lost, so the reason does not have to repeat it.
	if st.Reason != xiangqi.ReasonPerpetual {
		t.Errorf("Reason = %q, want %q", st.Reason, xiangqi.ReasonPerpetual)
	}
}

func TestSetConfigAppliesAndValidates(t *testing.T) {
	g, err := New(DefaultConfig(), nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	level, book := LevelHard, false
	st, err := g.SetConfig(ConfigPatch{Level: &level, UseBook: &book})
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if st.Level != LevelHard || st.UseBook {
		t.Errorf("State = level %q, book %v; want %q and false", st.Level, st.UseBook, LevelHard)
	}

	// A patch that omits a field leaves it alone.
	reEnabled := true
	st, err = g.SetConfig(ConfigPatch{UseBook: &reEnabled})
	if err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if st.Level != LevelHard || !st.UseBook {
		t.Errorf("State = level %q, book %v; want the level kept at %q and book true",
			st.Level, st.UseBook, LevelHard)
	}

	// An empty name is not in the list: it means "no preference" and resolves to
	// the default level, which is what lets an unnamed /api/new fall back rather
	// than fail. Only a name that is not a level is refused.
	for _, bad := range []string{"grandmaster", "9", "HARD", " ", "harder"} {
		if _, err := g.SetConfig(ConfigPatch{Level: &bad}); !errors.Is(err, ErrBadLevel) {
			t.Errorf("SetConfig(level %q) = %v, want %v", bad, err, ErrBadLevel)
		}
	}
	if _, err := g.SetConfig(ConfigPatch{UseBook: &reEnabled}); err != nil {
		t.Fatal(err)
	}
	// The rejected levels must not have taken effect.
	if st := g.State(); st.Level != LevelHard {
		t.Errorf("Level = %q after rejected updates, want %q", st.Level, LevelHard)
	}
}

func TestNewGameResets(t *testing.T) {
	g, err := New(Config{PlayerSide: xiangqi.Red, Level: LevelEasy, UseBook: true}, nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}
	mustPlayHuman(t, g, redPawnMove)
	mustAIMove(t, g)
	if got := g.State(); len(got.History) != 2 {
		t.Fatalf("History = %v, want 2 moves before the reset", got.History)
	}

	st, err := g.NewGame(Config{PlayerSide: xiangqi.Black, Level: LevelEasy, UseBook: false})
	if err != nil {
		t.Fatalf("NewGame: %v", err)
	}
	if st.FEN != xiangqi.StartFEN {
		t.Errorf("FEN = %q, want the opening position", st.FEN)
	}
	if len(st.History) != 0 {
		t.Errorf("History = %v, want empty", st.History)
	}
	if st.PlayerSide != xiangqi.Black || st.Level != LevelEasy || st.UseBook {
		t.Errorf("config not applied: side %v, level %q, book %v", st.PlayerSide, st.Level, st.UseBook)
	}
	if st.AI != nil {
		t.Errorf("AI = %+v, want nil after a reset", st.AI)
	}
	if st.CanUndo {
		t.Error("CanUndo = true with an empty history")
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(Config{PlayerSide: 7}, nil, discardLogger(), fixedRand()); !errors.Is(err, ErrBadPlayerSide) {
		t.Errorf("New with a bogus side = %v, want %v", err, ErrBadPlayerSide)
	}
	if _, err := New(Config{PlayerSide: xiangqi.Red, Level: "grandmaster"}, nil, discardLogger(), fixedRand()); !errors.Is(err, ErrBadLevel) {
		t.Errorf("New with an unknown level = %v, want %v", err, ErrBadLevel)
	}
	// An empty level means "unset" and takes the default rather than failing.
	g, err := New(Config{PlayerSide: xiangqi.Red}, nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatalf("New with no level: %v", err)
	}
	if got := g.State().Level; got != DefaultConfig().Level {
		t.Errorf("Level = %q, want the default %q", got, DefaultConfig().Level)
	}
}

func TestLegalTargetsFrom(t *testing.T) {
	g, err := New(DefaultConfig(), nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	// Red's b2 cannon can see the black cannon on b9 down an open file.
	targets, err := g.LegalTargets(sq(1, 2))
	if err != nil {
		t.Fatalf("LegalTargets: %v", err)
	}
	found := false
	for _, to := range targets {
		if to == sq(1, 9) {
			found = true
		}
	}
	if !found {
		t.Errorf("b2 targets = %v, want b9 among them", targets)
	}

	if _, err := g.LegalTargets(-1); !errors.Is(err, ErrBadSquare) {
		t.Errorf("LegalTargets(-1) = %v, want %v", err, ErrBadSquare)
	}
	if _, err := g.LegalTargets(90); !errors.Is(err, ErrBadSquare) {
		t.Errorf("LegalTargets(90) = %v, want %v", err, ErrBadSquare)
	}
}

func TestHintIsAdvisoryAndIgnoresDifficulty(t *testing.T) {
	fake := &fakeCloud{best: "b0c2", analysis: &cloud.Analysis{Known: true, Best: "b0c2"}}
	// The weakest level: the hint must not inherit its strength.
	g, err := New(Config{PlayerSide: xiangqi.Red, Level: LevelBeginner, UseBook: true}, fake, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}

	decision, err := g.Hint(context.Background())
	if err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if decision.Move != "b0c2" || decision.Source != SourceBook {
		t.Errorf("Hint = %+v, want the book move b0c2", decision)
	}
	// A hint suggests; it does not play.
	if got := g.State().History; len(got) != 0 {
		t.Errorf("History = %v after a hint, want empty", got)
	}

	// With the book off, the hint still gets a real search rather than the
	// weakest level's shallow one.
	fake2 := &fakeCloud{}
	g2, err := New(Config{PlayerSide: xiangqi.Red, Level: LevelBeginner, UseBook: false}, fake2, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}
	decision, err = g2.Hint(context.Background())
	if err != nil {
		t.Fatalf("Hint: %v", err)
	}
	if decision.Source != SourceSearch {
		t.Errorf("Source = %q, want %q", decision.Source, SourceSearch)
	}
	if decision.Depth < HintDepth {
		t.Errorf("hint depth = %d, want at least %d regardless of the difficulty", decision.Depth, HintDepth)
	}
}

func TestHintRefusedWhenItIsNotThePlayersTurn(t *testing.T) {
	g, err := New(Config{PlayerSide: xiangqi.Black, Level: LevelBeginner}, nil, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.Hint(context.Background()); !errors.Is(err, ErrNotYourTurn) {
		t.Errorf("Hint on the computer's turn = %v, want %v", err, ErrNotYourTurn)
	}
}

func TestAnalysisSurfacesCloudErrors(t *testing.T) {
	want := &cloud.Analysis{Known: true, Best: "b0c2"}
	g, err := New(DefaultConfig(), &fakeCloud{analysis: want}, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}
	got, err := g.Analysis(context.Background())
	if err != nil {
		t.Fatalf("Analysis: %v", err)
	}
	if got != want {
		t.Errorf("Analysis = %+v, want the client's", got)
	}

	broken := &fakeCloud{analysisErr: errors.New("boom")}
	g2, err := New(DefaultConfig(), broken, discardLogger(), fixedRand())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g2.Analysis(context.Background()); err == nil {
		t.Error("Analysis = nil error, want the client's error surfaced")
	}
}

// mustPlayHuman plays a move for the human, failing the test if it is refused.
func mustPlayHuman(t *testing.T, g *Game, move [2]xiangqi.Square) {
	t.Helper()
	if _, err := g.PlayHuman(move[0], move[1]); err != nil {
		t.Fatalf("PlayHuman(%v): %v", move, err)
	}
}

// mustAIMove plays the computer's turn, failing the test if it is refused.
func mustAIMove(t *testing.T, g *Game) {
	t.Helper()
	if _, err := g.AIMove(context.Background()); err != nil {
		t.Fatalf("AIMove: %v", err)
	}
}
