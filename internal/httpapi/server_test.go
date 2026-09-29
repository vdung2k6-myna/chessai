package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"chessai/internal/cloud"
	"chessai/internal/game"
	"chessai/internal/xiangqi"
)

type fakeCloud struct {
	best        string
	analysis    *cloud.Analysis
	analysisErr error
}

func (f *fakeCloud) BestMove(ctx context.Context, fen string) (string, error) { return f.best, nil }
func (f *fakeCloud) Analysis(ctx context.Context, fen string) (*cloud.Analysis, error) {
	return f.analysis, f.analysisErr
}

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// newTestServer returns a handler backed by a real game. Depth 2 keeps the
// searches in these tests fast.
func newTestServer(t *testing.T, cfg game.Config, c cloud.Client) http.Handler {
	t.Helper()
	g, err := game.New(cfg, c, discardLogger())
	if err != nil {
		t.Fatalf("game.New: %v", err)
	}
	mux := http.NewServeMux()
	New(g, discardLogger()).Register(mux)
	return Middleware(mux, discardLogger())
}

// do issues a request and returns the recorder.
func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req := httptest.NewRequest(method, target, reader)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeState decodes a State response, failing the test if it is not one.
func decodeState(t *testing.T, rec *httptest.ResponseRecorder) game.State {
	t.Helper()
	var st game.State
	if err := json.Unmarshal(rec.Body.Bytes(), &st); err != nil {
		t.Fatalf("decoding state from %q: %v", rec.Body.String(), err)
	}
	return st
}

// errorResponse returns the decoded "error" object of an error response,
// reusing the production types so the test fails if the envelope's shape
// changes rather than silently decoding nothing.
func errorResponse(t *testing.T, rec *httptest.ResponseRecorder) failure {
	t.Helper()
	var body failureBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding error from %q: %v", rec.Body.String(), err)
	}
	return body.Error
}

// sq converts file and rank to a square.
func sq(file, rank int) int { return file + rank*9 }

// The failure envelope is part of the API contract: a client reads the code and
// renders its own text from it, and must be able to ignore the English message.
// This asserts the shape at the HTTP boundary rather than through a helper.
func TestFailureResponseEnvelope(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)

	// Red moves, handing the turn to the computer. A second move by red is a
	// conflict, which is the cheapest real failure to provoke.
	do(t, h, http.MethodPost, "/api/move", moveBody(redPawn[0], redPawn[1]))
	rec := do(t, h, http.MethodPost, "/api/move", moveBody(redPawn[0], redPawn[1]))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", rec.Code)
	}

	// Named field by field, so that a response which merely put the code at the
	// top level instead of nesting it under "error" would fail to decode.
	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decoding %q: %v", rec.Body.String(), err)
	}

	if body.Error.Code != CodeNotYourTurn {
		t.Errorf("error.code = %q, want %q", body.Error.Code, CodeNotYourTurn)
	}
	if body.Error.Message == "" {
		t.Error("error.message is empty, but it is what makes a raw response readable")
	}
	// The message is prose for humans, not a second copy of the code: a client
	// that ignored the code and showed the message would otherwise still be
	// showing something unlocalizable.
	if body.Error.Message == body.Error.Code {
		t.Errorf("error.message = %q duplicates the code", body.Error.Message)
	}
}

// redPawn and blackPawn are legal opening moves, used to drive the game on.
var (
	redPawn   = [2]int{sq(2, 3), sq(2, 4)} // c3c4
	blackPawn = [2]int{sq(2, 6), sq(2, 5)} // c6c5
)

func moveBody(from, to int) string {
	return `{"from":` + itoa(from) + `,"to":` + itoa(to) + `}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	if neg {
		return "-" + string(digits)
	}
	return string(digits)
}

func TestStateEndpoint(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)

	rec := do(t, h, http.MethodGet, "/api/state", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("Content-Type = %q, want JSON", ct)
	}

	st := decodeState(t, rec)
	if len(st.Board) != 90 {
		t.Errorf("board has %d squares, want 90", len(st.Board))
	}
	if st.SideToMove != xiangqi.Red {
		t.Errorf("SideToMove = %v, want red", st.SideToMove)
	}
	if !st.YourTurn {
		t.Error("YourTurn = false at the start of a game as red")
	}
	if st.Over || st.CanUndo {
		t.Errorf("Over = %v, CanUndo = %v; want false and false", st.Over, st.CanUndo)
	}
	if st.FEN != xiangqi.StartFEN {
		t.Errorf("FEN = %q, want the opening position", st.FEN)
	}
}

func TestLegalEndpoint(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)

	// Red's b2 cannon can reach the black cannon on b9.
	rec := do(t, h, http.MethodGet, "/api/legal?sq="+itoa(sq(1, 2)), "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var targets []int
	if err := json.Unmarshal(rec.Body.Bytes(), &targets); err != nil {
		t.Fatalf("decoding targets: %v", err)
	}
	found := false
	for _, to := range targets {
		if to == sq(1, 9) {
			found = true
		}
	}
	if !found {
		t.Errorf("targets = %v, want b9 among them", targets)
	}

	// An empty square has no targets, and the response is an empty array rather
	// than null, so the client can iterate it without a guard.
	rec = do(t, h, http.MethodGet, "/api/legal?sq="+itoa(sq(4, 5)), "")
	if got := strings.TrimSpace(rec.Body.String()); got != "[]" {
		t.Errorf("empty square returned %q, want []", got)
	}

	rec = do(t, h, http.MethodGet, "/api/legal?sq=nonsense", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad sq status = %d, want 400", rec.Code)
	}
	rec = do(t, h, http.MethodGet, "/api/legal", "")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("missing sq status = %d, want 400", rec.Code)
	}
}

func TestMoveEndpoint(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)

	rec := do(t, h, http.MethodPost, "/api/move", moveBody(redPawn[0], redPawn[1]))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	st := decodeState(t, rec)
	if len(st.History) != 1 || st.History[0] != "c3c4" {
		t.Errorf("History = %v, want [c3c4]", st.History)
	}
	if st.SideToMove != xiangqi.Black {
		t.Errorf("SideToMove = %v, want black", st.SideToMove)
	}
	if st.YourTurn {
		t.Error("YourTurn = true after handing the turn to the computer")
	}
	if st.LastMove != "c3c4" {
		t.Errorf("LastMove = %q, want c3c4", st.LastMove)
	}

	// Moving twice in a row is a conflict: the client's view is out of date.
	rec = do(t, h, http.MethodPost, "/api/move", moveBody(redPawn[0], redPawn[1]))
	if rec.Code != http.StatusConflict {
		t.Errorf("second move status = %d, want 409", rec.Code)
	}
	// The client is told what went wrong by the code, which is the part it
	// renders from. The message is English and is only for reading raw
	// responses, so nothing here depends on its wording.
	if e := errorResponse(t, rec); e.Code != CodeNotYourTurn {
		t.Errorf("error code = %q, want %q", e.Code, CodeNotYourTurn)
	}
}

func TestMoveEndpointRejectsBadInput(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)

	// Red's own knight sits on b0.
	rec := do(t, h, http.MethodPost, "/api/move", moveBody(sq(0, 0), sq(1, 0)))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("illegal move status = %d, want 400", rec.Code)
	}

	rec = do(t, h, http.MethodPost, "/api/move", moveBody(-1, 5))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("negative square status = %d, want 400", rec.Code)
	}

	rec = do(t, h, http.MethodPost, "/api/move", "{not json")
	if rec.Code != http.StatusBadRequest {
		t.Errorf("malformed body status = %d, want 400", rec.Code)
	}

	// A body larger than the limit is refused rather than read into memory.
	huge := `{"from":0,"to":0,"pad":"` + strings.Repeat("x", 16<<10) + `"}`
	rec = do(t, h, http.MethodPost, "/api/move", huge)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("oversized body status = %d, want 400", rec.Code)
	}
}

func TestAIMoveEndpoint(t *testing.T) {
	// The human plays black, so it is the computer's turn from the start.
	h := newTestServer(t, game.Config{PlayerSide: xiangqi.Black, Level: game.LevelBeginner}, nil)

	// The computer opening the game is normal; asking it to move on the human's
	// turn is not.
	rec := do(t, h, http.MethodPost, "/api/ai-move", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	st := decodeState(t, rec)
	if len(st.History) != 1 {
		t.Fatalf("History = %v, want one move", st.History)
	}
	if st.AI == nil {
		t.Fatal("State.AI = nil, want the computer's decision")
	}
	if st.AI.Source != game.SourceSearch {
		t.Errorf("Source = %q, want %q offline", st.AI.Source, game.SourceSearch)
	}
	if st.SideToMove != xiangqi.Black || !st.YourTurn {
		t.Errorf("after the computer moved: side %v, yourTurn %v; want black and true", st.SideToMove, st.YourTurn)
	}

	rec = do(t, h, http.MethodPost, "/api/ai-move", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("ai-move on the human's turn status = %d, want 409", rec.Code)
	}
}

func TestUndoEndpoint(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)

	rec := do(t, h, http.MethodPost, "/api/undo", "")
	if rec.Code != http.StatusConflict {
		t.Errorf("undo with nothing to undo status = %d, want 409", rec.Code)
	}

	do(t, h, http.MethodPost, "/api/move", moveBody(redPawn[0], redPawn[1]))
	rec = do(t, h, http.MethodPost, "/api/undo", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("undo status = %d, want 200: %s", rec.Code, rec.Body)
	}
	st := decodeState(t, rec)
	if len(st.History) != 0 || st.SideToMove != xiangqi.Red {
		t.Errorf("after undo: history %v, side %v; want empty and red", st.History, st.SideToMove)
	}
}

func TestNewEndpoint(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)
	do(t, h, http.MethodPost, "/api/move", moveBody(redPawn[0], redPawn[1]))

	rec := do(t, h, http.MethodPost, "/api/new", `{"playerSide":1,"level":"easy","useBook":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	st := decodeState(t, rec)
	if st.FEN != xiangqi.StartFEN || len(st.History) != 0 {
		t.Errorf("the board was not reset: fen %q, history %v", st.FEN, st.History)
	}
	if st.PlayerSide != xiangqi.Black || st.Level != game.LevelEasy || st.UseBook {
		t.Errorf("config not applied: side %v, level %q, book %v", st.PlayerSide, st.Level, st.UseBook)
	}

	// An omitted field keeps its default rather than taking Go's zero value: a
	// partial body must not quietly turn the book off.
	rec = do(t, h, http.MethodPost, "/api/new", `{}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("empty body status = %d, want 200: %s", rec.Code, rec.Body)
	}
	st = decodeState(t, rec)
	want := game.DefaultConfig()
	if st.PlayerSide != want.PlayerSide || st.Level != want.Level || st.UseBook != want.UseBook {
		t.Errorf("empty body gave side %v, level %q, book %v; want the defaults %v, %q, %v",
			st.PlayerSide, st.Level, st.UseBook, want.PlayerSide, want.Level, want.UseBook)
	}

	rec = do(t, h, http.MethodPost, "/api/new", `{"playerSide":9}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad player side status = %d, want 400", rec.Code)
	}

	// A refused level must not start a game either. A move is played first so
	// that a reset would be visible as history going back to empty, rather than
	// indistinguishable from the state a refusal correctly leaves behind.
	do(t, h, http.MethodPost, "/api/move", moveBody(redPawn[0], redPawn[1]))

	rec = do(t, h, http.MethodPost, "/api/new", `{"playerSide":0,"level":"grandmaster"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown level status = %d, want 400", rec.Code)
	}
	if e := errorResponse(t, rec); e.Code != CodeBadLevel {
		t.Errorf("unknown level code = %q, want %q", e.Code, CodeBadLevel)
	}
	if st := decodeState(t, do(t, h, http.MethodGet, "/api/state", "")); len(st.History) != 1 {
		t.Errorf("History = %v after a refused level, want the game left as it was (1 move)", st.History)
	}
}

func TestConfigEndpoint(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)

	rec := do(t, h, http.MethodPost, "/api/config", `{"level":"hard"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	if st := decodeState(t, rec); st.Level != game.LevelHard {
		t.Errorf("Level = %q, want %q", st.Level, game.LevelHard)
	}

	// A partial patch leaves the other fields alone.
	rec = do(t, h, http.MethodPost, "/api/config", `{"useBook":false}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	st := decodeState(t, rec)
	if st.Level != game.LevelHard || st.UseBook {
		t.Errorf("Level = %q, UseBook = %v; want %q and false", st.Level, st.UseBook, game.LevelHard)
	}

	rec = do(t, h, http.MethodPost, "/api/config", `{"level":"nope"}`)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown level status = %d, want 400", rec.Code)
	}
	if e := errorResponse(t, rec); e.Code != CodeBadLevel {
		t.Errorf("unknown level code = %q, want %q", e.Code, CodeBadLevel)
	}
	// The refused level must not have been applied.
	if st := decodeState(t, do(t, h, http.MethodGet, "/api/state", "")); st.Level != game.LevelHard {
		t.Errorf("Level = %q after a refused patch, want %q", st.Level, game.LevelHard)
	}
}

func TestHintEndpoint(t *testing.T) {
	h := newTestServer(t, game.Config{PlayerSide: xiangqi.Red, Level: game.LevelBeginner, UseBook: true}, &fakeCloud{best: "b0c2"})

	rec := do(t, h, http.MethodPost, "/api/hint", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var decision game.AIDecision
	if err := json.Unmarshal(rec.Body.Bytes(), &decision); err != nil {
		t.Fatalf("decoding decision: %v", err)
	}
	if decision.Move != "b0c2" || decision.Source != game.SourceBook {
		t.Errorf("hint = %+v, want the book move b0c2", decision)
	}
	// A hint suggests; it must not have been played.
	if st := decodeState(t, do(t, h, http.MethodGet, "/api/state", "")); len(st.History) != 0 {
		t.Errorf("History = %v after a hint, want empty", st.History)
	}
}

// TestStateReportsTheLevelAndNothingBehindIt checks the API's half of the
// level's contract: the state names the level, and none of the numbers that make
// the level strong travel with it.
//
// The point is not secrecy — the values are a few integers and the source is
// open — but that a client cannot come to depend on them. Retuning the ladder
// has to be a server-side edit, and it stops being one the moment a client reads
// a depth out of a state response.
func TestStateReportsTheLevelAndNothingBehindIt(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)

	rec := do(t, h, http.MethodGet, "/api/state", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}

	// Decoded as raw keys rather than into game.State, which would answer only
	// for the fields that type happens to have and say nothing about what else
	// the response carries.
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &fields); err != nil {
		t.Fatalf("decoding state keys: %v", err)
	}

	var level string
	raw, ok := fields["level"]
	if !ok {
		t.Fatal("the state has no level field")
	}
	if err := json.Unmarshal(raw, &level); err != nil {
		t.Fatalf("decoding level: %v", err)
	}
	if level != game.DefaultConfig().Level {
		t.Errorf("level = %q, want %q", level, game.DefaultConfig().Level)
	}

	// Depth is a real field of the computer's decision — how deep the search
	// that produced its last move ran — but it is nested inside "ai", not a
	// property of the game. At the top level, none of the level's parameters
	// may appear, spelled the way this package spells them or the way the level
	// table does.
	for _, key := range []string{"depth", "scoreCap", "window", "score_cap"} {
		if _, present := fields[key]; present {
			t.Errorf("the state exposes %q at the top level; a level's parameters must stay server-side", key)
		}
	}
}

func TestAnalysisEndpoint(t *testing.T) {
	want := &cloud.Analysis{Known: true, Best: "b0c2", PVDepth: 55}
	h := newTestServer(t, game.DefaultConfig(), &fakeCloud{analysis: want})

	rec := do(t, h, http.MethodGet, "/api/analysis", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body)
	}
	var got cloud.Analysis
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decoding analysis: %v", err)
	}
	if got.Best != "b0c2" || got.PVDepth != 55 {
		t.Errorf("analysis = %+v, want best b0c2 at depth 55", got)
	}

	// Offline is reported as unavailable, not as a server error.
	offline := newTestServer(t, game.DefaultConfig(), nil)
	rec = do(t, offline, http.MethodGet, "/api/analysis", "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("offline status = %d, want 503", rec.Code)
	}

	broken := newTestServer(t, game.DefaultConfig(), &fakeCloud{analysisErr: errors.New("boom")})
	rec = do(t, broken, http.MethodGet, "/api/analysis", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("broken cloudbook status = %d, want 500", rec.Code)
	}
}

func TestMethodNotAllowed(t *testing.T) {
	h := newTestServer(t, game.DefaultConfig(), nil)

	if rec := do(t, h, http.MethodGet, "/api/move", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/move status = %d, want 405", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/state", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/state status = %d, want 405", rec.Code)
	}

	// The live server also serves static files under the catch-all pattern,
	// which matches every GET. The API must be dispatched ahead of it so a
	// wrong method is still a 405 rather than a 404 from the file server.
	notFound := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	h = Middleware(New(mustGame(t), discardLogger()).Handler(notFound), discardLogger())

	if rec := do(t, h, http.MethodGet, "/api/move", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/move with a static catch-all status = %d, want 405", rec.Code)
	}
	if rec := do(t, h, http.MethodPost, "/api/state", ""); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST /api/state with a static catch-all status = %d, want 405", rec.Code)
	}
	// An unknown API path belongs to neither side: it must not reach the file
	// server, which would answer as if it were a missing file.
	if rec := do(t, h, http.MethodGet, "/api/nope", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/nope status = %d, want 404", rec.Code)
	}
	// Anything outside /api/ still goes to the static handler.
	if rec := do(t, h, http.MethodGet, "/", ""); rec.Code != http.StatusNotFound {
		t.Errorf("GET / status = %d, want the static handler's 404", rec.Code)
	}
}

// mustGame returns a started game, failing the test if it cannot.
func mustGame(t *testing.T) *game.Game {
	t.Helper()
	g, err := game.New(game.DefaultConfig(), nil, discardLogger())
	if err != nil {
		t.Fatalf("game.New: %v", err)
	}
	return g
}

func TestStatusForMapsEveryDomainError(t *testing.T) {
	// Keyed by the canonical list in codes_test.go, so a sentinel added to the
	// game layer fails here until its status is chosen deliberately.
	want := map[error]int{
		game.ErrBadMove:       http.StatusBadRequest,
		game.ErrBadSquare:     http.StatusBadRequest,
		game.ErrBadLevel:      http.StatusBadRequest,
		game.ErrBadPlayerSide: http.StatusBadRequest,

		game.ErrNotYourTurn: http.StatusConflict,
		game.ErrNotAITurn:   http.StatusConflict,
		game.ErrGameOver:    http.StatusConflict,
		game.ErrNoUndo:      http.StatusConflict,
		game.ErrAIBusy:      http.StatusConflict,
		game.ErrStale:       http.StatusConflict,

		game.ErrOffline: http.StatusServiceUnavailable,

		// Impossible rather than the client's fault: the position would have
		// ended the game before a turn could be played in it.
		game.ErrNoMove: http.StatusInternalServerError,
	}

	for _, err := range allSentinels {
		expected, ok := want[err]
		if !ok {
			t.Errorf("%v has no expected status in this test", err)
			continue
		}
		if got := statusFor(err); got != expected {
			t.Errorf("statusFor(%v) = %d, want %d", err, got, expected)
		}
	}

	if got := statusFor(errors.New("something else")); got != http.StatusInternalServerError {
		t.Errorf("statusFor(unexpected) = %d, want 500", got)
	}
	// Wrapped errors must still map, since handlers wrap with %w.
	if got := statusFor(errors.Join(game.ErrBadMove, errors.New("detail"))); got != http.StatusBadRequest {
		t.Errorf("statusFor(wrapped ErrBadMove) = %d, want 400", got)
	}
}

func TestMiddlewareTurnsAPanicIntoA500(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /panic", func(http.ResponseWriter, *http.Request) {
		panic("deliberate")
	})
	h := Middleware(mux, discardLogger())

	rec := do(t, h, http.MethodGet, "/panic", "")
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
