// Package cloud talks to the chessdb.cn cloudbook: a cloud opening book and
// analysis database for Xiangqi.
//
// The API is a single endpoint taking an action and a FEN, e.g.
//
//	https://www.chessdb.cn/chessdb.php?action=querybest&board=<fen>
//
// and replying with a short plain-text body. Its move notation is the same UCCI
// coordinate notation godogpaw uses, so no translation layer is needed.
package cloud

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the public chessdb.cn endpoint. It is HTTPS-capable, which
// matters when the UI is served over HTTPS and cannot call plain HTTP.
const DefaultBaseURL = "https://www.chessdb.cn/chessdb.php"

// ErrInvalidBoard means chessdb rejected the FEN we sent. That points at a bug
// in our own FEN encoding rather than a transient network problem, so it is
// reported distinctly instead of being treated as "not in book".
var ErrInvalidBoard = errors.New("chessdb: invalid board")

// Move is one entry from the queryall action.
//
// Score and Winrate are both from the point of view of the side to move, as
// chessdb reports them. Verified against the live service: with black to move
// and black two rooks up, queryall gives score 1873 / winrate 99.66, and the
// mirror position with red to move gives 3645 / 100.00. So a positive score
// means the player whose turn it is stands better, whoever that is.
type Move struct {
	Move    string  `json:"move"`
	Score   int     `json:"score"`
	Rank    int     `json:"rank"`
	Note    string  `json:"note"`
	Winrate float64 `json:"winrate"`
}

// Analysis is everything the UI needs about a position.
//
// Score and the Moves' scores carry the same side-to-move convention as Move,
// which is why the FEN is always included: it names the side those numbers
// belong to.
type Analysis struct {
	FEN     string   `json:"fen"`
	Known   bool     `json:"known"`
	Best    string   `json:"best,omitempty"`
	Score   *int     `json:"score,omitempty"`
	Moves   []Move   `json:"moves,omitempty"`
	PV      []string `json:"pv,omitempty"`
	PVDepth int      `json:"pvDepth,omitempty"`
	PVScore *int     `json:"pvScore,omitempty"`
	Partial bool     `json:"partial,omitempty"`
}

// Client is the surface the game depends on. It is an interface so tests can
// stand in a fake cloudbook without touching the network.
type Client interface {
	// BestMove returns the cloudbook's move for a position, or "" when the
	// position is not in the book. An empty move is not an error; it is the
	// normal signal to fall back to a local search.
	BestMove(ctx context.Context, fen string) (string, error)

	// Analysis gathers what the UI shows in the sidebar. Individual queries may
	// fail without failing the whole call, in which case Partial is set.
	Analysis(ctx context.Context, fen string) (*Analysis, error)
}

// ChessDB is a Client backed by the chessdb.cn HTTP API.
type ChessDB struct {
	baseURL string
	http    *http.Client
	cache   *cache
	log     *slog.Logger
}

// Option configures a ChessDB client.
type Option func(*ChessDB)

// WithBaseURL points the client at a different endpoint, for tests.
func WithBaseURL(u string) Option {
	return func(c *ChessDB) { c.baseURL = u }
}

// WithHTTPClient supplies the HTTP client, for timeouts and tests.
func WithHTTPClient(h *http.Client) Option {
	return func(c *ChessDB) { c.http = h }
}

// WithCache sets the cache entry limit and TTL. A limit of zero disables caching.
func WithCache(limit int, ttl time.Duration) Option {
	return func(c *ChessDB) { c.cache = newCache(limit, ttl) }
}

// WithLogger sets the logger used for non-fatal degradation.
func WithLogger(l *slog.Logger) Option {
	return func(c *ChessDB) { c.log = l }
}

// New returns a client for the public chessdb.cn endpoint.
func New(opts ...Option) *ChessDB {
	c := &ChessDB{
		baseURL: DefaultBaseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
		cache:   newCache(512, 10*time.Minute),
		log:     slog.Default(),
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// BestMove implements Client.
func (c *ChessDB) BestMove(ctx context.Context, fen string) (string, error) {
	body, err := c.get(ctx, "querybest", url.Values{"board": {fen}})
	if err != nil {
		return "", err
	}
	return parseBestMove(body)
}

// Analysis implements Client. It issues its queries concurrently so the sidebar
// costs about one round trip rather than four, and tolerates any one of them
// failing so the sidebar still shows something useful.
func (c *ChessDB) Analysis(ctx context.Context, fen string) (*Analysis, error) {
	q := url.Values{"board": {fen}}

	var (
		wg                                   sync.WaitGroup
		allBody, bestBody, scoreBody, pvBody string
		allErr, bestErr, scoreErr, pvErr     error
	)
	wg.Add(4)
	go func() { defer wg.Done(); allBody, allErr = c.get(ctx, "queryall", q) }()
	go func() { defer wg.Done(); bestBody, bestErr = c.get(ctx, "querybest", q) }()
	go func() { defer wg.Done(); scoreBody, scoreErr = c.get(ctx, "queryscore", q) }()
	go func() { defer wg.Done(); pvBody, pvErr = c.get(ctx, "querypv", q) }()
	wg.Wait()

	a := &Analysis{FEN: fen}

	// A rejected board means our FEN is malformed. That is a bug, not a
	// transient failure, so surface it rather than degrading silently.
	for _, err := range []error{allErr, bestErr, scoreErr, pvErr} {
		if errors.Is(err, ErrInvalidBoard) {
			return nil, err
		}
	}

	if allErr == nil {
		a.Moves = parseMoveList(allBody)
	}
	if scoreErr == nil {
		if v, ok := parseScore(scoreBody); ok {
			a.Score = &v
		}
	}
	if pvErr == nil {
		if pv, ok := parsePV(pvBody); ok {
			a.PV = pv.Moves
			a.PVDepth = pv.Depth
			if pv.HasScore {
				score := pv.Score
				a.PVScore = &score
			}
		}
	}
	if bestErr == nil {
		mv, err := parseBestMove(bestBody)
		if err == nil {
			a.Best = mv
		}
	}
	// querybest is authoritative, but an egtb-only or partial database reply can
	// leave it empty while queryall still knows the position. Take the top-ranked
	// move in that case.
	if a.Best == "" && len(a.Moves) > 0 {
		a.Best = a.Moves[0].Move
	}

	a.Known = len(a.Moves) > 0 || a.Score != nil || a.Best != ""

	if allErr != nil || bestErr != nil || scoreErr != nil || pvErr != nil {
		a.Partial = true
	}
	if allErr != nil && bestErr != nil && scoreErr != nil && pvErr != nil {
		return nil, fmt.Errorf("chessdb unreachable: %w", errors.Join(allErr, bestErr, scoreErr, pvErr))
	}
	return a, nil
}

// get performs one API call, serving repeats from the cache.
func (c *ChessDB) get(ctx context.Context, action string, params url.Values) (string, error) {
	q := url.Values{}
	for k, vs := range params {
		q[k] = vs
	}
	q.Set("action", action)

	key := action + "?" + q.Encode()
	if body, ok := c.cache.get(key); ok {
		return body, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"?"+q.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("chessdb %s: %w", action, err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("chessdb %s: %w", action, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("chessdb %s: unexpected status %s", action, resp.Status)
	}
	// Responses are short; the limit just stops a surprise from filling memory.
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("chessdb %s: reading body: %w", action, err)
	}

	// chessdb terminates its replies with a NUL byte and no newline, so a bare
	// TrimSpace leaves it in place: "move:c0e2\x00" does not parse as a move,
	// and the last field of a queryall reply silently parses as zero. A NUL has
	// no legitimate place in these responses, so drop them before trimming.
	// chessdb terminates its replies with a NUL byte and no newline, so a bare
	// TrimSpace leaves it in place. It is not merely cosmetic: "move:c0e2\x00"
	// does not parse as a move, the last field of a queryall reply silently
	// becomes zero, and the last move of a PV is dropped because a coordinate
	// check rejects it. A NUL has no legitimate place in these responses.
	body := strings.TrimSpace(strings.ReplaceAll(string(raw), "\x00", ""))
	if body == "invalid board" {
		return "", ErrInvalidBoard
	}
	c.cache.set(key, body)
	return body, nil
}

// outOfBook is the set of bodies that mean "the database has nothing to say".
// These are normal outcomes, not errors.
func outOfBook(body string) bool {
	switch body {
	case "", "unknown", "nobestmove", "checkmate", "stalemate":
		return true
	}
	return false
}

// parseBestMove extracts the move from a querybest reply. It accepts the
// "move:" and "egtb:" forms but deliberately ignores "search:", which marks a
// candidate move that the API says needs further processing — the local engine
// searches those better than we could pick between them.
func parseBestMove(body string) (string, error) {
	if outOfBook(body) {
		return "", nil
	}
	if body == "invalid board" {
		return "", ErrInvalidBoard
	}
	for _, prefix := range []string{"move:", "egtb:"} {
		if rest, found := strings.CutPrefix(body, prefix); found {
			mv := strings.TrimSpace(rest)
			if i := strings.IndexByte(mv, '|'); i >= 0 {
				mv = strings.TrimSpace(mv[:i])
			}
			mv = strings.Fields(mv)[0]
			if !isCoord(mv) {
				return "", fmt.Errorf("chessdb: malformed move %q", mv)
			}
			return mv, nil
		}
	}
	if strings.HasPrefix(body, "search:") {
		return "", nil
	}
	return "", fmt.Errorf("chessdb: unrecognised querybest response %q", truncate(body, 120))
}

// parseMoveList parses a queryall reply: pipe-separated moves, each a
// comma-separated set of key:value fields. For example:
//
//	move:c0e2,score:1,rank:2,note:! (44-02),winrate:50.08|move:c3c4,...
func parseMoveList(body string) []Move {
	if outOfBook(body) || body == "invalid board" {
		return nil
	}
	var out []Move
	for _, entry := range strings.Split(body, "|") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		var m Move
		valid := false
		for _, field := range strings.Split(entry, ",") {
			key, val, found := strings.Cut(field, ":")
			if !found {
				continue
			}
			val = strings.TrimSpace(val)
			switch strings.TrimSpace(key) {
			case "move":
				m.Move = val
				valid = isCoord(val)
			case "score":
				m.Score, _ = strconv.Atoi(val)
			case "rank":
				m.Rank, _ = strconv.Atoi(val)
			case "note":
				m.Note = val
			case "winrate":
				m.Winrate, _ = strconv.ParseFloat(val, 64)
			}
		}
		if valid {
			out = append(out, m)
		}
	}
	return out
}

// parseScore parses a queryscore reply, e.g. "eval:1".
func parseScore(body string) (int, bool) {
	rest, found := strings.CutPrefix(body, "eval:")
	if !found {
		return 0, false
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest))
	if err != nil {
		return 0, false
	}
	return n, true
}

type pvResult struct {
	Moves    []string
	Depth    int
	Score    int
	HasScore bool
}

// parsePV parses a querypv reply, e.g.
//
//	score:1,depth:55,pv:b0c2|c6c5|a0a1|...
func parsePV(body string) (pvResult, bool) {
	if outOfBook(body) || body == "invalid board" {
		return pvResult{}, false
	}
	var out pvResult
	found := false
	for _, field := range strings.Split(body, ",") {
		key, val, ok := strings.Cut(field, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch strings.TrimSpace(key) {
		case "score":
			if n, err := strconv.Atoi(val); err == nil {
				out.Score, out.HasScore = n, true
				found = true
			}
		case "depth":
			out.Depth, _ = strconv.Atoi(val)
		case "pv":
			found = true
			for _, mv := range strings.Split(val, "|") {
				if mv = strings.TrimSpace(mv); isCoord(mv) {
					out.Moves = append(out.Moves, mv)
				}
			}
		}
	}
	if len(out.Moves) == 0 && !found {
		return pvResult{}, false
	}
	return out, true
}

// isCoord reports whether s is a UCCI coordinate move like "b0c2".
func isCoord(s string) bool {
	if len(s) != 4 {
		return false
	}
	if s[0] < 'a' || s[0] > 'i' || s[2] < 'a' || s[2] > 'i' {
		return false
	}
	return s[1] >= '0' && s[1] <= '9' && s[3] >= '0' && s[3] <= '9'
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
