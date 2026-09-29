package game

import (
	"math/rand"
	"testing"

	"github.com/hmgle/godogpaw/engine"

	"chessai/internal/xiangqi"
)

// materialFEN is a position with one free capture in it: the black rook on a5
// and the red rook on a0 share the a-file with nothing between them, so a0a5
// wins a whole rook and every other red move wins nothing.
//
// The kings are on e9 and f0 rather than facing each other on the e-file, which
// would be a flying-general position the engine refuses.
const materialFEN = "4k4/9/9/9/r8/9/9/9/9/R4K3 w - - 0 1"

// testGame builds a game around a position, with a known source of randomness,
// for the parts of the policy that need a Game and not just a Position.
func testGame(t *testing.T, fen string, seed int64) (*Game, *xiangqi.Position) {
	t.Helper()
	pos := mustPosition(t, fen)
	g, err := newFrom(pos, Config{PlayerSide: xiangqi.Red, Level: LevelMedium}, nil, discardLogger(),
		WithRand(rand.New(rand.NewSource(seed))))
	if err != nil {
		t.Fatalf("newFrom: %v", err)
	}
	return g, pos
}

// TestCandidatesCoverEveryLegalMove checks the list is a complete ranking of the
// position's options, with a real score on each.
func TestCandidatesCoverEveryLegalMove(t *testing.T) {
	g, pos := testGame(t, materialFEN, 1)

	legal := pos.LegalMoves()
	cands := g.candidatesFor(pos, 2)

	if len(cands) != len(legal) {
		t.Fatalf("candidatesFor returned %d candidates, want one per legal move (%d)", len(cands), len(legal))
	}

	seen := make(map[xiangqi.Move]bool, len(cands))
	for _, c := range cands {
		if !engine.IsOKMove(c.Move) {
			t.Errorf("candidate %v is not a playable move", c.Move)
		}
		if seen[c.Move] {
			t.Errorf("the move %s appears twice", engine.Move2Str(c.Move))
		}
		seen[c.Move] = true
		if c.Score == scoreUnsearchable {
			t.Errorf("%s was not scored; its search did not complete", engine.Move2Str(c.Move))
		}
	}
	for _, m := range legal {
		if !seen[m] {
			t.Errorf("the legal move %s has no candidate", engine.Move2Str(m))
		}
	}
}

// TestCandidateScoresAreFromTheChooser pins the sign convention.
//
// This is the assertion that catches the one mistake that would sink the whole
// mechanism: the engine scores from the point of view of the side to move, so a
// score read off the position *after* a move is the opponent's. Left unnegated,
// every level would rank the move that hands over the most material as its
// favourite.
func TestCandidateScoresAreFromTheChooser(t *testing.T) {
	g, pos := testGame(t, materialFEN, 1)

	cands := g.candidatesFor(pos, 2)

	byMove := make(map[string]int, len(cands))
	bestMove, bestScore := "", 0
	for i, c := range cands {
		uci := engine.Move2Str(c.Move)
		byMove[uci] = c.Score
		if i == 0 || c.Score > bestScore {
			bestMove, bestScore = uci, c.Score
		}
	}

	got, ok := byMove["a0a5"]
	if !ok {
		t.Fatal("the winning capture a0a5 is not among the candidates")
	}
	if bestMove != "a0a5" {
		t.Errorf("the best candidate is %s (%d), want the rook capture a0a5 (%d)",
			bestMove, bestScore, got)
	}
	// A rook is worth far more than the tempo any other move gains, so the
	// capture should lead by hundreds of centipawns, not by a rounding error.
	if got < 500 {
		t.Errorf("the rook capture scores %d, want a clearly winning value; the scores "+
			"are probably from the opponent's point of view", got)
	}
}

// TestChooseWithinWindowBoundary checks exactly where the window's edge falls.
//
// chooseWithin is exercised directly rather than through a position, because the
// point is the arithmetic and a real position cannot be built with scores of
// 100, 95 and 90 in it.
func TestChooseWithinWindowBoundary(t *testing.T) {
	// Moves are only labels here; chooseWithin never touches the engine.
	cands := []candidate{
		{Move: 1, Score: 100},
		{Move: 2, Score: 95},
		{Move: 3, Score: 90},
		{Move: 4, Score: -50},
	}

	cases := []struct {
		window int
		want   []xiangqi.Move
	}{
		{0, []xiangqi.Move{1}},    // only moves tied with the best
		{4, []xiangqi.Move{1}},    // 95 is 5 below, and 5 > 4
		{5, []xiangqi.Move{1, 2}}, // the edge is inclusive
		{10, []xiangqi.Move{1, 2, 3}},
		{200, []xiangqi.Move{1, 2, 3, 4}},
	}

	for _, tc := range cases {
		g, _ := testGame(t, materialFEN, 1)

		want := make(map[xiangqi.Move]bool, len(tc.want))
		for _, m := range tc.want {
			want[m] = true
		}
		seen := make(map[xiangqi.Move]bool)
		// Enough draws that a uniform choice over the eligible set would have to
		// show every member, and would have to show an ineligible one if the
		// window were wider than it should be.
		for range 200 {
			got, ok := g.chooseWithin(cands, tc.window)
			if !ok {
				t.Fatalf("chooseWithin(window %d) found no move", tc.window)
			}
			seen[got.Move] = true
		}

		for m := range seen {
			if !want[m] {
				t.Errorf("window %d played move %d, which is more than %d below the best",
					tc.window, m, tc.window)
			}
		}
		for _, m := range tc.want {
			if !seen[m] {
				t.Errorf("window %d never played move %d, which is inside it", tc.window, m)
			}
		}
	}
}

// TestChooseWithinForcedByANarrowWindow checks the spec's property on a real
// position: a move outside the window is never played, however the dice fall.
//
// materialFEN's a0a5 wins a rook and leads everything else by hundreds, so any
// window narrower than that gap leaves a single eligible move.
func TestChooseWithinForcedByANarrowWindow(t *testing.T) {
	// The candidate scores do not depend on the random source, so they are
	// computed once and reused across seeds.
	scorer, pos := testGame(t, materialFEN, 1)
	cands := scorer.candidatesFor(pos, 2)

	for seed := range int64(20) {
		g, _ := testGame(t, materialFEN, seed)
		for range 20 {
			got, ok := g.chooseWithin(cands, 10)
			if !ok {
				t.Fatal("chooseWithin found no move")
			}
			if uci := engine.Move2Str(got.Move); uci != "a0a5" {
				t.Fatalf("seed %d played %s with a 10cp window, want the forced capture a0a5",
					seed, uci)
			}
		}
	}
}

// TestChooseWithinWideWindowVaries checks the other half of the mechanism: a
// window wide enough to hold several moves produces several moves.
func TestChooseWithinWideWindowVaries(t *testing.T) {
	g, pos := testGame(t, materialFEN, 1)
	cands := g.candidatesFor(pos, 2)

	seen := make(map[string]bool)
	for range 200 {
		got, ok := g.chooseWithin(cands, 100000)
		if !ok {
			t.Fatal("chooseWithin found no move")
		}
		seen[engine.Move2Str(got.Move)] = true
	}
	if len(seen) < 2 {
		t.Errorf("a window holding every move produced %d distinct move(s); selection is not varying", len(seen))
	}
}

// TestSelectionIsReproducibleWithTheSameSeed checks that selection is a
// reproducible function of the seed, which is what lets a self-play game be
// replayed and lets a test assert on a specific move.
func TestSelectionIsReproducibleWithTheSameSeed(t *testing.T) {
	_, pos := testGame(t, materialFEN, 1)

	draw := func(seed int64, n int) []string {
		g, _ := testGame(t, materialFEN, seed)
		cands := g.candidatesFor(pos, 2)
		out := make([]string, 0, n)
		for range n {
			got, ok := g.chooseWithin(cands, 100000)
			if !ok {
				t.Fatal("chooseWithin found no move")
			}
			out = append(out, engine.Move2Str(got.Move))
		}
		return out
	}

	a, b := draw(7, 30), draw(7, 30)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("two games on seed 7 chose differently at draw %d: %s and %s", i, a[i], b[i])
		}
	}

	// Different seeds are not required to differ on any single draw, but over a
	// wide window they should differ somewhere; if they never did, the seed
	// would not be reaching the choice at all.
	c := draw(11, 30)
	same := true
	for i := range a {
		if a[i] != c[i] {
			same = false
			break
		}
	}
	if same {
		t.Errorf("seeds 7 and 11 made identical choices over 30 draws; the seed is not affecting selection")
	}
}

// TestStrongestLevelSkipsTheCandidatePass checks the strongest level plays the
// move its search returned, and never ranks alternatives against it.
//
// The evidence is the shape of the line. A decision that came through the
// candidate pass carries the one move that is known to follow, because the line
// the search produced belongs to a move that is no longer being played; a
// decision the pass never touched carries the line the search itself found,
// which runs on past the move. So a master decision whose line is longer than
// the move it plays is one the pass never touched.
//
// The reported depth used to be the evidence, and it cannot be any more: a level
// has one depth now, so a decision the pass touched and one it did not both
// report the same number, and the two are told apart by what they carry rather
// than by what they claim to have run.
func TestStrongestLevelSkipsTheCandidatePass(t *testing.T) {
	lvl, ok := lookupLevel(LevelMaster)
	if !ok {
		t.Fatalf("%s is not a level", LevelMaster)
	}
	if lvl.Window != 0 {
		t.Fatalf("%s has window %d; this test is about the level with no window", LevelMaster, lvl.Window)
	}

	for seed := range int64(8) {
		g, pos := testGame(t, materialFEN, seed)

		decision, err := g.searchDecision(pos, lvl)
		if err != nil {
			t.Fatalf("searchDecision: %v", err)
		}
		if decision.Move != "a0a5" {
			t.Errorf("seed %d played %s, want the rook capture a0a5", seed, decision.Move)
		}
		if decision.Depth != lvl.Depth {
			t.Errorf("seed %d reported depth %d, want the level's own %d",
				seed, decision.Depth, lvl.Depth)
		}
		if len(decision.PV) < 2 {
			t.Errorf("seed %d reported the line %v, which is the shape a candidate pass "+
				"leaves behind — one move; the strongest level should have no pass",
				seed, decision.PV)
		}
		if decision.PV[0] != decision.Move {
			t.Errorf("seed %d reported the line %v, which does not start with its move %s",
				seed, decision.PV, decision.Move)
		}
	}
}

// TestWindowedLevelReportsWhatThePassCost checks that a windowed decision carries
// what the turn spent.
//
// It has nowhere else to come from: a windowed level runs no search of its own,
// so the ranking is the whole of its work and the decision's nodes and time are
// the candidate searches added up. A decision that reports nothing would show a
// turn that cost nothing, which is what the interface would then display.
func TestWindowedLevelReportsWhatThePassCost(t *testing.T) {
	lvl := level{Name: "test-cost", Depth: 2, Window: 100000}

	g, pos := testGame(t, materialFEN, 1)
	if len(pos.LegalMoves()) < 2 {
		t.Fatal("this test needs a position with more than one move to rank")
	}

	decision, err := g.searchDecision(pos, lvl)
	if err != nil {
		t.Fatalf("searchDecision: %v", err)
	}
	if decision.Nodes == 0 {
		t.Error("Nodes = 0, want what the candidate searches spent; the pass ranked " +
			"every legal move and its cost is the turn's cost")
	}
	if decision.Fallback {
		t.Error("Fallback = true, but every candidate in this position searches")
	}
}

// TestWindowedLevelPicksFromTheCandidatePass is the other half of the same
// signature: a level that does have a window leaves the pass's own mark on the
// decision, and can play a move other than the one its ranking liked best.
//
// Without this, the assertion above would pass just as well if the candidate
// pass were wired to nothing, since a decision the pass never touched is exactly
// what a broken pass would produce.
func TestWindowedLevelPicksFromTheCandidatePass(t *testing.T) {
	// A level with a real depth and a window wide enough to hold the whole move
	// list, so what is played is limited by the ranking and not by the window.
	lvl := level{Name: "test-wide", Depth: 2, Window: 100000}

	seen := make(map[string]bool)
	for seed := range int64(20) {
		g, pos := testGame(t, materialFEN, seed)

		decision, err := g.searchDecision(pos, lvl)
		if err != nil {
			t.Fatalf("searchDecision: %v", err)
		}
		if decision.Depth != lvl.Depth {
			t.Errorf("seed %d reported depth %d, want the level's own %d",
				seed, decision.Depth, lvl.Depth)
		}
		// The one move known to follow is the line the pass leaves, and it is
		// what tells this decision apart from one a search produced.
		if len(decision.PV) != 1 || decision.PV[0] != decision.Move {
			t.Errorf("seed %d reported the line %v, want exactly the move it plays %s",
				seed, decision.PV, decision.Move)
		}
		seen[decision.Move] = true
	}
	if len(seen) < 2 {
		t.Errorf("a wide window over 20 seeds produced %d distinct move(s); the candidate "+
			"pass is not reaching the decision", len(seen))
	}
}
