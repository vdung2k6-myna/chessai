// Package httpapi exposes a game over HTTP as a small JSON API.
//
// Every endpoint that changes the game returns the same State object, so the
// client has one shape to render and never has to guess what a move did.
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"chessai/internal/game"
	"chessai/internal/xiangqi"
)

// maxBodyBytes bounds a request body. Every request here is a handful of
// numbers, so anything larger is a mistake or an attack.
const maxBodyBytes = 8 << 10

// Server exposes a game over HTTP.
type Server struct {
	game *game.Game
	log  *slog.Logger
}

// New returns a Server for a game.
func New(g *game.Game, log *slog.Logger) *Server {
	if log == nil {
		log = slog.Default()
	}
	return &Server{game: g, log: log}
}

// Handler returns the complete request handler: the API under /api/, and
// static files everywhere else.
//
// The two are dispatched by hand rather than both being put on one ServeMux,
// because a static handler registered as the catch-all pattern matches every
// GET. A GET to a POST-only API path would then be served as a missing file —
// a 404 from the file server instead of the 405 the API means. Keeping /api/
// off the static handler also means a typo in an API path cannot reach the
// file server at all.
func (s *Server) Handler(static http.Handler) http.Handler {
	mux := http.NewServeMux()
	s.Register(mux)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			mux.ServeHTTP(w, r)
			return
		}
		static.ServeHTTP(w, r)
	})
}

// Register adds the API routes to mux. Static files are the caller's business;
// most callers want Handler instead.
func (s *Server) Register(mux *http.ServeMux) {
	for _, rt := range s.routes() {
		mux.HandleFunc(rt.method+" "+rt.path, rt.handler)
	}
}

// route is one entry of the API surface.
type route struct {
	method  string
	path    string
	handler http.HandlerFunc
}

// routes lists the API surface. Keeping it in one place means the method guard
// in Register cannot drift out of step with the handlers.
func (s *Server) routes() []route {
	return []route{
		{http.MethodGet, "/api/state", s.handleState},
		{http.MethodGet, "/api/legal", s.handleLegal},
		{http.MethodGet, "/api/analysis", s.handleAnalysis},
		{http.MethodPost, "/api/move", s.handleMove},
		{http.MethodPost, "/api/ai-move", s.handleAIMove},
		{http.MethodPost, "/api/new", s.handleNew},
		{http.MethodPost, "/api/undo", s.handleUndo},
		{http.MethodPost, "/api/hint", s.handleHint},
		{http.MethodPost, "/api/config", s.handleConfig},
	}
}

// Middleware wraps a handler so a panic becomes a 500 rather than a dropped
// connection. net/http recovers panics itself, but silently, and a game server
// that panics should say so.
func Middleware(next http.Handler, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Error("panic handling request", "method", r.Method, "path", r.URL.Path, "panic", rec)
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.game.State())
}

func (s *Server) handleLegal(w http.ResponseWriter, r *http.Request) {
	sq, err := strconv.Atoi(r.URL.Query().Get("sq"))
	if err != nil {
		writeError(w, http.StatusBadRequest, CodeBadSquare, "sq must be an integer")
		return
	}
	targets, err := s.game.LegalTargets(sq)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if targets == nil {
		targets = []int{}
	}
	writeJSON(w, http.StatusOK, targets)
}

func (s *Server) handleAnalysis(w http.ResponseWriter, r *http.Request) {
	analysis, err := s.game.Analysis(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, analysis)
}

type moveRequest struct {
	From int `json:"from"`
	To   int `json:"to"`
}

func (s *Server) handleMove(w http.ResponseWriter, r *http.Request) {
	var req moveRequest
	if !s.decode(w, r, &req) {
		return
	}
	state, err := s.game.PlayHuman(req.From, req.To)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleAIMove(w http.ResponseWriter, r *http.Request) {
	// The request context is the right lifetime for a search: if the client goes
	// away, there is nobody left to receive the move.
	decision, err := s.game.AIMove(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	state := s.game.State()
	s.log.Debug("computer moved", "move", decision.Move, "source", decision.Source)
	writeJSON(w, http.StatusOK, state)
}

// newGameRequest is the body of POST /api/new. Every field is a pointer so an
// omitted one keeps its default rather than taking Go's zero value — otherwise
// a partial body would silently turn the opening book off and reset the side.
type newGameRequest struct {
	PlayerSide *xiangqi.Side `json:"playerSide"`
	Level      *string       `json:"level"`
	UseBook    *bool         `json:"useBook"`
}

func (s *Server) handleNew(w http.ResponseWriter, r *http.Request) {
	var req newGameRequest
	if !s.decode(w, r, &req) {
		return
	}

	cfg := game.DefaultConfig()
	if req.PlayerSide != nil {
		cfg.PlayerSide = *req.PlayerSide
	}
	if req.Level != nil {
		cfg.Level = *req.Level
	}
	if req.UseBook != nil {
		cfg.UseBook = *req.UseBook
	}

	state, err := s.game.NewGame(cfg)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleUndo(w http.ResponseWriter, r *http.Request) {
	state, err := s.game.Undo()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) handleHint(w http.ResponseWriter, r *http.Request) {
	decision, err := s.game.Hint(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, decision)
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	var patch game.ConfigPatch
	if !s.decode(w, r, &patch) {
		return
	}
	state, err := s.game.SetConfig(patch)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

// decode reads a JSON body, rejecting oversized and malformed input.
func (s *Server) decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	body := http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(body)
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			// An empty body is fine for endpoints whose fields are all optional.
			return true
		}
		writeError(w, http.StatusBadRequest, CodeBadBody, "invalid JSON body: "+err.Error())
		return false
	}
	return true
}

// fail maps a domain error onto a status code. The distinction matters to the
// client: a conflict means "your view is out of date, refresh", while a 500
// means something is genuinely broken.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	status := statusFor(err)
	if status >= http.StatusInternalServerError {
		s.log.Error("request failed", "method", r.Method, "path", r.URL.Path, "err", err)
	}
	writeError(w, status, codeFor(err), err.Error())
}

// Failure codes. A client switches on these and renders its own text, so they
// are the API's published vocabulary: defined once here, never spelled out at a
// call site, and never reused for a different cause.
const (
	CodeBadMove       = "bad_move"
	CodeBadSquare     = "bad_square"
	CodeBadLevel      = "bad_level"
	CodeBadPlayerSide = "bad_player_side"
	CodeBadBody       = "bad_body"

	CodeNotYourTurn = "not_your_turn"
	CodeNotAITurn   = "not_ai_turn"
	CodeGameOver    = "game_over"
	CodeNoUndo      = "no_undo"
	CodeAIBusy      = "ai_busy"
	CodeStale       = "stale"

	CodeOffline = "offline"
	CodeNoMove  = "no_move"

	// CodeInternal is the code for a failure with no more specific identity:
	// either a bug in this server, or an error from a layer that has no code of
	// its own. A client should show a generic message for it rather than
	// treating it as a known outcome.
	CodeInternal = "internal"
)

// apiErrors is the single source of truth for how each domain failure is
// reported: its HTTP status, and the code a client renders its own text from.
//
// Both are derived from this one table on purpose. A sentinel that had a status
// but no code would reach a client as an empty code, and the two would drift as
// soon as someone added a row to one place and not the other.
var apiErrors = []struct {
	err    error
	status int
	code   string
}{
	// The request itself was malformed.
	{game.ErrBadMove, http.StatusBadRequest, CodeBadMove},
	{game.ErrBadSquare, http.StatusBadRequest, CodeBadSquare},
	{game.ErrBadLevel, http.StatusBadRequest, CodeBadLevel},
	{game.ErrBadPlayerSide, http.StatusBadRequest, CodeBadPlayerSide},

	// The request was well formed but conflicts with the state of the game.
	{game.ErrNotYourTurn, http.StatusConflict, CodeNotYourTurn},
	{game.ErrNotAITurn, http.StatusConflict, CodeNotAITurn},
	{game.ErrGameOver, http.StatusConflict, CodeGameOver},
	{game.ErrNoUndo, http.StatusConflict, CodeNoUndo},
	{game.ErrAIBusy, http.StatusConflict, CodeAIBusy},
	{game.ErrStale, http.StatusConflict, CodeStale},

	// A dependency is unavailable, so the request could succeed later.
	{game.ErrOffline, http.StatusServiceUnavailable, CodeOffline},

	// The engine found no move and the position has none either, which the game
	// layer treats as impossible: the turn would have ended first. It is listed
	// with a 500 deliberately rather than being left to the default, so that the
	// code it carries is as stable as every other one.
	{game.ErrNoMove, http.StatusInternalServerError, CodeNoMove},
}

// statusFor maps a domain error onto an HTTP status. The distinction matters to
// the client: a conflict means "your view is out of date, refresh", while a 500
// means something is genuinely broken.
func statusFor(err error) int {
	for _, ae := range apiErrors {
		if errors.Is(err, ae.err) {
			return ae.status
		}
	}
	return http.StatusInternalServerError
}

// codeFor returns the language-neutral code for a domain error, or CodeInternal
// when the error has no specific identity. It walks the same table statusFor
// does, so the two can never disagree about what a given failure is.
//
// The codes are a compatibility contract, not a convenience. Clients render
// their own text from them, so a released client keeps working only if:
//
//   - an existing code keeps its meaning, and is never repointed at a different
//     cause;
//   - a code is never reused for a new cause — a new failure kind gets a new
//     code, and a client that does not recognize one shows its generic message
//     rather than mistaking it for something it already handles;
//   - a failure that a client can act on differently from any other gets its own
//     code, rather than being folded into a broader one.
//
// The value a client displays never comes from here: the English wording lives
// in the failure's Message, and a client must not show that.
func codeFor(err error) string {
	for _, ae := range apiErrors {
		if errors.Is(err, ae.err) {
			return ae.code
		}
	}
	return CodeInternal
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

// failureBody is the JSON body of a failed request.
type failureBody struct {
	Error failure `json:"error"`
}

// failure is a failed request's payload.
//
// Code is the field a client must render its text from: it is stable and
// language-neutral. Message is English, for the server log and for people
// reading raw responses with curl — a client must not display it, since that
// would put English in front of a player reading another language.
type failure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, failureBody{failure{Code: code, Message: message}})
}
