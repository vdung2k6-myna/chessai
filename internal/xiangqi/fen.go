package xiangqi

import (
	"fmt"
	"strings"

	"github.com/hmgle/godogpaw/engine"
)

// pieceToFEN maps engine piece codes to FEN characters. Uppercase is red, which
// the engine calls WHITE. The letters are the conventional Xiangqi FEN ones:
// C=cannon, N=knight, B=bishop/elephant, A=advisor, K=king, P=pawn.
var pieceToFEN = [engine.PIECE_NB]byte{
	engine.NO_PIECE:  ' ',
	engine.W_ROOK:    'R',
	engine.W_ADVISOR: 'A',
	engine.W_CANNON:  'C',
	engine.W_PAWN:    'P',
	engine.W_KNIGHT:  'N',
	engine.W_BISHOP:  'B',
	engine.W_KING:    'K',
	engine.B_ROOK:    'r',
	engine.B_ADVISOR: 'a',
	engine.B_CANNON:  'c',
	engine.B_PAWN:    'p',
	engine.B_KNIGHT:  'n',
	engine.B_BISHOP:  'b',
	engine.B_KING:    'k',
}

// fenPieces is the set of characters allowed in the board field.
const fenPieces = "RNBAKCP" + "rnbakcp"

// EncodeFEN renders a position as FEN.
//
// godogpaw has no FEN encoder of its own — PositionNG.String() returns an ASCII
// diagram — but the chessdb.cn API is built entirely on FEN, so we need one.
// The board and side-to-move fields are what chessdb reads; the trailing
// counters are filled in from the position's own state so a FEN survives a
// round trip through Set.
func EncodeFEN(pos *engine.PositionNG) string {
	var sb strings.Builder
	for r := engine.RANK_9; r >= engine.RANK_0; r-- {
		gap := 0
		for f := engine.FILE_A; f <= engine.FILE_I; f++ {
			pc := pos.Board[engine.MakeSquareNG(f, r)]
			if pc == engine.NO_PIECE {
				gap++
				continue
			}
			if gap > 0 {
				sb.WriteByte(byte('0' + gap))
				gap = 0
			}
			sb.WriteByte(pieceToFEN[pc])
		}
		if gap > 0 {
			sb.WriteByte(byte('0' + gap))
		}
		if r > engine.RANK_0 {
			sb.WriteByte('/')
		}
	}

	side := "w"
	if pos.SideToMove == engine.BLACK {
		side = "b"
	}

	// GamePly counts half-moves from zero; FEN's fullmove number is 1-based and
	// counts a move as a red+black pair.
	rule60 := 0
	if top := pos.St.Top(); top != nil {
		rule60 = top.Rule60
	}
	fullmove := pos.GamePly/2 + 1
	if fullmove < 1 {
		fullmove = 1
	}

	fmt.Fprintf(&sb, " %s - - %d %d", side, rule60, fullmove)
	return sb.String()
}

// ValidateFEN checks a FEN string is sound before it is handed to
// engine.PositionNG.Set.
//
// This guard matters more than its size suggests. Set has two log.Fatalf paths
// — one for a FEN with fewer than two fields, and one when the engine's own
// PosIsOk consistency check fails. log.Fatalf calls os.Exit, which recover
// cannot catch, so a malformed FEN does not return an error: it terminates the
// process. Anything reaching Set must therefore be checked first.
//
// The semantic checks below mirror the cheap subset of PosIsOk that a
// hand-written or corrupted FEN is most likely to trip. Not covered: whether
// the side not to move has been left in check, which needs full attack
// generation to detect.
func ValidateFEN(fen string) error {
	fields := strings.Fields(fen)
	if len(fields) < 2 {
		return fmt.Errorf("fen needs at least a board and a side-to-move field, got %q", fen)
	}

	board, side := fields[0], fields[1]
	if side != "w" && side != "b" {
		return fmt.Errorf("fen side-to-move must be %q or %q, got %q", "w", "b", side)
	}

	ranks := strings.Split(board, "/")
	if len(ranks) != 10 {
		return fmt.Errorf("fen board must have 10 ranks, got %d", len(ranks))
	}

	// grid[0] is rank 9, the top rank, matching the order ranks appear in a FEN.
	var grid [10][9]byte
	for i, rank := range ranks {
		file := 0
		for _, c := range rank {
			switch {
			case c >= '1' && c <= '9':
				file += int(c - '0')
			case strings.ContainsRune(fenPieces, c):
				if file >= 9 {
					return fmt.Errorf("fen rank %d describes more than 9 files", i)
				}
				grid[9-i][file] = byte(c)
				file++
			default:
				return fmt.Errorf("fen rank %d has invalid character %q", i, string(c))
			}
		}
		if file != 9 {
			return fmt.Errorf("fen rank %d describes %d files, want 9", i, file)
		}
	}

	var counts [256]int
	red, black := 0, 0
	for r := range grid {
		for _, c := range grid[r] {
			if c == 0 {
				continue
			}
			counts[c]++
			if c >= 'A' && c <= 'Z' {
				red++
			} else {
				black++
			}
		}
	}

	if counts['K'] != 1 || counts['k'] != 1 {
		return fmt.Errorf("fen must have exactly one red king (K) and one black king (k), got %d and %d",
			counts['K'], counts['k'])
	}
	if counts['P'] > 5 || counts['p'] > 5 {
		return fmt.Errorf("fen has more than 5 pawns for one side (P=%d p=%d)", counts['P'], counts['p'])
	}
	if red > 16 || black > 16 {
		return fmt.Errorf("fen has more than 16 pieces for one side (red=%d black=%d)", red, black)
	}

	// The kings may not face each other on an open file — the "flying general"
	// rule. The engine rejects this as a PosIsOk failure, which would kill the
	// process rather than return an error.
	wkFile, wkRank := findKing(grid, 'K')
	bkFile, bkRank := findKing(grid, 'k')
	if wkFile == bkFile {
		blocked := false
		for r := min(wkRank, bkRank) + 1; r < max(wkRank, bkRank); r++ {
			if grid[r][wkFile] != 0 {
				blocked = true
				break
			}
		}
		if !blocked {
			return fmt.Errorf("fen leaves the two kings facing on file %c with nothing between them", 'a'+wkFile)
		}
	}
	return nil
}

// findKing locates a king in the grid, whose indices are [rank][file].
func findKing(grid [10][9]byte, king byte) (file, rank int) {
	for r := range grid {
		for f, c := range grid[r] {
			if c == king {
				return f, r
			}
		}
	}
	return 0, 0
}
