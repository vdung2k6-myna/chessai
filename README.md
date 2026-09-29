# Xiangqi

Play Chinese chess in the browser against the **godogpaw** engine, with
[chessdb.cn](https://www.chessdb.cn/cloudbook_api_en.html) as its opening book.

One Go binary serves both the API and the interface: the web client is embedded
in the executable, so there is nothing to build, bundle or install on the client
side.

## Requirements

- Go 1.23.4 or newer
- A network connection for the opening book. `-offline` runs without one.

## Running it

```
go run .
```

Then open <http://127.0.0.1:8080>. The server logs where it is listening:

```
level=info msg=listening url=http://127.0.0.1:8080 offline=false level=medium
```

## How to play

Click a piece, then click where you want it to go. A square only accepts the
click if the move is legal. The line under the board says whose turn it is, or
how the game ended.

| Button | What it does |
| --- | --- |
| **New game** | Starts over, using the settings as they stand now. |
| **Undo** | Takes back your move and the computer's answer together, so it is your turn again. |
| **Hint** | Suggests a move for you, without playing it. Hints are always given at a fixed, strong search depth — advice is good advice whatever level you are playing. |

### Settings

| Setting | What it does |
| --- | --- |
| **Language** | Tiếng Việt, English or 中文. |
| **Play as** | Red (first) or Black (second). Changing this starts a new game, because the side you play is part of what a game is. |
| **Level** | How strong the computer plays. Takes effect on the computer's next move — your game carries on. |
| **Use the opening book** | Whether the computer may use chessdb.cn's book. Takes effect immediately. |

Your language and level are remembered in the browser, so a reload keeps them.
The game itself lives on the server, so a reload keeps that too.

### The analysis panel

Beside the board:

- **Cloud best / Cloud eval / Cloud depth** — what the opening book says about
  the current position, and how deeply it looked.
- **Top book moves** — the book's ranked moves, with its scores and win rates.
  The computer chooses among these in the opening.
- **Principal variation** — the line the computer expects to follow from its
  chosen move.

With the book unavailable or switched off, this panel has nothing to show.

## The five levels

From weakest to strongest: `beginner`, `easy`, `medium`, `hard`, `master`.
`medium` is the default.

None of them is a search depth, and none is named after one. Every level but the
strongest plays one of the moves it judges to be *nearly* as good as the best
move it can find; how much worse a move may be and still get played is what
separates the rungs, and it widens as you go down. Three things follow from that,
and they are the point of the design:

- **`master` always plays the best move it finds.** It is the only level that
  never settles.
- **A weak level is a weaker player, not a blind one.** In a position with only
  one move that avoids losing material, every level finds it — so a low level
  does not hang a piece from a position where the recapture is invisible.
- **Every level but `master` can play differently in the same position** on
  different days. Depth-limited play was identical every time, so a trap that
  worked once worked forever.

## Flags

| Flag | Default | What it does |
| --- | --- | --- |
| `-addr` | `127.0.0.1:8080` | Address to listen on. Loopback only by default, so the server is not reachable from other machines unless you ask for it. |
| `-level` | `medium` | Level to start at. An unknown name exits with the list to choose from. |
| `-offline` | off | Do not use the chessdb.cn opening book at all. The computer searches from its first move, and the analysis panel reports the book as unavailable. |
| `-dev` | off | Serve `web/` from disk instead of the embedded copy, so edits to the interface appear on reload. |
| `-v` | off | Debug logging. |

## Things worth knowing

- **The opening book is a third party.** It is chessdb.cn over the network: with
  no connection the book is simply absent, and the computer searches from move
  one instead. An entry that does not fit the current position — a stale or
  misindexed one — is ignored rather than trusted, and the engine searches.
  Nothing about a book failure stops you playing.
- **Difficulty was calibrated without the book.** The levels were tuned by
  playing them against each other, and that harness cannot depend on a network
  service, so the measured strengths describe bookless play. With the book on,
  the opening is stronger and more human than the measurements. See
  [docs/development.md](docs/development.md#calibration) for how the levels were
  tuned and what the measurements do and do not establish.
- **A computer move takes as long as it takes.** There is no time control and no
  clock: the computer searches and moves when it is done — about a second in the
  middlegame. A weaker level is not a faster one: every level below `master` pays
  for a search of *every move it is considering*, so dozens of shallow searches
  can cost about what the strongest level's single deep one does. Only the two
  lowest levels are markedly quicker. The board is frozen while it thinks, and a
  turn that arrives while it is thinking is refused rather than queued.
- **One game at a time.** The server holds a single game, not a lobby. Two
  browser tabs are two views of the same board.
- **The interface follows your browser's language** when the browser asks for one
  of the three shipped locales, and opens in Vietnamese otherwise. Choosing a
  language overrides both and is remembered.

## Developers

See [docs/development.md](docs/development.md) for the package layout, the level
table and how to retune it, the self-play harness, the tests, and the HTTP API.
