# Development

How the engine is put together, how the difficulty ladder works, how to retune
it, and what the HTTP API promises. For what the app is and how to play it, see
the [README](../README.md).

## Layout

```
main.go               flags, static file serving, wiring, graceful shutdown
internal/game/        the game and the difficulty ladder
  game.go             Config, ConfigPatch, State, AIDecision; the one-game state machine
  levels.go           THE LEVEL TABLE — the only place difficulty is defined
  select.go           the candidate pass and the window
  ai.go               the decision path: book, search, hint
  match.go            playing the ladder against itself, for the harness
internal/httpapi/     routes, JSON shapes, the error table
internal/xiangqi/     position, rules and FEN over the godogpaw engine
internal/cloud/       chessdb.cn client and the analysis cache
web/                  the interface; embedded into the binary
testdata/             the self-play harness and the node i18n test
```

`game` is the only package with domain logic. `httpapi` translates HTTP to it
and its errors back to statuses; it holds no rules.

## How a request flows

```
  browser
     |  fetch /api/*
     v
  httpapi.Server            routes, JSON encode/decode, error -> status+code
     |
     v
  game.Game                 the one game: position, config, generation counter
     |            \
     |             \-- cloud.Client ----> chessdb.cn   (opening book, cached)
     v
  xiangqi.Position          rules, FEN; wraps godogpaw
     |
     v
  godogpaw engine           the search itself
```

`game.Game` holds exactly one game. There is no lobby, no session, no
per-player state: `playerSide` is a property of the server's single game, which
is why two browser tabs show the same board.

## The level table

Everything about difficulty lives in `internal/game/levels.go`. A level is a
name plus two numbers:

```go
type level struct {
    Name   string
    Depth  uint8  // how many plies the level's moves are judged at
    Window int    // centipawns; how far below the best a move may be and still be played
}
```

| Level | Depth | Window |
| --- | --- | --- |
| `beginner` | 2 | 120 |
| `easy` | 4 | 90 |
| `medium` | 5 | 55 |
| `hard` | 5 | 30 |
| `master` | 8 | 0 |

The two invariants `TestLevelTable` enforces: `Depth` never decreases up the
ladder, and `Window` strictly decreases. `Window` carries the spacing; `Depth`
does not, which is why it is allowed to repeat — `medium` and `hard` are both
depth 5.

Nothing outside `internal/game` sees either number. A client sends a level
*name* and reads a name back, so retuning the ladder moves no client code and no
translation.

### What actually plays

- **A windowed level (`beginner` … `hard`) runs no search of its own.** It
  builds a ranked list of the position's legal moves at its own `Depth`, one
  search per legal move on a clone, and plays one of the moves within `Window`
  centipawns of the best. That pass *is* the level's search. The decision it
  reports carries the chosen move's score, its own depth, and a one-move line —
  and `nodes`/`timeMs` are the **sum over every candidate scored**, including
  candidates the window rejected, because those searches were paid for.
- **`master` has `Window 0` and skips the pass entirely.** It searches at its
  `Depth` and plays the move the search returned, reporting that search's own
  nodes, time and principal variation.
- **A forced position forces every level.** With only one move inside the
  window, every level plays it. This is deliberate: the goal is a weaker
  *player*, not a blind engine that hangs material.
- **The window is evaluated on the value from the side choosing**, so the
  engine's side-to-move scores are negated when a candidate is scored.
- **`Hint` is exempt.** It searches at `HintDepth` (8) with the window cleared,
  whatever level is being advised.

### Why one search per legal move

godogpaw has no MultiPV — `SearchLimits` is `{Depth, TimeLimit, Infinite}` and a
search returns one move — so a ranked list has to be built here by cloning the
position and searching each root move. That is ~40 searches in the opening,
which is what bounds a windowed level's depth well below `master`'s.

Two consequences to know before touching this code:

- **All searches serialize through `Game.searchMu`.** godogpaw's stop flag, PV
  tables, history and transposition tables are package-level, so two concurrent
  searches would corrupt each other. `TestDeepSearchOnSparseEndgamesDoesNotPanic`
  and the isolation tests in `internal/xiangqi` pin the upstream behaviours this
  depends on.
- **The engine's transposition table ages rather than clears**, so interleaving
  searches changes what later searches see. This is why the self-play harness
  judges each move in a *second pass* rather than during play, and why the
  calibration was re-measured rather than assumed after the discarded searches
  were removed.

## Calibration

The ladder's numbers are measured, not chosen. `testdata/selfplay` plays
adjacent levels against each other; its output is committed as the constants in
`levels.go`.

**The band.** A pair is calibrated when the stronger level scores 55-75% of the
points over eight games, a draw counting half. With `score = (W + D/2) / N`,
`score > 1/2` is exactly the spec's "wins more games than it loses" on a scale a
draw-heavy ladder can be tuned on. The baseline is over 60% draws, so a
wins-only band would mostly measure which colour moved first.

**The procedure.** Run each pair with `-refdepth` at the *stronger level's own
depth*, then adjust `Window` (or, at the top rung, `master`'s `Depth` — it has no
window to narrow) and re-run. Every level but the two ends sits in two pairs, so
a window change helps the rung above it and hurts the one below; that is why the
middle rungs are the hard ones.

**The harness.**

```
go run testdata/selfplay/main.go -pairs beginner/easy -refdepth 4
go run testdata/selfplay/main.go -pairs easy/medium   -refdepth 6
go run testdata/selfplay/main.go -pairs medium/hard   -refdepth 8
go run testdata/selfplay/main.go -pairs hard/master   -refdepth 8
```

Use `-seed 1`; that seed plus the committed table is the whole input, so the
same command reproduces the same numbers exactly. Measured eight-game runs at
that seed, `-blunder=false`: `beginner/easy` ~3 minutes, `easy/medium` ~12,
`medium/hard` ~16, `hard/master` ~15. The blunder pass roughly doubles the lower
pairs and adds about half again to the top one (22 minutes), so a full pass with
the default settings is around an hour and a half. A full ladder pass is a
nightly job, not a check.

**What is committed.** The harness lives under `testdata/`, which the Go
toolchain ignores — so it is invisible to `go build ./...`, `go vet ./...` and
`go test ./...`. Nothing that takes hours belongs in CI. The one thing CI checks
about the ladder is that the low levels are ordered (`TestLadderOrdersTheLowLevels`).

**The honest state of the record.** `design.md` in the change
`add-human-like-difficulty` is where the measurements, the band, and the
shortfalls live. Read it before trusting a number here. As of 2026-09-29 it
records that two middle rungs rest on a single game each, that `beginner/easy`
has never been reproduced by a second process, and that calibration measures
**bookless** play — the harness cannot depend on a network service, so with the
book on, the opening is stronger than the measurements describe.

## Tests

```
go test ./...                 # everything; seconds
node testdata/i18n_test.js    # locale tables: key sets, placeholders, markup
```

- `internal/game` — rules, the decision path, the level table, the window
  arithmetic, and reproducibility under an injected seed. `game.New` takes a
  seeded random source as an option (`game.WithRand`) precisely so these tests
  can be deterministic; a package-level `math/rand` would make them flaky.
- `internal/httpapi` — request/response shapes and the error table.
- `internal/xiangqi` — FEN, perft, rules, and the upstream engine behaviours a
  server cannot afford to get wrong.
- `main_test.go` — static file serving: cache headers, and that `index.html`'s
  asset version tokens all agree.

**The asset version token.** Every local asset referenced by `index.html`
carries the same `?v=N`. It exists because a cache keyed by URL is only as fresh
as the URL: bumping the token renames every asset at once, so no host can answer
from a stale copy. Bump both the stylesheet and the scripts together when you
change them — `TestIndexAssetsAreVersioned` fails if they drift apart. This is
belt-and-braces; the server also sends `Cache-Control: no-store` for embedded
files (`-dev` serves from disk instead, where `Last-Modified` lets a reload
revalidate cheaply).

## HTTP API

Nine endpoints. `sq` and the move fields are square indices, 0-89.

| Method | Path | Body | Returns |
| --- | --- | --- | --- |
| GET | `/api/state` | — | `State` |
| GET | `/api/legal?sq=N` | — | array of legal destination squares |
| GET | `/api/analysis` | — | the book's view of the position |
| POST | `/api/move` | `{"from":N,"to":N}` | `State` |
| POST | `/api/ai-move` | — | `State`, after the computer moves |
| POST | `/api/new` | `{"playerSide"?, "level"?, "useBook"?}` | `State` |
| POST | `/api/undo` | — | `State` |
| POST | `/api/hint` | — | `AIDecision` |
| POST | `/api/config` | `{"level"?, "useBook"?}` | `State` |

`/api/new`'s fields are all optional and all pointers server-side, so a partial
body keeps the default for what it omits rather than taking Go's zero value —
otherwise `{"level":"hard"}` would silently switch the opening book off.
`/api/undo` unwinds until it is the player's turn again, so one press takes back
your move *and* the computer's answer.

**State** carries `fen`, `board`, `sideToMove`, `inCheck`, `status`, `over`,
`winner`, `draw`, `reason`, `lastMove`, `history`, `playerSide`, `yourTurn`,
`canUndo`, `level`, `useBook`, and `ai` (the computer's last decision).

**AIDecision** carries `move` and `source` (`book` or `search`), then
`depth`, `score`, `nodes`, `timeMs`, `pv`, and `fallback`. `fallback` means no
search produced a move and a legal one was played to keep the game going; a
client should not present such a move as a judgement.

**Errors** use one envelope:

```json
{"error": {"code": "not_your_turn", "message": "..."}}
```

`code` is stable and language-neutral — a client renders its own text from it,
and must not display `message`, which is English and meant for logs and `curl`.

| Status | Codes |
| --- | --- |
| 400 | `bad_move`, `bad_square`, `bad_level`, `bad_player_side`, `bad_body` |
| 409 | `not_your_turn`, `not_ai_turn`, `game_over`, `no_undo`, `ai_busy`, `stale` |
| 503 | `offline` |
| 500 | `no_move`, `internal` |

`ai_busy` is returned rather than queued when a second computer turn arrives
while one is thinking. `stale` means the game moved on — a New game or an Undo —
while the computer was deciding, so the decision was dropped instead of played
into a game it no longer belongs to. Both statuses and codes come from the
single `apiErrors` table in `internal/httpapi`, so a sentinel cannot acquire one
without the other.

## Gotchas

- **`-dev` serves `web/` from disk.** Without it the interface is the copy
  embedded at build time, so editing `web/` and reloading changes nothing.
- **`testdata/` is invisible to the Go toolchain** by design. That is what keeps
  the harness and its hours-long run out of `go test ./...`.
- **`-level` is validated against the level table**, not a hardcoded list;
  `LevelNames()` is the single source for the flag's help and for the harness's
  pair list.
- **The write deadline is deliberately disabled** (`WriteTimeout: 0`) because a
  deep search legitimately takes tens of seconds. Do not "fix" it.
