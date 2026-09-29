// app.js — drives the board from the server's HTTP API.
//
// The server owns the game; this file owns nothing but what the player has
// clicked. Every change goes to the server and comes back as a fresh state,
// which keeps the two from drifting apart.
//
// No text here is written in English. Every word comes from the tables in
// web/locales/, looked up with t() (text) or tHTML() (markup), and the server's
// failures arrive as codes that are looked up the same way.

const board = new BoardRenderer(document.getElementById('board'));
const els = {
  status: document.getElementById('status'),
  newGame: document.getElementById('new-game'),
  undo: document.getElementById('undo'),
  hint: document.getElementById('hint'),
  locale: document.getElementById('locale'),
  side: document.getElementById('side'),
  level: document.getElementById('level'),
  useBook: document.getElementById('use-book'),
  aiMove: document.getElementById('ai-move'),
  cloudBest: document.getElementById('cloud-best'),
  cloudEval: document.getElementById('cloud-eval'),
  cloudDepth: document.getElementById('cloud-depth'),
  pov: document.getElementById('pov'),
  moves: document.querySelector('#moves tbody'),
  analysisNote: document.getElementById('analysis-note'),
  pv: document.getElementById('pv'),
};

const SIDE_RED = 0;
const SIDE_BLACK = 1;

// A dash stands in for a value that is not known, in every locale.
const NONE = '—';

// state is the server's last answer about the game.
let state = null;
// analysis is the cloudbook's last answer, kept so that changing language can
// redraw the sidebar without asking the server again; analysisFailure is why
// there is no answer, when there is none.
let analysis = null;
let analysisFailure = null;
// busy guards against overlapping requests: the player clicking twice while the
// computer is thinking would otherwise queue a second turn.
let busy = false;

// Failure is a request that did not succeed.
//
// It carries the server's code — which is what the interface renders from — and
// keeps the server's English message for the console. The message is never shown
// to the player: it is English, and the player may not read English.
class Failure extends Error {
  constructor(code, message) {
    super(message || code);
    this.name = 'Failure';
    this.code = code;
  }
}

async function api(path, options) {
  let res;
  try {
    res = await fetch(path, {
      headers: { 'Content-Type': 'application/json' },
      ...options,
    });
  } catch (err) {
    // The request never reached the server, so there is no code to read. It is
    // the one failure the interface knows about without being told.
    throw new Failure('network', String(err));
  }

  const text = await res.text();
  let body = null;
  if (text) {
    try { body = JSON.parse(text); } catch { body = null; }
  }

  if (!res.ok) {
    const failure = body && body.error;
    if (failure && typeof failure === 'object' && failure.code) {
      throw new Failure(failure.code, failure.message);
    }
    // A failure with no envelope at all did not come from this API's handlers.
    throw new Failure('internal', `${res.status} ${res.statusText}`);
  }
  return body;
}

async function refresh() {
  state = await api('/api/state');
  render();
}

function render() {
  if (!state) return;

  board.flipped = state.playerSide === SIDE_BLACK;
  const last = ucciMoveToSquares(state.lastMove);
  board.update(state.board, last ? last.from : -1, last ? last.to : -1);

  renderStatus();
  renderAIMove();

  els.undo.disabled = busy || !state.canUndo;
  els.newGame.disabled = busy;
  els.hint.disabled = busy || !state.yourTurn;
  // The side picker is live only while there is no game to lose: choosing a side
  // starts that game at once, which would otherwise discard a position in
  // progress and leave the computer on the move.
  els.side.disabled = busy || state.history.length > 0;
}

// sideName names a player. These are the words for the two sides, not the
// characters drawn on the pieces.
function sideName(side) {
  return t(side === SIDE_RED ? 'side.red' : 'side.black');
}

// reasonText turns the server's reason code into a phrase, so the outcome reads
// as one sentence in the player's own language.
function reasonText() {
  const key = `reason.${state.reason}`;
  return t(hasKey(key) ? key : 'reason.unknown');
}

// failureText renders a failure for the player from its code. A code the
// interface does not recognize — one added to the server since this build, or a
// proxy answering in its place — gets the generic wording rather than being
// shown raw.
function failureText(err) {
  const key = `error.${err && err.code ? err.code : 'unknown'}`;
  return t(hasKey(key) ? key : 'error.unknown');
}

function renderStatus() {
  let text;
  let cls = '';
  if (state.over) {
    cls = 'over';
    if (state.draw) {
      text = t('status.draw', { reason: reasonText() });
    } else {
      text = t(state.winner === state.playerSide ? 'status.winYou' : 'status.winComputer', {
        winner: sideName(state.winner),
        reason: reasonText(),
      });
    }
  } else if (busy) {
    cls = 'thinking';
    text = t('status.thinking');
  } else if (state.yourTurn) {
    cls = 'your-turn';
    text = t(state.inCheck ? 'status.yourTurnCheck' : 'status.yourTurn', {
      side: sideName(state.sideToMove),
    });
  } else {
    cls = 'thinking';
    text = t('status.computerTurn', { side: sideName(state.sideToMove) });
  }

  els.status.textContent = text;
  els.status.className = `status ${cls}`;
}

// badge is a small labelled tag beside the computer's move.
function badge(key, variant) {
  const span = document.createElement('span');
  span.className = `badge ${variant}`;
  span.textContent = t(key);
  return span;
}

// searchDetail describes a search the way a player reads it: the statistics that
// exist, joined by the locale's own separator.
function searchDetail(ai) {
  const bits = [];
  if (ai.depth) bits.push(t('stat.depth', { n: ai.depth }));
  if (typeof ai.score === 'number') bits.push(t('stat.score', { n: ai.score }));
  if (ai.nodes) bits.push(t('stat.nodes', { n: formatNumber(ai.nodes) }));
  if (ai.timeMs) bits.push(t('stat.time', { n: ai.timeMs }));
  return bits.join(t('list.separator'));
}

function renderAIMove() {
  const ai = state.ai;
  if (!ai) {
    els.aiMove.replaceChildren();
    return;
  }

  const parts = [];

  // The sentence is one template with the move escaped into it. This is the only
  // place the interface puts a server-supplied value into markup, and tHTML is
  // what makes it safe: the markup is the table's, the value is escaped.
  const played = document.createElement('span');
  played.innerHTML = tHTML('aiMove.played', { move: ai.move });
  parts.push(played);

  const fromBook = ai.source === 'book';
  parts.push(badge(fromBook ? 'badge.book' : 'badge.search', fromBook ? 'book' : 'search'));
  if (ai.fallback) parts.push(badge('badge.fallback', 'warn'));

  if (!fromBook) {
    const detail = searchDetail(ai);
    if (detail) parts.push(document.createTextNode(t('aiMove.detail', { stats: detail })));
  }

  els.aiMove.replaceChildren(...parts);
}

// povLabel says whose numbers the sidebar is showing.
//
// The FEN the analysis carries names the side those numbers belong to, so the
// sidebar can say so rather than leaving the reader to guess.
function povLabel(fen) {
  const side = fen ? fen.split(' ')[1] : '';
  return t(side === 'b' ? 'pov.black' : 'pov.red');
}

// renderAnalysis fills the sidebar from the cloudbook. It is deliberately
// separate from render(): the sidebar can be stale or missing without the game
// being wrong, so a failure here never blocks play.
//
// The fetch and the drawing are split so that a language change can redraw the
// panel from what is already in hand — the moment a player switches language is
// not the moment to depend on a server.
async function renderAnalysis() {
  try {
    analysis = await api('/api/analysis');
    analysisFailure = null;
  } catch (err) {
    analysis = null;
    analysisFailure = err;
  }
  drawAnalysis();
}

// drawAnalysis renders the last cloudbook answer in whatever locale is active.
function drawAnalysis() {
  const a = analysis;
  // Nothing has been asked for yet, so the panel keeps the dashes it was
  // authored with rather than claiming the position is not in the book.
  if (!a && !analysisFailure) return;

  if (analysisFailure) {
    // A failure is worth showing in the panel rather than only the status line:
    // it explains a panel that is empty. The server's own message still goes to
    // the console, never to the player.
    console.warn('cloudbook analysis failed:', analysisFailure.message);
    els.analysisNote.textContent = t('analysis.unavailable', { error: failureText(analysisFailure) });
    return;
  }

  if (!a.known) {
    els.cloudBest.textContent = NONE;
    els.cloudEval.textContent = NONE;
    els.cloudDepth.textContent = NONE;
    els.pov.textContent = '';
    els.moves.replaceChildren();
    els.pv.textContent = NONE;
    els.analysisNote.textContent = t('analysis.notInBook');
    return;
  }

  els.cloudBest.textContent = a.best || NONE;
  els.cloudEval.textContent = typeof a.score === 'number' ? a.score : NONE;
  els.cloudDepth.textContent = a.pvDepth ? t('analysis.plies', { n: a.pvDepth }) : NONE;
  els.pov.textContent = povLabel(a.fen);

  const rows = (a.moves || []).map((m) => {
    const tr = document.createElement('tr');
    for (const [text, cls] of [
      [m.move, 'move'],
      [typeof m.score === 'number' ? m.score : '', ''],
      [typeof m.winrate === 'number' ? m.winrate.toFixed(2) : '', ''],
      [m.note || '', ''],
    ]) {
      const td = document.createElement('td');
      td.textContent = text;
      if (cls) td.className = cls;
      tr.appendChild(td);
    }
    return tr;
  });
  els.moves.replaceChildren(...rows);

  els.pv.textContent = (a.pv && a.pv.length) ? a.pv.join(' ') : NONE;
  els.analysisNote.textContent = a.partial ? t('analysis.partial') : '';
}

async function withBusy(fn) {
  busy = true;
  render();
  try {
    await fn();
  } catch (err) {
    els.status.textContent = failureText(err);
    els.status.className = 'status over';
  } finally {
    busy = false;
    render();
  }
}

// playAI runs the computer's turn when it is its move.
async function playAI() {
  if (!state || state.over || state.yourTurn) return;
  await withBusy(async () => {
    await api('/api/ai-move', { method: 'POST' });
    await refresh();
  });
  renderAnalysis();
}

board.canvas.addEventListener('click', async (ev) => {
  if (!state || busy || state.over || !state.yourTurn) return;

  const sq = board.canvasToSq(ev.clientX, ev.clientY);
  if (sq < 0) {
    board.clearSelection();
    return;
  }

  // Clicking a legal target commits the move.
  if (board.selectedSq >= 0 && board.legalTargets.includes(sq)) {
    const from = board.selectedSq;
    board.clearSelection();
    await withBusy(async () => {
      await api('/api/move', {
        method: 'POST',
        body: JSON.stringify({ from, to: sq }),
      });
      await refresh();
    });
    renderAnalysis();
    await playAI();
    return;
  }

  // Otherwise select the piece that was clicked, if it is the player's own.
  if (state.board[sq] === 0) {
    board.clearSelection();
    return;
  }
  try {
    const targets = await api(`/api/legal?sq=${sq}`);
    if (targets && targets.length) {
      board.setSelected(sq, targets);
    } else {
      board.clearSelection();
    }
  } catch (err) {
    board.clearSelection();
    console.warn('legal moves failed:', err.message);
  }
});

// startGame begins a game from whatever the controls currently say, then lets
// the computer open if the opening move is not the player's.
//
// All three entry points — page load, the New game button, and choosing a side —
// mean the same thing: stop what is happening, take the settings as they now
// stand, and play from the start.
async function startGame() {
  await withBusy(async () => {
    await api('/api/new', {
      method: 'POST',
      body: JSON.stringify({
        playerSide: Number(els.side.value),
        level: els.level.value,
        useBook: els.useBook.checked,
      }),
    });
    await refresh();
  });
  renderAnalysis();
  await playAI();
}

els.newGame.addEventListener('click', startGame);

// Choosing a side starts the game that side describes, rather than waiting for
// the New game button to be pressed afterwards. Picking Black and then having to
// press a second button to make it mean anything is a step with no purpose, and
// the picker reads as a control that does something the moment it is used.
//
// This cannot cost anyone a position: the picker is disabled from the first move
// onwards, so it is live only while there is no game to lose.
els.side.addEventListener('change', startGame);

els.undo.addEventListener('click', async () => {
  await withBusy(async () => {
    await api('/api/undo', { method: 'POST' });
    await refresh();
  });
  renderAnalysis();
});

els.hint.addEventListener('click', async () => {
  try {
    const decision = await api('/api/hint', { method: 'POST' });
    const squares = ucciMoveToSquares(decision.move);
    if (squares) board.setHint(squares.from, squares.to);
    els.status.textContent = decision.source === 'book'
      ? t('hint.fromBook', { move: decision.move })
      : t('hint.fromSearch', { move: decision.move, depth: decision.depth });
  } catch (err) {
    els.status.textContent = failureText(err);
    els.status.className = 'status over';
  }
});

// LEVEL_STORAGE_KEY is where the player's level is remembered. The level is a
// choice about the game rather than a word the interface draws, so it is kept
// apart from the locale's own key.
const LEVEL_STORAGE_KEY = 'xq.level';

// storedLevel returns the remembered level, or null.
//
// Reading storage can throw outright — a blocked or private-mode store — and
// that is the same bargain storedLocale makes: it costs persistence, not the
// choice, and it never stops the page from loading.
function storedLevel() {
  try {
    return localStorage.getItem(LEVEL_STORAGE_KEY);
  } catch {
    return null;
  }
}

// storeLevel remembers the player's choice. Failure to store is likewise
// harmless: the choice holds for this page and no longer.
function storeLevel(name) {
  try {
    localStorage.setItem(LEVEL_STORAGE_KEY, name);
  } catch {
    // Storage is unavailable; the choice holds for this page and no longer.
  }
}

// applyStoredLevel selects the remembered level, before the first game starts.
//
// A remembered name the picker does not offer is not a choice this page can
// honour — a level a newer build dropped, or a value written by hand — so it is
// ignored and the markup's own default stands. Sending it would be refused by
// the server, which would greet the player with a failure instead of a game.
function applyStoredLevel() {
  const remembered = storedLevel();
  if (!remembered) return;
  const offered = Array.from(els.level.options).some((option) => option.value === remembered);
  if (offered) els.level.value = remembered;
}

// Choosing a level changes the level and nothing else. It deliberately does not
// touch the book switch: the book is now the level's opening candidate list
// rather than a source of moves stronger than the level, so a weak level wants
// it as much as a strong one, and a player who has turned it off has said
// something about their game that a level change should not overrule.
els.level.addEventListener('change', async () => {
  const level = els.level.value;
  storeLevel(level);
  await applyConfig({ level });
});

els.useBook.addEventListener('change', async () => {
  await applyConfig({ useBook: els.useBook.checked });
});

async function applyConfig(patch) {
  try {
    await api('/api/config', { method: 'POST', body: JSON.stringify(patch) });
    await refresh();
  } catch (err) {
    els.status.textContent = failureText(err);
    els.status.className = 'status over';
  }
}

// applyLocale makes tag the interface's language and redraws every string now on
// screen.
//
// Nothing here goes near the game. The server neither knows nor needs to know
// the locale, so switching language mid-game only redraws the words around a
// position the server has not been asked about — no move is lost, none replayed.
//
// persist is false at startup: resolving to the default because the player has
// expressed no preference is not the same as the player choosing it, and storing
// it would turn "no preference" into a choice that outranks their browser from
// then on.
function applyLocale(tag, { persist = true } = {}) {
  setLocale(tag);
  const active = currentLocale();
  if (persist) storeLocale(active);

  // The document's language, for screen readers and for the browser's own font
  // and hyphenation choices. It is the BCP-47 tag, so zh-Hans stays zh-Hans
  // rather than being flattened to zh.
  document.documentElement.lang = active;

  applyStatic(document);
  renderLevelOptions();
  renderLocaleOptions();
  render();
  drawAnalysis();
}

els.locale.addEventListener('change', () => {
  applyLocale(els.locale.value);
});

// renderLevelOptions labels the difficulty presets from their own values, so the
// level a player reads and the level sent to the server cannot disagree.
function renderLevelOptions() {
  for (const option of els.level.options) {
    option.textContent = t(`level.${option.value}`);
  }
}

// renderLocaleOptions fills the language picker from SUPPORTED_LOCALES.
//
// The three labels are written in their own language and are never translated —
// a player who cannot read the interface must still be able to find their way
// out of it — so they come from that one list rather than from markup that could
// drift out of step with it.
function renderLocaleOptions() {
  const options = SUPPORTED_LOCALES.map(({ tag, label }) => {
    const option = document.createElement('option');
    option.value = tag;
    option.textContent = label;
    return option;
  });
  els.locale.replaceChildren(...options);
  els.locale.value = currentLocale();
}

window.addEventListener('resize', () => board.resize());

// Start a game with whatever the controls currently say, then let the computer
// open if the player chose black.
(async function init() {
  // Which language to draw in is settled before anything is drawn: the stored
  // choice first, then what the browser says it prefers, then Vietnamese.
  applyLocale(resolveLocale(storedLocale(), navigator.languages || [navigator.language]), {
    persist: false,
  });

  // The level is read back before the first game starts, so the game the page
  // opens with is the one the player last chose.
  applyStoredLevel();

  await startGame();
})();
