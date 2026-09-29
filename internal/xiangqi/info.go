package xiangqi

import (
	"bufio"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/hmgle/godogpaw/engine"
)

// SearchResult is a finished search: the chosen move plus the engine's own
// statistics for the last completed iteration.
type SearchResult struct {
	BestMove Move
	Depth    uint8
	Score    engine.Value
	Nodes    uint64
	TimeMs   int
	PV       []Move

	// Fallback is set when the search produced no usable move (it was stopped
	// before completing an iteration) and a legal move was substituted, so the
	// caller can tell a real search result from a placeholder.
	Fallback bool
}

// searchWithInfo runs a search, capturing the "info ..." lines that
// SearchPositionWithLimits writes directly to stdout.
//
// The engine gives no callback or writer to hook, so the only way to get depth,
// score, node count, and the principal variation out of it is to temporarily
// point os.Stdout at a pipe and read what it prints. That is safe here only
// because callers serialize searches, so nothing else is writing to stdout for
// the duration.
func searchWithInfo(pos *engine.PositionNG, limits engine.SearchLimits) SearchResult {
	r, w, err := os.Pipe()
	if err != nil {
		// Without a pipe we lose the statistics but can still search.
		return SearchResult{BestMove: pos.SearchPositionWithLimits(limits)}
	}

	saved := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = saved }()

	var captured SearchResult
	done := make(chan struct{})
	go func() {
		defer close(done)
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			if line, ok := parseInfoLine(sc.Text()); ok {
				captured = line
			}
		}
	}()

	start := time.Now()
	best := pos.SearchPositionWithLimits(limits)
	elapsed := time.Since(start)

	// Restoring stdout before draining would let the engine's buffered output
	// escape, so close the write end first and wait for the reader to finish.
	os.Stdout = saved
	w.Close()
	<-done
	r.Close()

	captured.BestMove = best
	if captured.TimeMs == 0 {
		captured.TimeMs = int(elapsed.Milliseconds())
	}
	return captured
}

// parseInfoLine parses one of the engine's UCI-style info lines, e.g.
//
//	info score cp 26 depth 1 nodes 47 time 20.29ms pv h7e7
//
// Fields are scanned by keyword rather than position, so the parser tolerates
// the engine adding or reordering fields.
func parseInfoLine(line string) (SearchResult, bool) {
	if !strings.HasPrefix(line, "info ") {
		return SearchResult{}, false
	}
	f := strings.Fields(line)

	var out SearchResult
	seen := false
	for i := 0; i < len(f); i++ {
		switch f[i] {
		case "score":
			if i+2 < len(f) {
				switch f[i+1] {
				case "cp":
					out.Score = engine.Value(atoi(f[i+2]))
					seen = true
				case "mate":
					// Report mate scores the way the engine does, as a distance
					// from VALUE_MATE rather than a raw ply count.
					n := atoi(f[i+2])
					if n >= 0 {
						out.Score = engine.VALUE_MATE - engine.Value(n)
					} else {
						out.Score = -engine.VALUE_MATE - engine.Value(n)
					}
					seen = true
				}
				i += 2
			}
		case "depth":
			if i+1 < len(f) {
				out.Depth = uint8(atoi(f[i+1]))
				i++
			}
		case "nodes":
			if i+1 < len(f) {
				out.Nodes = uint64(atoi(f[i+1]))
				i++
			}
		case "time":
			if i+1 < len(f) {
				if d, err := time.ParseDuration(f[i+1]); err == nil {
					out.TimeMs = int(d.Milliseconds())
				}
				i++
			}
		case "pv":
			for j := i + 1; j < len(f); j++ {
				if m, ok := parseCoords(f[j]); ok {
					out.PV = append(out.PV, m)
				}
			}
			i = len(f)
		}
	}
	return out, seen
}

// parseCoords turns "h7e7" into a move without checking it against a position.
// PV moves beyond the first are not legal-verified, which is fine for display.
func parseCoords(s string) (Move, bool) {
	if len(s) < 4 {
		return engine.MOVE_NONE, false
	}
	from, err := engine.SquareFromString(s[:2])
	if err != nil {
		return engine.MOVE_NONE, false
	}
	to, err := engine.SquareFromString(s[2:4])
	if err != nil {
		return engine.MOVE_NONE, false
	}
	return engine.MakeMove(from, to), true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
