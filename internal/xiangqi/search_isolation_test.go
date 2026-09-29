package xiangqi

import (
	"testing"

	"github.com/hmgle/godogpaw/engine"
)

// TestSearchDoesNotDestroyPositionState pins down what an in-place search does
// to the position it searches.
//
// SearchPositionWithLimits calls clearSearch first, and clearSearch sets
// pos.GamePly = 0. Because GamePly drives both the FEN's move counter and the
// engine's repetition walk-back, searching the live position silently discards
// its history: the FEN loses its counters and a threefold repetition can no
// longer be adjudicated. That is why Search must work on a copy.
func TestSearchDoesNotDestroyPositionState(t *testing.T) {
	p, err := New("4k4/9/9/9/9/9/9/9/R8/5K3 w - - 0 1")
	if err != nil {
		t.Fatal(err)
	}
	for _, mv := range []string{"a1e1", "e9d9", "e1d1", "d9e9", "d1e1", "e9d9", "e1d1", "d9e9"} {
		if _, err := p.PlayUCI(mv); err != nil {
			t.Fatal(err)
		}
	}

	beforeFEN, beforeHistory := p.FEN(), p.HistoryLen()
	if !p.pos.IsRepetition() {
		t.Fatal("fixture is not a repetition; the test would prove nothing")
	}
	if got := p.Status(); got != RepetitionLoss {
		t.Fatalf("fixture Status() = %v, want %v", got, RepetitionLoss)
	}

	if _, err := p.Search(engine.SearchLimits{Depth: 6}); err != nil {
		t.Fatalf("Search: %v", err)
	}

	if got := p.HistoryLen(); got != beforeHistory {
		t.Errorf("HistoryLen() = %d after search, want %d", got, beforeHistory)
	}
	if got := p.FEN(); got != beforeFEN {
		t.Errorf("FEN changed across a search\n got: %q\nwant: %q", got, beforeFEN)
	}
	if !p.pos.IsRepetition() {
		t.Error("the position no longer registers as a repetition after a search")
	}
	if got := p.Status(); got != RepetitionLoss {
		t.Errorf("Status() = %v after search, want %v", got, RepetitionLoss)
	}
}

// TestCloneIsIndependent guards the reason Clone replays history instead of
// copying the engine struct: PositionNG's state stack is a slice of pointers, so
// a shallow copy would leave the clone sharing a backing array with the
// original, and pushing states in one would corrupt the other.
func TestCloneIsIndependent(t *testing.T) {
	orig, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	for _, mv := range []string{"c3c4", "c6c5", "b2e2"} {
		if _, err := orig.PlayUCI(mv); err != nil {
			t.Fatal(err)
		}
	}

	clone, err := orig.Clone()
	if err != nil {
		t.Fatalf("Clone: %v", err)
	}
	if clone.FEN() != orig.FEN() {
		t.Fatalf("clone FEN = %q, want %q", clone.FEN(), orig.FEN())
	}

	// Advance the clone well past the original's history depth, so any shared
	// backing array would be overwritten several times over.
	for _, mv := range []string{"h7e7", "b0c2", "h9g7", "a0b0", "b9c7"} {
		if _, err := clone.PlayUCI(mv); err != nil {
			t.Fatalf("clone PlayUCI(%q): %v", mv, err)
		}
	}

	// c3c4 c6c5 b2e2: red's c-pawn is on c4, black's c-pawn on c5, and red's b2
	// cannon has moved to e2 — all three moves visible, black to move, and the
	// counters advanced by three plies.
	const origFEN = "rnbakabnr/9/1c5c1/p3p1p1p/2p6/2P6/P3P1P1P/4C2C1/9/RNBAKABNR b - - 3 2"
	if got := orig.FEN(); got != origFEN {
		t.Errorf("original changed when the clone played on\n got: %q\nwant: %q", got, origFEN)
	}
	if got := orig.HistoryLen(); got != 3 {
		t.Errorf("original HistoryLen() = %d, want 3", got)
	}
	if got := orig.Status(); got != Ongoing {
		t.Errorf("original Status() = %v, want %v", got, Ongoing)
	}

	// And the reverse: unwinding the original must not disturb the clone.
	cloneFEN, cloneLen := clone.FEN(), clone.HistoryLen()
	for orig.Undo() {
	}
	if got := clone.FEN(); got != cloneFEN {
		t.Errorf("clone changed when the original was unwound\n got: %q\nwant: %q", got, cloneFEN)
	}
	if got := clone.HistoryLen(); got != cloneLen {
		t.Errorf("clone HistoryLen() = %d, want %d", got, cloneLen)
	}
}
