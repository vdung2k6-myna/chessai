package game

import "testing"

// TestLevelTable pins the shape every level must hold to, so that a retune
// cannot quietly introduce one the engine will not run, or a ladder that is not
// a ladder.
func TestLevelTable(t *testing.T) {
	levels := levelsInOrder()
	if len(levels) == 0 {
		t.Fatal("no levels are defined")
	}

	seen := make(map[string]bool, len(levels))
	for _, l := range levels {
		if l.Name == "" {
			t.Error("a level has no name")
		}
		if seen[l.Name] {
			t.Errorf("the name %q is used by two levels", l.Name)
		}
		seen[l.Name] = true

		if l.Depth < MinDepth || l.Depth > MaxDepth {
			t.Errorf("%s searches %d plies, outside the %d..%d the engine is bounded to",
				l.Name, l.Depth, MinDepth, MaxDepth)
		}
		if l.Window < 0 {
			t.Errorf("%s has a window of %d; a window is a distance below the best move",
				l.Name, l.Window)
		}
	}

	// Ordered from weakest to strongest is what the client lists and what the
	// self-play harness measures between neighbours. Depth alone does not order
	// the ladder: the spacing lives in the windows, and two adjacent rungs may
	// judge their moves at the same depth. What Depth may not do is fall as the
	// ladder rises — a level that judges its moves less deeply than the level
	// below it is the ladder coming apart.
	for i := 1; i < len(levels); i++ {
		if levels[i].Depth < levels[i-1].Depth {
			t.Errorf("%s judges its moves at depth %d, shallower than %s at %d",
				levels[i].Name, levels[i].Depth, levels[i-1].Name, levels[i-1].Depth)
		}
		// The window is what a rung's strength is made of, so this is the
		// ladder's real ordering, and it is strict: a rung that may err as far
		// as the one below it may is a rung that may play as weakly.
		if levels[i].Window >= levels[i-1].Window {
			t.Errorf("%s may err by up to %d centipawns, not less than %s at %d",
				levels[i].Name, levels[i].Window, levels[i-1].Name, levels[i-1].Window)
		}
	}

	// The strongest level plays the best move it can find. The spec makes that a
	// contract rather than a tuning outcome, so it is asserted here and not left
	// to whatever calibration last committed.
	if got := levels[len(levels)-1].Window; got != 0 {
		t.Errorf("the strongest level has window %d, want 0", got)
	}
}

// TestLadderOrdersTheLowLevels is the ordering check CI runs.
//
// The spec establishes the ladder's order by playing the levels against each
// other, and that is testdata/selfplay: hours of it, and the numbers design.md
// records. CI gets the cheap half instead — the properties a table edit can
// break, checked on the low rungs only, in microseconds. It is deliberately not
// a game: a game between two low levels would need a window's worth of searches
// per move once calibration turns windows on, which is what would put the
// suite's seconds at risk.
//
// What it catches that TestLevelTable does not is the ladder's order and its
// noise: a table whose levels are listed out of order, or where a weaker level
// has the narrower window, fails here.
func TestLadderOrdersTheLowLevels(t *testing.T) {
	// The low rungs are the ladder's declared start, in this order. Pinning the
	// names is what makes swapping two of them a failure rather than a
	// rearrangement the test follows along with.
	lowest := []string{LevelBeginner, LevelEasy, LevelMedium}

	levels := levelsInOrder()
	if len(levels) < len(lowest) {
		t.Fatalf("the ladder has %d levels, fewer than the %d this check reads", len(levels), len(lowest))
	}
	low := levels[:len(lowest)]
	for i, want := range lowest {
		if low[i].Name != want {
			t.Errorf("rung %d of the ladder is %q, want %q", i+1, low[i].Name, want)
		}
	}

	for i := 1; i < len(low); i++ {
		weaker, stronger := low[i-1], low[i]
		if stronger.Depth < weaker.Depth {
			t.Errorf("%s judges its moves at depth %d, shallower than %s at %d",
				stronger.Name, stronger.Depth, weaker.Name, weaker.Depth)
		}
		// This is the check that makes the ladder a ladder. Noise is what makes a
		// level weak, so a rung must err less than the rung below it, and a
		// stronger level holding the wider window is the ladder standing on its
		// head.
		if stronger.Window >= weaker.Window {
			t.Errorf("%s may err by up to %d centipawns, not less than %s at %d",
				stronger.Name, stronger.Window, weaker.Name, weaker.Window)
		}
	}
}

// TestLookupLevel checks that every name in the table resolves to its own level,
// and that nothing else resolves at all.
func TestLookupLevel(t *testing.T) {
	for _, want := range levelsInOrder() {
		got, ok := lookupLevel(want.Name)
		if !ok {
			t.Errorf("lookupLevel(%q) found no level", want.Name)
			continue
		}
		if got != want {
			t.Errorf("lookupLevel(%q) = %+v, want %+v", want.Name, got, want)
		}
	}

	// Empty is refused by lookupLevel and defaulted by normalizeLevel, so it
	// belongs in both tests with opposite expectations.
	for _, name := range []string{"", "Beginner", "MASTER", " master", "harder", "2", "unknown"} {
		if got, ok := lookupLevel(name); ok {
			t.Errorf("lookupLevel(%q) resolved to %+v, want no level", name, got)
		}
	}
}

// TestNormalizeLevel checks the name a request carries is turned into a level,
// or refused.
func TestNormalizeLevel(t *testing.T) {
	// An empty name is not a mistake a client can make — it is a request that
	// named no level, which takes the default.
	want, ok := lookupLevel(DefaultLevel)
	if !ok {
		t.Fatalf("the default level %q is not in the table", DefaultLevel)
	}
	got, err := normalizeLevel("")
	if err != nil {
		t.Fatalf("normalizeLevel(\"\") failed: %v", err)
	}
	if got != want {
		t.Errorf("normalizeLevel(\"\") = %+v, want the default %+v", got, want)
	}

	for _, want := range levelsInOrder() {
		got, err := normalizeLevel(want.Name)
		if err != nil {
			t.Errorf("normalizeLevel(%q) failed: %v", want.Name, err)
			continue
		}
		if got != want {
			t.Errorf("normalizeLevel(%q) = %+v, want %+v", want.Name, got, want)
		}
	}

	// A name that is not a level is refused rather than defaulted: playing
	// Medium for a typo would hide the mistake behind a game that looks fine.
	for _, name := range []string{"Beginner", "harder", "2", " master", "unknown"} {
		if _, err := normalizeLevel(name); err == nil {
			t.Errorf("normalizeLevel(%q) was accepted", name)
		}
	}
}
