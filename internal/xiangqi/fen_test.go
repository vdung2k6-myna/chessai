package xiangqi

import (
	"strings"
	"testing"
)

func TestEncodeFENRoundTrip(t *testing.T) {
	// The start position, a midgame position with black to move, and an endgame
	// with the counters set to non-default values.
	positions := []string{
		StartFEN,
		"rnbakabnr/9/1c5c1/p1p1p1p1p/9/2P6/P3P1P1P/1C5C1/9/RNBAKABNR b - - 0 1",
		"3k5/9/9/9/9/9/9/9/4A4/4K4 w - - 0 1",
		"rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNR b - - 7 30",
	}

	for _, want := range positions {
		p, err := New(want)
		if err != nil {
			t.Fatalf("New(%q): %v", want, err)
		}
		if got := p.FEN(); got != want {
			t.Errorf("round trip mismatch\n got: %q\nwant: %q", got, want)
		}
	}
}

func TestEncodeFENBoardShape(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	board := strings.Split(strings.Fields(p.FEN())[0], "/")
	if len(board) != 10 {
		t.Fatalf("expected 10 ranks, got %d", len(board))
	}
	for i, rank := range board {
		files := 0
		for _, c := range rank {
			if c >= '1' && c <= '9' {
				files += int(c - '0')
			} else {
				files++
			}
		}
		if files != 9 {
			t.Errorf("rank %d describes %d files, want 9: %q", i, files, rank)
		}
	}
}

func TestEncodeFENTracksSideToMove(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(p.FEN())[1]; got != "w" {
		t.Errorf("start position side = %q, want %q", got, "w")
	}
	if _, err := p.PlayUCI("c3c4"); err != nil {
		t.Fatal(err)
	}
	if got := strings.Fields(p.FEN())[1]; got != "b" {
		t.Errorf("after a red move, side = %q, want %q", got, "b")
	}
}

func TestValidateFEN(t *testing.T) {
	tests := []struct {
		name    string
		fen     string
		wantErr bool
	}{
		{"start position", StartFEN, false},
		{"no counters", "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNR w", false},
		{"empty", "", true},
		{"board only", "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNR", true},
		{"bad side", "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNR x - - 0 1", true},
		{"nine ranks", "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9 w - - 0 1", true},
		{"short rank", "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABN w - - 0 1", true},
		{"bad piece letter", "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNZ w - - 0 1", true},

		// Semantic checks. The engine's own Set would take the process down with
		// log.Fatalf on each of these, so rejecting them here is the whole point.
		{"kings facing on an open file", "4k4/9/9/9/9/9/9/9/9/4K4 w - - 0 1", true},
		{"kings facing but blocked", "4k4/9/9/9/9/9/9/9/4R4/4K4 w - - 0 1", false},
		{"two red kings", "4k4/9/9/9/9/9/9/9/4K4/4K4 w - - 0 1", true},
		{"no black king", "9/9/9/9/9/9/9/9/9/4K4 w - - 0 1", true},
		{"six red pawns", "RNBAKABNR/9/9/P1P1P1P1P/P8/9/p1p1p1p1p/1c5c1/9/rnbakabnr w - - 0 1", true},
		{"seventeen red pieces", "RNBAKABNR/9/1C5C1/P1P1P1P1P/R8/9/p1p1p1p1p/1c5c1/9/rnbakabnr w - - 0 1", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFEN(tc.fen)
			if tc.wantErr && err == nil {
				t.Errorf("ValidateFEN(%q) = nil, want error", tc.fen)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("ValidateFEN(%q) = %v, want nil", tc.fen, err)
			}
		})
	}
}

func TestNewRejectsMalformedFENWithoutFatal(t *testing.T) {
	// PositionNG.Set calls log.Fatalf on a FEN with fewer than two fields, and
	// again when its PosIsOk consistency check fails. log.Fatalf calls os.Exit,
	// which recover cannot catch, so each of these would kill this test process
	// were New to pass it through. The last case is the one that actually did.
	for _, bad := range []string{
		"   ",
		"rnbakabnr",
		"not a fen at all",
		"9/9/9/9/9/9/9/9/9/9 q",
		"4k4/9/9/9/9/9/9/9/9/R3K4 w - - 0 1", // kings facing: well formed, illegal
	} {
		if _, err := New(bad); err == nil {
			t.Errorf("New(%q) = nil error, want rejection", bad)
		}
	}
}

func TestNewDefaultsToStartPosition(t *testing.T) {
	p, err := New("")
	if err != nil {
		t.Fatalf("New(\"\"): %v", err)
	}
	if got := p.FEN(); got != StartFEN {
		t.Errorf("New(\"\") = %q, want the start position %q", got, StartFEN)
	}
}
