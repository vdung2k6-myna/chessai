// board.js — Canvas Xiangqi board renderer.
//
// Adapted from godogpaw's demo renderer. The geometry is the engine's own square
// encoding: square = file + rank * 9, with rank 0 the red back rank.
//
// Two things changed from upstream: the canvas is scaled for device pixel ratio
// so it is sharp on a high-DPI display, and the geometry is recomputed on resize
// rather than baked in as constants.

const BOARD_COLS = 9;
const BOARD_ROWS = 10;

// Piece codes, matching the engine's encoding.
const NO_PIECE = 0;
const W_ROOK = 1, W_ADVISOR = 2, W_CANNON = 3, W_PAWN = 4, W_KNIGHT = 5, W_BISHOP = 6, W_KING = 7;
const B_ROOK = 9, B_ADVISOR = 10, B_CANNON = 11, B_PAWN = 12, B_KNIGHT = 13, B_BISHOP = 14, B_KING = 15;

// Red uses the traditional characters; black uses the simplified ones, which is
// how a real set is printed.
const PIECE_CHARS = {
  [W_ROOK]: '俥', [W_ADVISOR]: '仕', [W_CANNON]: '砲', [W_PAWN]: '兵',
  [W_KNIGHT]: '傌', [W_BISHOP]: '相', [W_KING]: '帅',
  [B_ROOK]: '车', [B_ADVISOR]: '士', [B_CANNON]: '炮', [B_PAWN]: '卒',
  [B_KNIGHT]: '马', [B_BISHOP]: '象', [B_KING]: '将',
};

function isRedPiece(pc) { return pc >= W_ROOK && pc <= W_KING; }

class BoardRenderer {
  constructor(canvas) {
    this.canvas = canvas;
    this.ctx = canvas.getContext('2d');
    this.flipped = false;
    this.selectedSq = -1;
    this.legalTargets = [];
    this.hintMove = null;
    this.lastFrom = -1;
    this.lastTo = -1;
    this.boardData = new Array(BOARD_COLS * BOARD_ROWS).fill(NO_PIECE);
    this.cell = 56;
    this.pad = 28;
    this.resize();
  }

  // resize measures the canvas's CSS box and matches the backing store to the
  // device pixel ratio, so lines and glyphs stay crisp on a retina display.
  resize() {
    const rect = this.canvas.getBoundingClientRect();
    const available = rect.width || this.canvas.parentElement?.clientWidth || 504;

    // 2*pad + 8*cell across and 2*pad + 9*cell down, with pad = cell/2, gives a
    // ratio of 9:10 between width and height.
    this.cell = Math.max(28, Math.floor(available / (BOARD_COLS + 1)));
    this.pad = this.cell / 2;
    this.radius = this.cell * 0.44;

    const cssW = this.pad * 2 + (BOARD_COLS - 1) * this.cell;
    const cssH = this.pad * 2 + (BOARD_ROWS - 1) * this.cell;
    const dpr = window.devicePixelRatio || 1;

    this.canvas.style.width = `${cssW}px`;
    this.canvas.style.height = `${cssH}px`;
    this.canvas.width = Math.round(cssW * dpr);
    this.canvas.height = Math.round(cssH * dpr);
    // Draw in CSS pixels; the transform handles the DPR scaling.
    this.ctx.setTransform(dpr, 0, 0, dpr, 0, 0);

    this.draw();
  }

  sqToCanvas(sq) {
    const file = sq % BOARD_COLS;
    const rank = Math.floor(sq / BOARD_COLS);
    if (this.flipped) {
      // A 180° rotation, which is how a board is drawn from black's side.
      return {
        x: this.pad + (BOARD_COLS - 1 - file) * this.cell,
        y: this.pad + rank * this.cell,
      };
    }
    return {
      x: this.pad + file * this.cell,
      y: this.pad + (BOARD_ROWS - 1 - rank) * this.cell,
    };
  }

  // canvasToSq maps a point in CSS pixels to the nearest square, or -1 when the
  // click is not close enough to any intersection to count.
  canvasToSq(px, py) {
    const rect = this.canvas.getBoundingClientRect();
    let best = -1;
    let bestDist = Infinity;
    for (let sq = 0; sq < BOARD_COLS * BOARD_ROWS; sq++) {
      const { x, y } = this.sqToCanvas(sq);
      const dx = px - rect.left - x;
      const dy = py - rect.top - y;
      const dist = Math.hypot(dx, dy);
      if (dist < this.cell * 0.55 && dist < bestDist) {
        bestDist = dist;
        best = sq;
      }
    }
    return best;
  }

  draw() {
    const ctx = this.ctx;
    const w = this.pad * 2 + (BOARD_COLS - 1) * this.cell;
    const h = this.pad * 2 + (BOARD_ROWS - 1) * this.cell;

    ctx.fillStyle = '#e8c97a';
    ctx.fillRect(0, 0, w, h);

    this.drawGrid(ctx);
    this.drawPalaceDiagonals(ctx);
    this.drawRiver(ctx);

    if (this.lastFrom >= 0 && this.lastTo >= 0) {
      this.drawSquareHighlight(ctx, this.lastFrom, 'rgba(255, 200, 0, 0.45)');
      this.drawSquareHighlight(ctx, this.lastTo, 'rgba(255, 200, 0, 0.45)');
    }
    if (this.selectedSq >= 0) {
      this.drawSquareHighlight(ctx, this.selectedSq, 'rgba(0, 150, 0, 0.35)');
    }
    if (this.hintMove) {
      this.drawSquareHighlight(ctx, this.hintMove.from, 'rgba(30, 110, 220, 0.35)');
      this.drawSquareHighlight(ctx, this.hintMove.to, 'rgba(30, 110, 220, 0.35)');
    }

    ctx.strokeStyle = '#4a3520';
    for (const tgt of this.legalTargets) {
      const { x, y } = this.sqToCanvas(tgt);
      ctx.beginPath();
      ctx.arc(x, y, this.cell * 0.14, 0, Math.PI * 2);
      if (this.boardData[tgt] !== NO_PIECE) {
        // A capture target is marked with a ring rather than a dot, so the
        // piece it would take stays readable.
        ctx.lineWidth = 3;
        ctx.strokeStyle = 'rgba(0, 150, 0, 0.75)';
        ctx.stroke();
        ctx.strokeStyle = '#4a3520';
      } else {
        ctx.fillStyle = 'rgba(0, 150, 0, 0.5)';
        ctx.fill();
      }
    }

    for (let sq = 0; sq < this.boardData.length; sq++) {
      if (this.boardData[sq] !== NO_PIECE) this.drawPiece(ctx, sq, this.boardData[sq]);
    }
  }

  drawGrid(ctx) {
    ctx.strokeStyle = '#4a3520';
    ctx.lineWidth = 1;

    const left = this.pad;
    const right = this.pad + (BOARD_COLS - 1) * this.cell;
    const top = this.pad;
    const bottom = this.pad + (BOARD_ROWS - 1) * this.cell;

    for (let r = 0; r < BOARD_ROWS; r++) {
      const y = this.pad + r * this.cell;
      ctx.beginPath();
      ctx.moveTo(left, y);
      ctx.lineTo(right, y);
      ctx.stroke();
    }

    for (let c = 0; c < BOARD_COLS; c++) {
      const x = this.pad + c * this.cell;
      ctx.beginPath();
      if (c === 0 || c === BOARD_COLS - 1) {
        // The outer files run the full height; the inner ones stop at the river.
        ctx.moveTo(x, top);
        ctx.lineTo(x, bottom);
      } else {
        ctx.moveTo(x, top);
        ctx.lineTo(x, this.pad + 4 * this.cell);
        ctx.moveTo(x, this.pad + 5 * this.cell);
        ctx.lineTo(x, bottom);
      }
      ctx.stroke();
    }
  }

  drawPalaceDiagonals(ctx) {
    // The palace spans files d,e,f on ranks 0-2 (red) and 7-9 (black).
    ctx.strokeStyle = '#4a3520';
    ctx.lineWidth = 1;
    for (const [lo, hi] of [[0, 2], [7, 9]]) {
      const corners = [
        [3 + lo * 9, 5 + hi * 9],
        [5 + lo * 9, 3 + hi * 9],
      ];
      for (const [a, b] of corners) {
        const p1 = this.sqToCanvas(a);
        const p2 = this.sqToCanvas(b);
        ctx.beginPath();
        ctx.moveTo(p1.x, p1.y);
        ctx.lineTo(p2.x, p2.y);
        ctx.stroke();
      }
    }
  }

  drawRiver(ctx) {
    const mid = this.pad + 4.5 * this.cell;
    ctx.font = `${Math.round(this.cell * 0.36)}px "KaiTi", "STKaiti", "SimSun", serif`;
    ctx.fillStyle = '#4a3520';
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    // Flipped, the two halves swap sides along with everything else on the board.
    const left = this.flipped ? '汉 界' : '楚 河';
    const right = this.flipped ? '楚 河' : '汉 界';
    ctx.fillText(left, this.pad + 2 * this.cell, mid);
    ctx.fillText(right, this.pad + 6 * this.cell, mid);
  }

  drawSquareHighlight(ctx, sq, color) {
    const { x, y } = this.sqToCanvas(sq);
    ctx.fillStyle = color;
    const half = this.cell / 2;
    ctx.fillRect(x - half, y - half, this.cell, this.cell);
  }

  drawPiece(ctx, sq, pc) {
    const { x, y } = this.sqToCanvas(sq);
    const red = isRedPiece(pc);
    const ink = red ? '#c02020' : '#1a1a1a';

    ctx.beginPath();
    ctx.arc(x + 1, y + 2, this.radius, 0, Math.PI * 2);
    ctx.fillStyle = 'rgba(0,0,0,0.18)';
    ctx.fill();

    ctx.beginPath();
    ctx.arc(x, y, this.radius, 0, Math.PI * 2);
    ctx.fillStyle = '#f7ead0';
    ctx.fill();
    ctx.lineWidth = 2;
    ctx.strokeStyle = ink;
    ctx.stroke();

    ctx.beginPath();
    ctx.arc(x, y, this.radius * 0.84, 0, Math.PI * 2);
    ctx.lineWidth = 1;
    ctx.stroke();

    ctx.font = `bold ${Math.round(this.radius * 0.95)}px "KaiTi", "STKaiti", "SimSun", serif`;
    ctx.fillStyle = ink;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(PIECE_CHARS[pc] || '?', x, y + 1);
  }

  update(board, lastFrom, lastTo) {
    this.boardData = board && board.length === BOARD_COLS * BOARD_ROWS
      ? board
      : new Array(BOARD_COLS * BOARD_ROWS).fill(NO_PIECE);
    this.lastFrom = typeof lastFrom === 'number' ? lastFrom : -1;
    this.lastTo = typeof lastTo === 'number' ? lastTo : -1;
    // A hint belongs to one position only.
    this.hintMove = null;
    this.draw();
  }

  setSelected(sq, targets) {
    this.selectedSq = sq;
    this.legalTargets = targets || [];
    this.draw();
  }

  setHint(from, to) {
    this.hintMove = from >= 0 && to >= 0 ? { from, to } : null;
    this.draw();
  }

  clearSelection() {
    this.selectedSq = -1;
    this.legalTargets = [];
    this.draw();
  }
}

// ucciMoveToSquares converts a move like "b0c2" into the two square indices, or
// null when it is not a move.
//
// Both coordinates are checked, not just the total: a letter past 'i' would
// otherwise land on a real square of the wrong rank ('z0' would come out as the
// same square as 'd0' plus a rank), and silently highlight the wrong piece.
function ucciMoveToSquares(move) {
  if (!move || move.length !== 4) return null;
  const square = (fileChar, rankChar) => {
    const file = fileChar.charCodeAt(0) - 'a'.charCodeAt(0);
    const rank = rankChar.charCodeAt(0) - '0'.charCodeAt(0);
    if (file < 0 || file >= BOARD_COLS || rank < 0 || rank >= BOARD_ROWS) return -1;
    return file + rank * BOARD_COLS;
  };
  const from = square(move[0], move[1]);
  const to = square(move[2], move[3]);
  if (from < 0 || to < 0) return null;
  return { from, to };
}
