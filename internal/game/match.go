package game

import (
	"context"
	"errors"
	"log/slog"

	"chessai/internal/cloud"
	"chessai/internal/xiangqi"
)

// Playing the ladder against itself.
//
// A Game is a human against the computer: one side is the player, the other is
// the level, and the two never swap. That is the wrong shape for a caller that
// needs the other arrangement — the self-play harness in testdata/selfplay,
// which plays level N+1 against level N — because in a match neither side is a
// human and both are levels.
//
// What such a caller needs from this package is only what makes a level strong:
// the book's candidate list, the search, and the window. So that is what is
// exported, as one move at a time rather than as parameters. A level is played,
// never described — the numbers behind it stay inside the package, which is the
// same reason no client ever sees them.

// LevelMove plays one move at the named level for the side that is to move, and
// applies it to pos.
//
// It is the whole policy — the opening book, the search, and the level's window
// — applied once, with a Game built around the position and thrown away. Two
// callers taking turns with it are a match between levels:
//
//	for !pos.Result().Over {
//		if _, err := game.LevelMove(ctx, pos, level, nil, nil, opts...); err != nil {
//			return err
//		}
//	}
//
// A nil client means the book is off, which is what makes a self-play run
// independent of a third party. opts are Game options; a caller that needs
// reproducible selection passes WithRand.
func LevelMove(ctx context.Context, pos *xiangqi.Position, levelName string, c cloud.Client, log *slog.Logger, opts ...Option) (*AIDecision, error) {
	if pos == nil {
		return nil, errors.New("a level move needs a position")
	}
	// The computer takes the side to move, which is what makes this a move
	// rather than a reply to one.
	player := xiangqi.Black
	if pos.SideToMove() == xiangqi.Black {
		player = xiangqi.Red
	}
	g, err := newFrom(pos, Config{
		PlayerSide: player,
		Level:      levelName,
		UseBook:    c != nil,
	}, c, log, opts...)
	if err != nil {
		return nil, err
	}
	return g.AIMove(ctx)
}
