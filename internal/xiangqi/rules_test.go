package xiangqi

import (
	"testing"

	"github.com/hmgle/godogpaw/engine"
)

func TestPlayAndUndoRestorePosition(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	startFEN := p.FEN()

	m, err := p.PlayUCI("c3c4")
	if err != nil {
		t.Fatalf("PlayUCI(c3c4): %v", err)
	}
	if got := engine.Move2Str(m); got != "c3c4" {
		t.Errorf("parsed move = %q, want %q", got, "c3c4")
	}
	if p.HistoryLen() != 1 {
		t.Errorf("history length = %d, want 1", p.HistoryLen())
	}
	if p.SideToMove() != Black {
		t.Error("side to move should be black after a red move")
	}
	if p.FEN() == startFEN {
		t.Error("FEN should change after a move")
	}

	if !p.Undo() {
		t.Fatal("Undo returned false")
	}
	if got := p.FEN(); got != startFEN {
		t.Errorf("FEN after undo = %q, want %q", got, startFEN)
	}
	if p.SideToMove() != Red {
		t.Error("side to move should be red again after undo")
	}
	if p.Undo() {
		t.Error("Undo on an empty history should return false")
	}
}

func TestPlayRejectsIllegalAndMalformedMoves(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}

	// a0b0 and a0a3 land on the rook's own knight and pawn. b0b2 is not a
	// knight's move, and e0d0 is occupied by the king's own advisor.
	for _, illegal := range []string{"a0b0", "a0a3", "b0b2", "e0d0"} {
		if _, err := p.PlayUCI(illegal); err == nil {
			t.Errorf("expected %q to be rejected", illegal)
		}
	}
	// Malformed coordinates.
	for _, bad := range []string{"", "zz", "a0", "c3c4c5", "j0j1"} {
		if _, err := p.PlayUCI(bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
	// A rejected move must not disturb the position.
	if p.HistoryLen() != 0 {
		t.Errorf("history length = %d after rejected moves, want 0", p.HistoryLen())
	}
	if got := p.FEN(); got != StartFEN {
		t.Errorf("FEN changed after rejected moves: %q", got)
	}
}

func TestLegalTargetsFrom(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}

	// The red cannon on b2 can slide along the file and along the rank, plus
	// jump the black cannon on b7 to capture it.
	sq, err := engine.SquareFromString("b2")
	if err != nil {
		t.Fatal(err)
	}
	targets := p.LegalTargetsFrom(sq)
	if len(targets) == 0 {
		t.Fatal("expected legal targets for the b2 cannon")
	}
	has := func(coord string) bool {
		want, err := engine.SquareFromString(coord)
		if err != nil {
			t.Fatal(err)
		}
		for _, got := range targets {
			if got == want {
				return true
			}
		}
		return false
	}
	if !has("b9") {
		t.Errorf("cannon on b2 should be able to capture the black cannon on b9, got %v", targets)
	}

	// An empty square has no targets.
	if got := p.LegalTargetsFrom(engine.SQ_E4); len(got) != 0 {
		t.Errorf("empty square returned %d targets, want 0", len(got))
	}
}

func TestStatusCheckmateAndStalemateBothLose(t *testing.T) {
	tests := []struct {
		name       string
		fen        string
		want       Status
		winnerIs   Side
		wantReason Reason
	}{
		{
			// Black king on e9 is checked down the e-file by the rook on e0 and
			// along rank 9 by the rook on a9, with every escape covered.
			name:       "checkmate",
			fen:        "R3k4/9/9/9/9/9/9/9/9/3KR4 b - - 0 1",
			want:       Checkmate,
			winnerIs:   Red,
			wantReason: ReasonCheckmate,
		},
		{
			// Black king on e9 is not attacked, but d9, f9 and e8 all are, so
			// black has no legal move: 困毙, which loses in Xiangqi.
			name:       "stalemate is a loss",
			fen:        "4k4/R8/9/9/9/9/9/9/9/2KR1R3 b - - 0 1",
			want:       Stalemate,
			winnerIs:   Red,
			wantReason: ReasonStalemate,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p, err := New(tc.fen)
			if err != nil {
				t.Fatal(err)
			}
			got := p.Status()
			if got != tc.want {
				t.Fatalf("Status() = %v, want %v (legal moves: %d, in check: %v)",
					got, tc.want, len(p.LegalMoves()), p.InCheck())
			}
			res := p.Result()
			if !res.Over {
				t.Error("Result().Over = false, want true")
			}
			if res.Draw {
				t.Error("Result().Draw = true, want false")
			}
			if res.Winner != tc.winnerIs {
				t.Errorf("Result().Winner = %v, want %v", res.Winner, tc.winnerIs)
			}
			// The reason is a wire-visible code the UI renders its own text
			// from, so it is asserted here rather than left to the status prose.
			if res.Reason != tc.wantReason {
				t.Errorf("Result().Reason = %q, want %q", res.Reason, tc.wantReason)
			}
		})
	}
}

func TestStatusOngoingAtStart(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	if got := p.Status(); got != Ongoing {
		t.Errorf("Status() = %v, want Ongoing", got)
	}
	if res := p.Result(); res.Over {
		t.Errorf("Result() = %+v, want not over", res)
	}
}
