package xiangqi

import (
	"fmt"
	"testing"

	"github.com/hmgle/godogpaw/engine"
)

// These tests pin down two upstream behaviours that a server cannot afford to
// get wrong. Both were originally investigated with a throwaway probe; they live
// here now so a dependency bump cannot quietly reintroduce a crash.

// TestDeepSearchOnSparseEndgamesDoesNotPanic searches positions that are hostile
// to the engine's in-search repetition detection.
//
// Negamax indexes PvLength[pos.GamePly] without the bound check Quiescence has,
// and positions built by Set carry no move history, so in principle a deep
// search on a sparse endgame could run GamePly past MAX_MOVES and panic. It was
// claimed to be reachable; searching these positions at depths 8 through 20
// showed it is not, because the engine's repetition detection terminates the
// shuffle lines first. This test holds that finding in place.
func TestDeepSearchOnSparseEndgamesDoesNotPanic(t *testing.T) {
	if testing.Short() {
		t.Skip("deep searches are slow")
	}

	fens := []string{
		"4k4/9/9/9/9/9/9/9/R8/5K3 w - - 0 1", // king and rook vs bare king
		"3k5/9/9/9/9/9/9/9/9/R4K3 w - - 0 1", // rook far from the black king
		"3k5/9/9/9/9/9/9/9/9/4K4 w - - 0 1",  // bare kings: every line is a draw
	}

	for _, fen := range fens {
		for _, depth := range []uint8{8, 12, 16, 20} {
			t.Run(fmt.Sprintf("depth-%d", depth), func(t *testing.T) {
				defer func() {
					if r := recover(); r != nil {
						t.Fatalf("panic searching %q at depth %d: %v", fen, depth, r)
					}
				}()

				p, err := New(fen)
				if err != nil {
					t.Fatalf("New(%q): %v", fen, err)
				}
				res, err := p.Search(engine.SearchLimits{Depth: depth})
				if err != nil {
					t.Fatalf("search at depth %d: %v", depth, err)
				}
				if !engine.IsOKMove(res.BestMove) {
					t.Errorf("depth %d returned no usable move", depth)
				}
			})
		}
	}
}

// TestRepetitionAdjudicationSurvivesPlay checks that a position with real move
// history is adjudicated by the engine's perpetual-check rules, and that
// searching it terminates.
//
// The move list is the upstream perpetual-check fixture: red shuttles its rook
// between e1 and d1 while black's king shuffles on d9/e9, so red is the side
// repeating and should be ruled against.
func TestRepetitionAdjudicationSurvivesPlay(t *testing.T) {
	p, err := New("4k4/9/9/9/9/9/9/9/R8/5K3 w - - 0 1")
	if err != nil {
		t.Fatal(err)
	}

	for _, mv := range []string{"a1e1", "e9d9", "e1d1", "d9e9", "d1e1", "e9d9", "e1d1", "d9e9"} {
		if _, err := p.PlayUCI(mv); err != nil {
			t.Fatalf("PlayUCI(%q): %v", mv, err)
		}
	}

	// DoMove must carry history forward, otherwise in-search repetition
	// detection is dead and the search would have nothing to adjudicate.
	if !p.pos.IsRepetition() {
		t.Fatal("replaying a threefold cycle did not register as a repetition")
	}

	// Red shuttles its rook onto the e-file where it checks the black king, so
	// red is the side repeating and the engine must rule against it — not call
	// it a draw.
	if status := p.Status(); status != RepetitionLoss {
		t.Errorf("Status() = %v, want %v (red is the perpetual checker)", status, RepetitionLoss)
	}

	// With repetition detection live this must terminate rather than shuffle
	// forever. Depth is modest so the test stays quick.
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic searching a repetition position: %v", r)
		}
	}()
	res, err := p.Search(engine.SearchLimits{Depth: 14})
	if err != nil {
		t.Fatalf("searching a repetition position: %v", err)
	}
	if res.Depth == 0 {
		t.Error("search returned no depth, so it did not complete")
	}
}
