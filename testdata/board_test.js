// Tests the pure helpers in web/board.js, which cannot be reached from Go tests
// and are otherwise only exercised by clicking in a browser.
//
// Run with:  node testdata/board_test.js
//
// It lives under testdata/ so that neither `go build` nor //go:embed all:web
// picks it up: this is a test program, not something to ship or serve.

const fs = require('fs');
const path = require('path');
const assert = require('assert');

const src = fs.readFileSync(path.join(__dirname, '..', 'web', 'board.js'), 'utf8');

// board.js expects a browser. Stub just enough of one to load the file and
// reach the helpers, which touch no DOM at call time.
const ctx = new Proxy({}, { get: () => () => {} });
global.document = {
  getElementById: () => ({ getContext: () => ctx, addEventListener() {}, style: {} }),
  createElement: () => ({ style: {}, getContext: () => ctx }),
};
global.window = { devicePixelRatio: 2, addEventListener() {} };
global.getComputedStyle = () => ({ getPropertyValue: () => '400' });

const mod = { exports: {} };
new Function('module', 'exports', src + '\nmodule.exports = { BoardRenderer, ucciMoveToSquares };')(mod, mod.exports);
const { ucciMoveToSquares } = mod.exports;

// Square encoding is file + rank * 9, which is the engine's own.
const cases = [
  ['c3c4', 2 + 3 * 9, 2 + 4 * 9], // red pawn forward
  ['a0a1', 0, 9],                 // red rook up the edge file
  ['b0c2', 1, 2 + 2 * 9],         // knight out
  ['h2e2', 7 + 2 * 9, 4 + 2 * 9], // cannon across to the centre file
  ['e9e8', 4 + 9 * 9, 4 + 8 * 9], // black king, the far corner of the board
];

for (const [move, from, to] of cases) {
  assert.deepStrictEqual(
    ucciMoveToSquares(move), { from, to }, `${move} should map to ${from}->${to}`);
}

// Anything malformed must be rejected rather than silently mapped onto a real
// square. "z0z1" is the interesting one: the file letter is past 'i', so a
// bounds check on the final square index alone lets it through as a valid
// square of a different rank.
const rejected = ['', 'x', 'c3c', 'c3c44', 'z0z1', 'c0c9x', 'c3-c4'];
for (const junk of rejected) {
  assert.strictEqual(ucciMoveToSquares(junk), null, `${JSON.stringify(junk)} should be rejected`);
}

console.log(`board.js: ${cases.length + rejected.length} checks passed`);
