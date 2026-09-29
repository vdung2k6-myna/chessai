package xiangqi

import "testing"

// TestPerftStartPosition pins move generation to the published perft values for
// the Xiangqi start position. Its purpose is regression safety: it proves that
// wrapping the engine in this package (FEN encoding, position bookkeeping, the
// history stack) did not disturb the legal move generator underneath.
//
// Depth 5 is 133,312,995 nodes and is far too slow for routine runs, so it only
// runs when the short flag is absent.
func TestPerftStartPosition(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		depth uint
		want  int
	}{
		{1, 44},
		{2, 1920},
		{3, 79666},
		{4, 3290240},
	}

	for _, tc := range tests {
		if got := p.pos.Perft(tc.depth, false); got != tc.want {
			t.Errorf("perft(%d) = %d, want %d", tc.depth, got, tc.want)
		}
	}

	if !testing.Short() {
		if got := p.pos.Perft(5, false); got != 133312995 {
			t.Errorf("perft(5) = %d, want %d", got, 133312995)
		}
	}
}

// TestPerftMatchesLegalMoveCount cross-checks perft(1) against the same
// GenerateLEGAL call the game logic uses, so the two can never silently diverge.
func TestPerftMatchesLegalMoveCount(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := p.pos.Perft(1, false), len(p.LegalMoves()); got != want {
		t.Errorf("perft(1) = %d but LegalMoves() = %d", got, want)
	}
}

func TestPerftIsStableAcrossPlayout(t *testing.T) {
	// Play a short opening then undo it; the position and its perft must return
	// to exactly the starting values.
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	before := p.pos.Perft(3, false)
	fenBefore := p.FEN()

	for _, mv := range []string{"c3c4", "h7e7", "b2e2", "b9c7"} {
		if _, err := p.PlayUCI(mv); err != nil {
			t.Fatalf("PlayUCI(%q): %v", mv, err)
		}
	}
	for range 4 {
		if !p.Undo() {
			t.Fatal("Undo returned false")
		}
	}

	if got := p.FEN(); got != fenBefore {
		t.Errorf("FEN after undo = %q, want %q", got, fenBefore)
	}
	if got := p.pos.Perft(3, false); got != before {
		t.Errorf("perft(3) after undo = %d, want %d", got, before)
	}
}
