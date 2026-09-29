package cloud

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestParseBestMove(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr error
	}{
		{name: "plain move", body: "move:b0c2", want: "b0c2"},
		{name: "first of several", body: "move:b0c2|c3c4|g0e2", want: "b0c2"},
		{name: "tablebase move", body: "egtb:h9g7", want: "h9g7"},
		{name: "trainling newline already trimmed by caller", body: "move:b0c2", want: "b0c2"},
		{name: "unknown means out of book", body: "unknown", want: ""},
		{name: "nobestmove means out of book", body: "nobestmove", want: ""},
		{name: "checkmate means out of book", body: "checkmate", want: ""},
		{name: "stalemate means out of book", body: "stalemate", want: ""},
		{name: "empty body means out of book", body: "", want: ""},
		// "search:" marks a candidate the API says needs further processing, so
		// we prefer our own search over picking one blindly.
		{name: "search candidates are not treated as best", body: "search:b0c2", want: ""},
		{name: "invalid board is a real error", body: "invalid board", wantErr: ErrInvalidBoard},
		{name: "unrecognised body is an error", body: "banana", wantErr: nil},
		{name: "malformed move is an error", body: "move:zzz", wantErr: nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseBestMove(tc.body)
			switch {
			case tc.wantErr != nil:
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("parseBestMove(%q) err = %v, want %v", tc.body, err, tc.wantErr)
				}
			case tc.name == "unrecognised body is an error" || tc.name == "malformed move is an error":
				if err == nil {
					t.Fatalf("parseBestMove(%q) = %q, nil; want an error", tc.body, got)
				}
			default:
				if err != nil {
					t.Fatalf("parseBestMove(%q): %v", tc.body, err)
				}
				if got != tc.want {
					t.Errorf("parseBestMove(%q) = %q, want %q", tc.body, got, tc.want)
				}
			}
		})
	}
}

func TestParseMoveList(t *testing.T) {
	// Trimmed from a real reply for the start position.
	body := "move:c0e2,score:1,rank:2,note:! (44-02),winrate:50.08|" +
		"move:c3c4,score:1,rank:2,note:! (44-02),winrate:50.08|" +
		"move:b2b4,score:-4,rank:1,note:* (42-13),winrate:49.70|" +
		"move:b2b6,score:-13,rank:0,note:? (40-03),winrate:49.02"

	moves := parseMoveList(body)
	if len(moves) != 4 {
		t.Fatalf("got %d moves, want 4: %+v", len(moves), moves)
	}

	first := moves[0]
	if first.Move != "c0e2" {
		t.Errorf("first move = %q, want %q", first.Move, "c0e2")
	}
	if first.Score != 1 {
		t.Errorf("first score = %d, want 1", first.Score)
	}
	if first.Rank != 2 {
		t.Errorf("first rank = %d, want 2", first.Rank)
	}
	if first.Note != "! (44-02)" {
		t.Errorf("first note = %q, want %q", first.Note, "! (44-02)")
	}
	if first.Winrate != 50.08 {
		t.Errorf("first winrate = %v, want 50.08", first.Winrate)
	}

	if last := moves[3]; last.Score != -13 || last.Rank != 0 || last.Note != "? (40-03)" {
		t.Errorf("last move = %+v, want score -13, rank 0, note %q", last, "? (40-03)")
	}
}

func TestParseMoveListEdgeCases(t *testing.T) {
	for _, body := range []string{"", "unknown", "nobestmove", "checkmate", "stalemate", "invalid board"} {
		if got := parseMoveList(body); got != nil {
			t.Errorf("parseMoveList(%q) = %+v, want nil", body, got)
		}
	}
	// Entries without a usable move are skipped rather than emitted blank.
	if got := parseMoveList("score:1,rank:1|move:c3c4,score:1"); len(got) != 1 || got[0].Move != "c3c4" {
		t.Errorf("got %+v, want only the c3c4 entry", got)
	}
}

func TestParseScore(t *testing.T) {
	tests := []struct {
		body string
		want int
		ok   bool
	}{
		{"eval:1", 1, true},
		{"eval:-30", -30, true},
		{"eval:0", 0, true},
		{"eval: 12 ", 12, true},
		{"unknown", 0, false},
		{"invalid board", 0, false},
		{"eval:abc", 0, false},
		{"", 0, false},
	}
	for _, tc := range tests {
		got, ok := parseScore(tc.body)
		if ok != tc.ok {
			t.Errorf("parseScore(%q) ok = %v, want %v", tc.body, ok, tc.ok)
			continue
		}
		if ok && got != tc.want {
			t.Errorf("parseScore(%q) = %d, want %d", tc.body, got, tc.want)
		}
	}
}

func TestParsePV(t *testing.T) {
	body := "score:1,depth:55,pv:b0c2|c6c5|a0a1|h9g7"
	pv, ok := parsePV(body)
	if !ok {
		t.Fatalf("parsePV(%q) returned not-ok", body)
	}
	if pv.Depth != 55 {
		t.Errorf("depth = %d, want 55", pv.Depth)
	}
	if !pv.HasScore || pv.Score != 1 {
		t.Errorf("score = %d (has=%v), want 1", pv.Score, pv.HasScore)
	}
	if len(pv.Moves) != 4 {
		t.Fatalf("pv moves = %v, want 4", pv.Moves)
	}
	if pv.Moves[0] != "b0c2" || pv.Moves[3] != "h9g7" {
		t.Errorf("pv moves = %v, want first b0c2 and last h9g7", pv.Moves)
	}

	for _, bad := range []string{"unknown", "invalid board", ""} {
		if _, ok := parsePV(bad); ok {
			t.Errorf("parsePV(%q) ok = true, want false", bad)
		}
	}
}

func TestIsCoord(t *testing.T) {
	valid := []string{"a0a1", "b0c2", "i9i0", "e0e9"}
	invalid := []string{"", "a0a", "a0a1a2", "j0j1", "a0b", "00ab", "aXbY"}
	for _, s := range valid {
		if !isCoord(s) {
			t.Errorf("isCoord(%q) = false, want true", s)
		}
	}
	for _, s := range invalid {
		if isCoord(s) {
			t.Errorf("isCoord(%q) = true, want false", s)
		}
	}
}

// fakeServer stands in for chessdb.cn. It answers each action from bodies, and
// fails with a 500 for any action listed in fail.
func fakeServer(t *testing.T, bodies map[string]string, fail map[string]bool, requests *atomic.Int64) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if requests != nil {
			requests.Add(1)
		}
		action := r.URL.Query().Get("action")

		if fail[action] {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		body, ok := bodies[action]
		if !ok {
			http.Error(w, "unknown action", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, body)
	}))
}

func TestBestMoveOverHTTP(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		want    string
		wantErr error
	}{
		{name: "in book", body: "move:b0c2", want: "b0c2"},
		// The live service ends every reply with a NUL and no newline. Verified
		// against chessdb.cn directly: `move:c0e2\0`.
		{name: "in book, NUL terminated", body: "move:b0c2\x00", want: "b0c2"},
		{name: "out of book", body: "unknown", want: ""},
		{name: "invalid board", body: "invalid board", wantErr: ErrInvalidBoard},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := fakeServer(t, map[string]string{"querybest": tc.body}, nil, nil)
			defer srv.Close()

			c := New(WithBaseURL(srv.URL), WithCache(0, 0))
			got, err := c.BestMove(context.Background(), "some fen w - - 0 1")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("move = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBestMoveTransportFailure(t *testing.T) {
	var requests atomic.Int64
	srv := fakeServer(t, nil, map[string]bool{"querybest": true}, &requests)
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithCache(0, 0))
	if _, err := c.BestMove(context.Background(), "fen w"); err == nil {
		t.Fatal("expected an error for a 500 response")
	}
}

func TestBestMoveRespectsContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithCache(0, 0))
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	if _, err := c.BestMove(ctx, "fen w"); err == nil {
		t.Fatal("expected a timeout error")
	}
}

func TestCacheAvoidsRepeatedRequests(t *testing.T) {
	var requests atomic.Int64
	srv := fakeServer(t, map[string]string{"querybest": "move:b0c2"}, nil, &requests)
	defer srv.Close()

	c := New(WithBaseURL(srv.URL))
	for range 5 {
		if _, err := c.BestMove(context.Background(), "fen w"); err != nil {
			t.Fatal(err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Errorf("made %d requests, want 1 (the rest should be cache hits)", got)
	}

	hits, _ := c.cache.stats()
	if hits != 4 {
		t.Errorf("cache hits = %d, want 4", hits)
	}
}

func TestCacheIsBounded(t *testing.T) {
	const limit = 32
	c := New(WithCache(limit, time.Minute))
	for i := range 200 {
		c.cache.set(fmt.Sprintf("key-%d", i), "body")
	}
	if got := c.cache.len(); got > limit {
		t.Errorf("cache holds %d entries, want at most %d", got, limit)
	}
}

func TestAnalysisGathersEverything(t *testing.T) {
	srv := fakeServer(t, map[string]string{
		"queryall":   "move:c0e2,score:1,rank:2,note:! (44-02),winrate:50.08|move:c3c4,score:1,rank:2,note:! (44-02),winrate:50.08",
		"querybest":  "move:b0c2",
		"queryscore": "eval:7",
		"querypv":    "score:7,depth:55,pv:b0c2|c6c5|a0a1",
	}, nil, nil)
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithCache(0, 0))
	a, err := c.Analysis(context.Background(), "fen w - - 0 1")
	if err != nil {
		t.Fatalf("Analysis: %v", err)
	}
	if !a.Known {
		t.Error("Known = false, want true")
	}
	if a.Partial {
		t.Error("Partial = true, want false")
	}
	if a.Best != "b0c2" {
		t.Errorf("Best = %q, want b0c2", a.Best)
	}
	if a.Score == nil || *a.Score != 7 {
		t.Errorf("Score = %v, want 7", a.Score)
	}
	if len(a.Moves) != 2 {
		t.Errorf("Moves = %+v, want 2 entries", a.Moves)
	}
	if a.PVDepth != 55 || len(a.PV) != 3 {
		t.Errorf("PV = %v depth %d, want 3 moves at depth 55", a.PV, a.PVDepth)
	}
}

func TestAnalysisParsesTheLastFieldOfANULTerminatedReply(t *testing.T) {
	// Every chessdb reply ends with a NUL. Only the final field of the final
	// entry is adjacent to it, so a NUL that survives trimming corrupts exactly
	// one value — the last move's winrate — and does it silently, by leaving the
	// zero value in place.
	srv := fakeServer(t, map[string]string{
		"queryall":   "move:c0e2,score:1,rank:2,note:! (44-02),winrate:50.08|move:c3c4,score:1,rank:2,note:! (44-02),winrate:49.75\x00",
		"querybest":  "move:c0e2\x00",
		"queryscore": "eval:1\x00",
		"querypv":    "score:1,depth:71,pv:c3c4|b7c7\x00",
	}, nil, nil)
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithCache(0, 0))
	a, err := c.Analysis(context.Background(), "fen w - - 0 1")
	if err != nil {
		t.Fatalf("Analysis: %v", err)
	}
	if a.Partial {
		t.Error("Partial = true, want false — a NUL must not read as a query failure")
	}
	if a.Best != "c0e2" {
		t.Errorf("Best = %q, want c0e2", a.Best)
	}
	if a.Score == nil || *a.Score != 1 {
		t.Errorf("Score = %v, want 1", a.Score)
	}
	if a.PVDepth != 71 || len(a.PV) != 2 {
		t.Errorf("PV = %v at depth %d, want 2 moves at depth 71", a.PV, a.PVDepth)
	}
	if len(a.Moves) != 2 {
		t.Fatalf("Moves = %+v, want 2 entries", a.Moves)
	}
	if got := a.Moves[1].Winrate; got != 49.75 {
		t.Errorf("last move's Winrate = %v, want 49.75 — the NUL ate the final field", got)
	}
}

func TestAnalysisFallsBackToTopMoveWhenBestIsEmpty(t *testing.T) {
	// An egtb-only or partial reply can leave querybest empty while queryall
	// still ranks the position.
	srv := fakeServer(t, map[string]string{
		"queryall":   "move:c0e2,score:1,rank:2,note:! (44-02),winrate:50.08|move:c3c4,score:1,rank:2",
		"querybest":  "search:b0c2",
		"queryscore": "eval:1",
		"querypv":    "score:1,depth:55,pv:b0c2",
	}, nil, nil)
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithCache(0, 0))
	a, err := c.Analysis(context.Background(), "fen w")
	if err != nil {
		t.Fatalf("Analysis: %v", err)
	}
	if a.Best != "c0e2" {
		t.Errorf("Best = %q, want the top-ranked c0e2", a.Best)
	}
}

func TestAnalysisDegradesWhenOneQueryFails(t *testing.T) {
	srv := fakeServer(t,
		map[string]string{
			"queryall":  "move:c0e2,score:1,rank:2,note:! (44-02),winrate:50.08",
			"querybest": "move:b0c2",
		},
		map[string]bool{"queryscore": true, "querypv": true},
		nil)
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithCache(0, 0))
	a, err := c.Analysis(context.Background(), "fen w")
	if err != nil {
		t.Fatalf("Analysis should survive a partial outage, got %v", err)
	}
	if !a.Partial {
		t.Error("Partial = false, want true")
	}
	if a.Best != "b0c2" || len(a.Moves) != 1 {
		t.Errorf("analysis lost good data: %+v", a)
	}
	if a.Score != nil {
		t.Error("Score should be nil when queryscore failed")
	}
}

func TestAnalysisErrorsWhenCloudIsUnreachable(t *testing.T) {
	srv := fakeServer(t, nil,
		map[string]bool{"queryall": true, "querybest": true, "queryscore": true, "querypv": true},
		nil)
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithCache(0, 0))
	if _, err := c.Analysis(context.Background(), "fen w"); err == nil {
		t.Fatal("expected an error when every query fails")
	}
}

func TestAnalysisSurfacesInvalidBoard(t *testing.T) {
	// A rejected board is our own bug, so it must not be swallowed as a partial
	// result.
	srv := fakeServer(t, map[string]string{
		"queryall":   "invalid board",
		"querybest":  "invalid board",
		"queryscore": "eval:1",
		"querypv":    "score:1,depth:1,pv:b0c2",
	}, nil, nil)
	defer srv.Close()

	c := New(WithBaseURL(srv.URL), WithCache(0, 0))
	if _, err := c.Analysis(context.Background(), "bad fen"); !errors.Is(err, ErrInvalidBoard) {
		t.Fatalf("err = %v, want ErrInvalidBoard", err)
	}
}

func TestGetSendsExpectedRequest(t *testing.T) {
	var gotPath, gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		fmt.Fprint(w, "move:b0c2")
	}))
	defer srv.Close()

	// Mirror the real endpoint's path so the assertion below is about the client
	// rather than about httptest's bare root URL.
	c := New(WithBaseURL(srv.URL+"/chessdb.php"), WithCache(0, 0))
	if _, err := c.BestMove(context.Background(), "rnbakabnr/9/1c5c1/p1p1p1p1p/9/9/P1P1P1P1P/1C5C1/9/RNBAKABNR w - - 0 1"); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(gotPath, "/chessdb.php") {
		t.Errorf("path = %q, want it to end with /chessdb.php", gotPath)
	}
	if !strings.Contains(gotQuery, "action=querybest") {
		t.Errorf("query = %q, want action=querybest", gotQuery)
	}
	if !strings.Contains(gotQuery, "board=") {
		t.Errorf("query = %q, want a board parameter", gotQuery)
	}
}

func TestNewDefaults(t *testing.T) {
	c := New()
	if c.baseURL != DefaultBaseURL {
		t.Errorf("baseURL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
	if c.http == nil || c.cache == nil {
		t.Error("New() left a nil field")
	}
}
