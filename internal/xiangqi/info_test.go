package xiangqi

import (
	"os"
	"testing"

	"github.com/hmgle/godogpaw/engine"
)

func TestParseInfoLine(t *testing.T) {
	tests := []struct {
		name     string
		line     string
		ok       bool
		depth    uint8
		score    engine.Value
		nodes    uint64
		timeMs   int
		pvLength int
	}{
		{
			name: "typical line", line: "info score cp 26 depth 1 nodes 47 time 20.2961ms pv h7e7",
			ok: true, depth: 1, score: 26, nodes: 47, timeMs: 20, pvLength: 1,
		},
		{
			name: "negative score and multi-move pv", line: "info score cp -51 depth 4 nodes 6029 time 20.2961ms pv b7e7 h2h9 i9h9 b2h2",
			ok: true, depth: 4, score: -51, nodes: 6029, timeMs: 20, pvLength: 4,
		},
		{
			name: "zero duration", line: "info score cp 13 depth 5 nodes 19944 time 0s pv h7e7 b0c2",
			ok: true, depth: 5, score: 13, nodes: 19944, timeMs: 0, pvLength: 2,
		},
		{
			name: "mate score", line: "info score mate 3 depth 5 nodes 100 time 1s pv a0a1",
			ok: true, depth: 5, score: engine.VALUE_MATE - 3, nodes: 100, timeMs: 1000, pvLength: 1,
		},
		{
			name: "negative mate score", line: "info score mate -2 depth 6 nodes 200 time 1s pv a0a1",
			ok: true, depth: 6, score: -engine.VALUE_MATE + 2, nodes: 200, timeMs: 1000, pvLength: 1,
		},
		{
			name: "empty pv", line: "info score cp 0 depth 1 nodes 0 time 1.5s pv",
			ok: true, depth: 1, score: 0, nodes: 0, timeMs: 1500, pvLength: 0,
		},
		{
			name: "fields reordered", line: "info depth 7 nodes 999 score cp 42 time 2s pv c3c4",
			ok: true, depth: 7, score: 42, nodes: 999, timeMs: 2000, pvLength: 1,
		},
		{name: "not an info line", line: "bestmove b7b5", ok: false},
		{name: "no score means nothing to report", line: "info depth 3 nodes 5", ok: false},
		{name: "empty string", line: "", ok: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseInfoLine(tc.line)
			if ok != tc.ok {
				t.Fatalf("parseInfoLine(%q) ok = %v, want %v", tc.line, ok, tc.ok)
			}
			if !ok {
				return
			}
			if got.Depth != tc.depth {
				t.Errorf("depth = %d, want %d", got.Depth, tc.depth)
			}
			if got.Score != tc.score {
				t.Errorf("score = %d, want %d", got.Score, tc.score)
			}
			if got.Nodes != tc.nodes {
				t.Errorf("nodes = %d, want %d", got.Nodes, tc.nodes)
			}
			if got.TimeMs != tc.timeMs {
				t.Errorf("timeMs = %d, want %d", got.TimeMs, tc.timeMs)
			}
			if len(got.PV) != tc.pvLength {
				t.Errorf("pv length = %d, want %d (%v)", len(got.PV), tc.pvLength, got.PV)
			}
		})
	}
}

// TestSearchCapturesStats checks the stdout capture actually reaches the engine's
// info lines and that the chosen move is legal.
func TestSearchCapturesStats(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	res, err := p.Search(engine.SearchLimits{Depth: 4})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}

	if res.Depth == 0 {
		t.Error("expected a nonzero depth from the captured info lines")
	}
	if res.Nodes == 0 {
		t.Error("expected a nonzero node count from the captured info lines")
	}
	if len(res.PV) == 0 {
		t.Error("expected a non-empty principal variation")
	}
	if !engine.IsOKMove(res.BestMove) {
		t.Fatalf("search returned no move: %v", res.BestMove)
	}
	if !p.isLegal(res.BestMove) {
		t.Errorf("search returned an illegal move %s", engine.Move2Str(res.BestMove))
	}
}

// TestSearchDoesNotLeakStdout verifies the capture restores os.Stdout, so the
// server's own logging keeps working after a search.
func TestSearchDoesNotLeakStdout(t *testing.T) {
	p, err := New(StartFEN)
	if err != nil {
		t.Fatal(err)
	}
	before := os.Stdout
	if _, err := p.Search(engine.SearchLimits{Depth: 2}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if after := os.Stdout; after != before {
		t.Error("searchWithInfo did not restore os.Stdout")
	}
}
