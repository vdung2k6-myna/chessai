package httpapi

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"chessai/internal/game"
)

// allSentinels is every failure the game layer can report.
//
// It is listed out in full rather than derived, because the point of the test
// below is to fail when internal/game gains a sentinel that nobody gave a code
// or a status to. Deriving the list from the same table under test would make
// the check circular and it would pass no matter what was added.
var allSentinels = []error{
	game.ErrBadMove,
	game.ErrBadSquare,
	game.ErrBadLevel,
	game.ErrBadPlayerSide,
	game.ErrNotYourTurn,
	game.ErrNotAITurn,
	game.ErrGameOver,
	game.ErrNoUndo,
	game.ErrAIBusy,
	game.ErrStale,
	game.ErrOffline,
	game.ErrNoMove,
}

// Every sentinel must be in apiErrors, so that it has both a status and a code.
// A sentinel missing from the table would reach a client as CodeInternal with a
// 500 — indistinguishable from a bug in this server, and unlocalizable.
func TestErrorTableCoversEverySentinel(t *testing.T) {
	covered := make(map[error]bool, len(apiErrors))
	for _, ae := range apiErrors {
		covered[ae.err] = true
	}

	for _, err := range allSentinels {
		if !covered[err] {
			t.Errorf("%v is not in apiErrors: it would be reported as %q with status %d",
				err, CodeInternal, http.StatusInternalServerError)
		}
	}
}

// Each sentinel must map to a distinct, non-empty code, since the code is what a
// client switches on to choose its text.
func TestCodesAreSpecificAndDistinct(t *testing.T) {
	seen := make(map[string]error, len(allSentinels))

	for _, err := range allSentinels {
		code := codeFor(err)
		switch {
		case code == "":
			t.Errorf("codeFor(%v) = %q, want a specific code", err, code)
			continue
		case code == CodeInternal:
			t.Errorf("codeFor(%v) = %q, want a code more specific than the fallback", err, code)
			continue
		}
		if other, dup := seen[code]; dup {
			t.Errorf("code %q is produced by both %v and %v", code, other, err)
			continue
		}
		seen[code] = err
	}

	if len(seen) != len(allSentinels) {
		t.Errorf("distinct codes = %d, want %d for %d sentinels",
			len(seen), len(allSentinels), len(allSentinels))
	}
}

// Every row in the table must produce a plausible status, so a row cannot be
// added with a code but no meaningful HTTP status. Which status each sentinel
// gets is asserted exactly in TestStatusForMapsEveryDomainError.
func TestErrorTableStatusesAreFailureStatuses(t *testing.T) {
	for _, ae := range apiErrors {
		if ae.code == "" {
			t.Errorf("row for %v has an empty code", ae.err)
		}
		if ae.status < 400 || ae.status > 599 {
			t.Errorf("row for %v has status %d, which is not a failure status", ae.err, ae.status)
		}
	}
}

// The game layer wraps sentinels with detail ("... is not legal in <fen>"), and
// the wrapper must keep the original's code: that detail is what makes the
// server log usable, so it cannot cost the client its ability to localize.
func TestWrappedErrorsKeepTheirCode(t *testing.T) {
	wrapped := fmt.Errorf("%w: c0c1 is not legal", game.ErrBadMove)
	if got, want := codeFor(wrapped), CodeBadMove; got != want {
		t.Errorf("codeFor(wrapped) = %q, want %q", got, want)
	}
	if got, want := codeFor(errors.Join(game.ErrOffline, errors.New("dial failed"))), CodeOffline; got != want {
		t.Errorf("codeFor(joined) = %q, want %q", got, want)
	}
}

// An error from a layer with no code of its own must degrade to the generic
// code rather than an empty string, which a client could not switch on.
func TestUnknownErrorsFallBackToTheInternalCode(t *testing.T) {
	if got := codeFor(errors.New("something in the plumbing broke")); got != CodeInternal {
		t.Errorf("codeFor(unknown) = %q, want %q", got, CodeInternal)
	}
}
