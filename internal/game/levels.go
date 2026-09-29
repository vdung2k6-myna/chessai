package game

import "fmt"

// Difficulty, defined in one place.
//
// A level is the game's unit of difficulty: what a player chooses, what the API
// carries, and what the interface names. Everything that makes a level strong
// lives in this file, so retuning the ladder is an edit here and nowhere else —
// not the markup, not the locale tables, not the client.
//
// Nothing outside this package ever sees Depth or Window. A client sends a name
// and reads a name back, which is what lets the numbers move without any client
// or translation moving with them.

// Level names, from weakest to strongest. These are part of the API (and of the
// interface's keys), so they are stable: retuning a level changes the numbers in
// this file, never its name.
const (
	LevelBeginner = "beginner"
	LevelEasy     = "easy"
	LevelMedium   = "medium"
	LevelHard     = "hard"
	LevelMaster   = "master"
)

// DefaultLevel is what a new game plays at when nothing else is asked for.
const DefaultLevel = LevelMedium

// level is the strength one level plays at.
type level struct {
	// Name is the level's identity. It is the only part a client sees.
	Name string

	// Depth is how many plies the level's moves are judged at.
	//
	// One depth and one search per level is the whole point of the column: a
	// level with a window ranks every legal move at this depth and plays one of
	// them, and the strongest level searches at it and plays what it finds. A
	// second depth would mean a second search whose answer the first one's
	// window throws away, which is a column that cannot be retuned — retune it
	// and nothing plays differently.
	Depth uint8

	// Window is how far below the best move's value a candidate may be and still
	// be eligible to play, in centipawns.
	//
	// This is the level's whole strength dial, and it is deliberately the only
	// one: the rate at which a level substitutes a weaker move is not a separate
	// setting, it falls out of how many moves lie inside the window. In a forced
	// position nothing else is inside it, so every level — like every player —
	// finds the only move.
	//
	// Zero means only the best move, which is what the strongest level plays and
	// what the spec requires of it.
	Window int
}

// levelsInOrder returns the levels from weakest to strongest, which is also the
// order a client lists them in.
//
// The depths are not arbitrary. Measured search times for the opening position
// on the development machine: depth 6 ~0.2s, 8 ~0.9s, 10 ~4s, 11 ~14s, 12 ~58s.
// A search is one blocking request with no progress reporting, so depth 12 would
// mean a minute of a frozen board. Depth 10 is the strongest that still feels
// like a move rather than a hang, and that ceiling is what bounds the ladder.
//
// The strongest level sits two plies below it, at 8. At 10 its pair with the
// level below measured 81.25%, above the band the calibration aims at — the top
// step was a cliff rather than a rung. Master's depth is the only dial that
// shrinks that pair without moving another one: every other level sits in two
// pairs, so retuning it helps the rung above and hurts the rung below. At 8 the
// pair measures 68.75%, inside the band, and the level costs about a quarter of
// what depth 10 did.
//
// A windowed level pays one search per legal move rather than one per turn —
// about forty of them in the opening — which bounds its depth well below the
// strongest level's. The two upper windowed rungs therefore rank at 5.
//
// The windows are the output of calibration, and design.md records what they
// measured and where the measurement is thin. Measured with every window at 0,
// two plies of extra depth separated two adjacent levels by nothing at all, so
// the ladder's spacing lives in the windows rather than in the depths and a
// window decreases at every rung. The top rung keeps a window of 0, which the
// spec requires: the strongest level plays the best move it finds.
func levelsInOrder() []level {
	return []level{
		{Name: LevelBeginner, Depth: 2, Window: 120},
		{Name: LevelEasy, Depth: 4, Window: 90},
		{Name: LevelMedium, Depth: 5, Window: 55},
		{Name: LevelHard, Depth: 5, Window: 30},
		{Name: LevelMaster, Depth: 8, Window: 0},
	}
}

// LevelNames returns the level names from weakest to strongest, which is the
// order a client lists them in and the order the ladder is measured in.
//
// The server's flag help and the self-play harness both read this rather than
// carrying their own copy of the list, which would drift from the table the
// first time a level was renamed.
func LevelNames() []string {
	levels := levelsInOrder()
	names := make([]string, 0, len(levels))
	for _, l := range levels {
		names = append(names, l.Name)
	}
	return names
}

// lookupLevel returns the level with the given name.
//
// The bool distinguishes "no such level" from a level that happens to be the
// zero value, because a caller has to refuse an unknown name rather than play it
// as a default: silently substituting Medium for a typo would hide the mistake
// behind a game that looks like it worked.
func lookupLevel(name string) (level, bool) {
	for _, l := range levelsInOrder() {
		if l.Name == name {
			return l, true
		}
	}
	return level{}, false
}

// normalizeLevel resolves a level name, taking an empty name as "no
// preference" and refusing anything else that is not a level.
//
// The name is matched exactly — no trimming, no case folding. A client sends a
// value it read from the picker, and a near miss is a bug worth surfacing rather
// than a spelling to be guessed at.
func normalizeLevel(name string) (level, error) {
	if name == "" {
		name = DefaultLevel
	}
	l, ok := lookupLevel(name)
	if !ok {
		return level{}, fmt.Errorf("%w: %q", ErrBadLevel, name)
	}
	return l, nil
}

// withDepth returns the level judging its moves at a different depth.
//
// A hint searches deeper than the level it advises, and plays the best move it
// finds rather than one of that level's near-equal moves — its caller clears the
// window — so the depth it is given is the depth of its one search.
func (l level) withDepth(d uint8) level {
	l.Depth = d
	return l
}
