// Command selfplay measures the difficulty ladder by playing its levels against
// each other.
//
// The spec requires the ladder's ordering to be established by playing, not
// assumed from the parameters, and this is what plays it: adjacent pairs from
// game.LevelNames(), both sides of each, reporting a win rate per pair and a
// blunder rate per level. The numbers it prints are the ones design.md records
// for the committed level table.
//
// Run it from the repository root. A full run takes hours — the top pair alone takes
// ~15 minutes for eight games, and the reference search that judges each move is another
// full search on top of the move it judges. Splitting the run by pair is what keeps each
// command to one sitting, and each pair's -refdepth is the stronger level's own depth, so
// the judge is never weaker than the play it is judging:
//
//	go run testdata/selfplay/main.go -pairs beginner/easy -refdepth 4
//	go run testdata/selfplay/main.go -pairs easy/medium   -refdepth 6
//	go run testdata/selfplay/main.go -pairs medium/hard   -refdepth 8
//	go run testdata/selfplay/main.go -pairs hard/master   -refdepth 8
//
// A short run finishes in seconds rather than hours — around twenty of them,
// because a windowed level pays for a candidate pass on every move. It is how
// the tool itself is checked, including the blunder pass, which is what makes it
// short rather than trivial. The reference depth is the pair's own, since the
// default is too deep to judge a beginner cheaply:
//
//	go run testdata/selfplay/main.go -pairs beginner/easy -games 1 -maxplies 24 -refdepth 4
//
// The numbers design.md records were measured with -seed 1. That seed plus the
// committed level table is the whole input: the same command reproduces the same
// numbers exactly, because every random draw in a run comes from the seed.
//
// It lives under testdata/ for the reason the node tests there do: the go
// command ignores a testdata directory, so this file is invisible to
// `go build ./...`, `go vet ./...` and `go test ./...`. Nothing that takes hours
// belongs in CI. What is committed is its output — a level table tuned against
// these numbers — not the run itself.
//
// How it measures:
//
//   - Each adjacent pair plays -games games with the stronger level on each
//     side, so 2 x -games games per pair. Two numbers come out of that: the win
//     rate, the share of games the stronger level won, and the score, the share
//     of the available points it took with a draw counting half. The score is
//     the one calibration is banded on, because it is the spec's requirement on
//     a scale a draw-heavy ladder can be tuned on — a score above 50% is exactly
//     "wins more games than it loses". A game that reaches -maxplies is recorded
//     as a draw, which is why those are counted and reported separately. Note
//     that a level with no window searches deterministically, so replaying a
//     pair with a different seed replays the same game: -games above 1 buys
//     information only from levels that have a window, and against a table of
//     windows set to 0, -games 1 is the whole measurement.
//
//   - Every move is judged by a reference search at -refdepth: the best value
//     available in the position the move was chosen in, against the value of the
//     move actually played, both at that depth. A move is a blunder when the
//     difference exceeds -threshold centipawns. -refdepth belongs at or above
//     the strongest level's own depth in the pair, or the judge is weaker than
//     the play it is judging and its numbers overstate the blunders.
//
//   - The free-choice comparison the spec names is measured at the same
//     positions rather than in separate games: for each position a level faced,
//     one legal move is drawn uniformly and valued by the same reference search.
//     A uniformly drawn legal move is what "a search of the same depth choosing
//     freely" reduces to — the search ranks the moves and the choice ignores the
//     ranking — so both columns come from the same positions and the same judge,
//     and differ only in which move was chosen.
//
//   - Judging is a second pass, over the games exactly as they were played.
//     godogpaw ages its transposition table rather than clearing it between
//     searches, so a search interleaved with the play changes the move that
//     follows it. A judge that ran during a game would be measuring a game it
//     had altered — run a pair with the blunder pass on and off and the games
//     differ. So play finishes first; -blunder changes the second table and
//     never the first.
//
// The judge is deliberately not the package's own candidate scoring. A
// measurement that valued moves with the code it is measuring would agree with
// it by construction.
//
// The whole run is a function of -seed: game n of the run plays from -seed+n.
// That game's own moves and its free-choice draws use separate sources derived
// from that seed, so -blunder=false changes what is measured and not what is
// played.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/hmgle/godogpaw/engine"

	"chessai/internal/game"
	"chessai/internal/xiangqi"
)

// freeChoiceSeedOffset separates the two random sources a game uses. The
// free-choice draw is a measurement, and a measurement must not decide what the
// game did next.
const freeChoiceSeedOffset = 1_000_000

// pair is one rung of the ladder: a level and the one directly above it.
type pair struct {
	weak   string
	strong string
}

func (p pair) String() string { return p.weak + "->" + p.strong }

type config struct {
	games     int
	maxPlies  int
	seed      int64
	refDepth  uint8
	threshold int
	blunder   bool
	pairs     []pair
}

// moveStats is what one level's moves came to, over every game it played.
type moveStats struct {
	moves, blunders         int
	freeMoves, freeBlunders int
}

// pairTally is what one adjacent pair came to.
type pairTally struct {
	pair       pair
	games      int
	strongWins int
	weakWins   int
	draws      int
	plies      int
	capped     int
	levels     map[string]moveStats
}

func (t *pairTally) winRate() float64 {
	if t.games == 0 {
		return 0
	}
	return float64(t.strongWins) / float64(t.games)
}

// score is the share of the available points the stronger level took, a draw
// counting half.
//
// It is the quantity calibration is banded on, and it is not the same as the win
// rate: the spec's requirement is that a level "wins more games than it loses",
// and score > 1/2 is exactly that — (W + D/2)/N > 1/2 simplifies to W > L. On a
// ladder where most games are draws, the win rate alone cannot tell a pair that
// is ordered from one that is level.
func (t *pairTally) score() float64 {
	if t.games == 0 {
		return 0
	}
	return (float64(t.strongWins) + float64(t.draws)/2) / float64(t.games)
}

// gameResult is how one game ended.
type gameResult struct {
	winner xiangqi.Side // meaningless when draw is set
	draw   bool
	plies  int
	capped bool // stopped at the ply cap rather than by the game ending
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "selfplay:", err)
		os.Exit(1)
	}
}

func run() error {
	games := flag.Int("games", 4, "games per side for each adjacent pair (the pair plays twice this)")
	maxPlies := flag.Int("maxplies", 240, "stop a game after this many plies and record it as a draw")
	seed := flag.Int64("seed", 1, "base seed: game n of the run plays from seed+n")
	refDepth := flag.Int("refdepth", 8, "depth of the reference search that judges each move")
	threshold := flag.Int("threshold", 200, "centipawns below the best move at which a move counts as a blunder")
	blunder := flag.Bool("blunder", true, "measure blunder rates; off is a much faster run reporting win rates only")
	pairsSpec := flag.String("pairs", "", `adjacent pairs to play, e.g. "hard/master"; empty plays every pair`)
	flag.Parse()

	if *games < 1 {
		return fmt.Errorf("-games must be at least 1, got %d", *games)
	}
	if *maxPlies < 1 {
		return fmt.Errorf("-maxplies must be at least 1, got %d", *maxPlies)
	}
	if *refDepth < 1 || *refDepth > 32 {
		return fmt.Errorf("-refdepth must be 1..32, got %d", *refDepth)
	}

	names := game.LevelNames()
	pairs, err := parsePairs(*pairsSpec, names)
	if err != nil {
		return err
	}
	cfg := config{
		games:     *games,
		maxPlies:  *maxPlies,
		seed:      *seed,
		refDepth:  uint8(*refDepth),
		threshold: *threshold,
		blunder:   *blunder,
		pairs:     pairs,
	}

	start := time.Now()
	rows := make([]pairTally, 0, len(cfg.pairs))
	// Every game is kept until the play is over: judging is a second pass, so
	// that no judge search can change a move that has not been played yet.
	var played []recordedGame
	for _, p := range cfg.pairs {
		row := pairTally{pair: p, levels: map[string]moveStats{}}
		// Both sides, so a level cannot look stronger merely by moving first.
		for _, strongSide := range []xiangqi.Side{xiangqi.Red, xiangqi.Black} {
			for i := 0; i < cfg.games; i++ {
				gameSeed := cfg.seed + int64(len(played))
				// Progress goes to stderr, so that stdout stays the table this
				// run exists to produce. A run nobody can watch is a run nobody
				// can tell apart from one that has hung.
				fmt.Fprintf(os.Stderr, "%s: game %d/%d, %s as %s, seed %d ...\n",
					p, i+1, cfg.games, p.strong, sideName(strongSide), gameSeed)

				res, moves, err := playGame(context.Background(), p, strongSide, gameSeed, cfg)
				if err != nil {
					return fmt.Errorf("%s game %d (seed %d): %w", p, len(played), gameSeed, err)
				}
				fmt.Fprintf(os.Stderr, "%s: %s after %d plies\n\n", p, describe(res), res.plies)

				row.add(res, strongSide)
				played = append(played, recordedGame{
					pairIndex: len(rows),
					seed:      gameSeed,
					moves:     moves,
				})
			}
		}
		rows = append(rows, row)
	}

	if cfg.blunder {
		fmt.Fprintf(os.Stderr, "judging %d game(s) at depth %d ...\n", len(played), cfg.refDepth)
		for i, g := range played {
			stats := map[string]moveStats{}
			if err := judgeGame(g.seed, g.moves, cfg, stats); err != nil {
				return fmt.Errorf("judging game %d: %w", i, err)
			}
			for level, s := range stats {
				merged := rows[g.pairIndex].levels[level]
				merged.add(s)
				rows[g.pairIndex].levels[level] = merged
			}
		}
	}

	printTables(rows, cfg, time.Since(start))
	return nil
}

// recordedGame is a finished game waiting to be judged.
type recordedGame struct {
	pairIndex int
	seed      int64
	moves     []playedMove
}

func sideName(s xiangqi.Side) string {
	if s == xiangqi.Red {
		return "red"
	}
	return "black"
}

// describe renders a game's outcome for the progress line.
func describe(r gameResult) string {
	switch {
	case r.draw && r.capped:
		return "stopped at the ply cap"
	case r.draw:
		return "draw"
	default:
		return sideName(r.winner) + " wins"
	}
}

// parsePairs resolves -pairs against the ladder. An empty spec means every
// adjacent pair, which is the full run. Only adjacent pairs are accepted: the
// win rate between two levels with another level between them says nothing
// about the ladder's spacing.
func parsePairs(spec string, names []string) ([]pair, error) {
	all := make([]pair, 0, len(names)-1)
	for i := 1; i < len(names); i++ {
		all = append(all, pair{weak: names[i-1], strong: names[i]})
	}
	if strings.TrimSpace(spec) == "" {
		return all, nil
	}

	var out []pair
	for _, item := range strings.Split(spec, ",") {
		item = strings.TrimSpace(item)
		parts := strings.Split(item, "/")
		if len(parts) != 2 {
			return nil, fmt.Errorf("-pairs wants weak/strong, got %q", item)
		}
		want := pair{weak: parts[0], strong: parts[1]}
		found := false
		for _, a := range all {
			if a == want {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("%q is not an adjacent pair of the ladder (%s)", item, strings.Join(names, " "))
		}
		out = append(out, want)
	}
	return out, nil
}

// add folds a game's outcome into the pair's tally.
func (t *pairTally) add(r gameResult, strongSide xiangqi.Side) {
	t.games++
	t.plies += r.plies
	if r.capped {
		t.capped++
	}
	switch {
	case r.draw:
		t.draws++
	case r.winner == strongSide:
		t.strongWins++
	default:
		t.weakWins++
	}
}

func (s *moveStats) add(o moveStats) {
	s.moves += o.moves
	s.blunders += o.blunders
	s.freeMoves += o.freeMoves
	s.freeBlunders += o.freeBlunders
}

// playGame plays one game of the pair, with the stronger level on strongSide,
// and returns how it ended along with the moves it was played with, for the
// judge to read afterwards.
//
// Nothing is judged here. The engine's transposition table ages rather than
// clearing between searches — see clearSearch in godogpaw's engine — so a search
// that runs between two moves changes the move that follows it. A judge that ran
// during the game would therefore be measuring a game it had altered: run the
// same pair with the blunder pass on and off and the games differ. So the play
// is finished first, and every game in the run is judged after the last one has
// been played.
func playGame(ctx context.Context, p pair, strongSide xiangqi.Side, seed int64, cfg config) (gameResult, []playedMove, error) {
	pos, err := xiangqi.New("")
	if err != nil {
		return gameResult{}, nil, err
	}
	moveRand := rand.New(rand.NewSource(seed))
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	var moves []playedMove

	for plies := 0; ; plies++ {
		if res := pos.Result(); res.Over {
			return gameResult{winner: res.Winner, draw: res.Draw, plies: plies}, moves, nil
		}
		if plies >= cfg.maxPlies {
			return gameResult{draw: true, plies: plies, capped: true}, moves, nil
		}

		level := p.weak
		if pos.SideToMove() == strongSide {
			level = p.strong
		}
		if _, err := game.LevelMove(ctx, pos, level, nil, log, game.WithRand(moveRand)); err != nil {
			return gameResult{}, nil, err
		}
		moves = append(moves, playedMove{level: level, move: pos.LastMove()})
	}
}

// playedMove is one move of a finished game, with the level that chose it.
type playedMove struct {
	level string
	move  xiangqi.Move
}

// judgeGame replays a finished game's moves and judges each one.
//
// Replaying rather than recording positions is what keeps the judge honest about
// the position it is judging: the moves are the same, played from the same
// start, so the repetition history the engine sees is the one the game had.
func judgeGame(seed int64, moves []playedMove, cfg config, out map[string]moveStats) error {
	pos, err := xiangqi.New("")
	if err != nil {
		return err
	}
	// The free-choice draws come from this game's own seed, so a judged run
	// reproduces exactly as an unjudged one does.
	freeRand := rand.New(rand.NewSource(seed + freeChoiceSeedOffset))

	for _, pm := range moves {
		judge(pos, pm.move, pm.level, cfg, freeRand, out)
		if err := pos.Play(pm.move); err != nil {
			return fmt.Errorf("replaying the game: %w", err)
		}
	}
	return nil
}

// judge records what one move cost against the best move available, and what a
// freely chosen legal move would have cost in the same position.
//
// A position whose reference search does not complete is left out of the count
// rather than counted as clean: an unjudged move is not a good one.
func judge(before *xiangqi.Position, played xiangqi.Move, level string, cfg config, freeRand *rand.Rand, out map[string]moveStats) {
	res, err := before.Search(engine.SearchLimits{Depth: cfg.refDepth})
	if err != nil || res.Fallback || !engine.IsOKMove(res.BestMove) {
		return
	}
	best := int(res.Score)

	st := out[level]
	if v, ok := moveValue(before, played, cfg.refDepth); ok {
		st.moves++
		if best-v > cfg.threshold {
			st.blunders++
		}
	}
	if legal := before.LegalMoves(); len(legal) > 0 {
		pick := legal[freeRand.Intn(len(legal))]
		if v, ok := moveValue(before, pick, cfg.refDepth); ok {
			st.freeMoves++
			if best-v > cfg.threshold {
				st.freeBlunders++
			}
		}
	}
	out[level] = st
}

// moveValue returns the value of playing m in pos, from the point of view of the
// side playing it.
//
// The sign flip is the whole subtlety: a search scores from the side to move,
// which after m is the opponent. The same flip is what the package's own
// candidate scoring does, and both have to agree about whose move it is.
func moveValue(pos *xiangqi.Position, m xiangqi.Move, depth uint8) (int, bool) {
	clone, err := pos.Clone()
	if err != nil {
		return 0, false
	}
	if err := clone.Play(m); err != nil {
		return 0, false
	}
	res, err := clone.Search(engine.SearchLimits{Depth: depth})
	if err != nil || res.Fallback {
		return 0, false
	}
	return -int(res.Score), true
}

func printTables(rows []pairTally, cfg config, elapsed time.Duration) {
	fmt.Printf("selfplay: %d adjacent pair(s), %d game(s) per side (%d per pair), seed %d\n",
		len(rows), cfg.games, 2*cfg.games, cfg.seed)
	fmt.Printf("ply cap %d; reference depth %d, blunder threshold %dcp\n\n", cfg.maxPlies, cfg.refDepth, cfg.threshold)

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "pair\tgames\tstronger W-L-D\twin rate\tscore\tavg plies")
	capped := 0
	for _, r := range rows {
		capped += r.capped
		avg := 0.0
		if r.games > 0 {
			avg = float64(r.plies) / float64(r.games)
		}
		fmt.Fprintf(w, "%s\t%d\t%d-%d-%d\t%.1f%%\t%.1f%%\t%.1f\n",
			r.pair, r.games, r.strongWins, r.weakWins, r.draws, r.winRate()*100, r.score()*100, avg)
	}
	w.Flush()

	if cfg.blunder {
		fmt.Println()
		w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "pair\tlevel\tref\tmoves\tblunder rate\tfree-choice rate")
		for _, r := range rows {
			// Weakest first, so each pair reads up the ladder.
			for _, level := range []string{r.pair.weak, r.pair.strong} {
				s := r.levels[level]
				fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\t%s\n",
					r.pair, level, cfg.refDepth, s.moves, rate(s.blunders, s.moves), rate(s.freeBlunders, s.freeMoves))
			}
		}
		w.Flush()
	} else {
		fmt.Println("\nblunder rates not measured (-blunder=false)")
	}

	if capped > 0 {
		fmt.Printf("\n%d game(s) reached the %d-ply cap and were recorded as draws.\n", capped, cfg.maxPlies)
	}
	fmt.Printf("total: %s\n", elapsed.Round(time.Millisecond))
}

// rate renders a share, or a dash when nothing was counted — which is not the
// same as zero.
func rate(n, of int) string {
	if of == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f%%", 100*float64(n)/float64(of))
}
