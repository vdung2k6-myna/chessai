// Tests the locale tables in web/locales/, which no Go test can reach and which
// a browser would otherwise be the only thing to exercise.
//
// Run with:  node testdata/i18n_test.js
//
// It lives under testdata/ so that neither `go build` nor //go:embed all:web
// picks it up: this is a test program, not something to ship or serve.
//
// What it protects, and why each one is a real failure rather than a style
// preference:
//
//   1. The three tables hold the same keys. A key in one and not another is a
//      hole that shows English — or worse, the raw key — in one language only,
//      which is exactly the bug this change exists to prevent.
//   2. Every key the interface asks for exists. A typo'd or forgotten key
//      renders as its own name, so the interface says "button.hint" at someone.
//   3. A key's placeholders are the same in all three locales. A translation
//      that drops {move} renders a sentence with a hole in it, and reads as
//      fluent while saying nothing.
//   4. Markup appears only in the templates that are inserted as markup. A
//      template with tags but inserted as text shows the reader literal "<strong>",
//      and one without tags inserted as markup is harmless but a sign of drift.
//
// The families {error.<code>}, {reason.<code>} and {level.<name>} cannot be read
// off a call site — the key is assembled at run time — so their members are
// taken from the code that produces them: the Go constants for the codes, and
// index.html for the level names.

const fs = require('fs');
const path = require('path');
const vm = require('vm');
const assert = require('assert');

const root = path.join(__dirname, '..');
const readWeb = (p) => fs.readFileSync(path.join(root, 'web', p), 'utf8');
const readRepo = (p) => fs.readFileSync(path.join(root, p), 'utf8');

// --- the tables, loaded exactly as the browser loads them ------------------

const ctx = vm.createContext({});
for (const tag of ['en', 'vi', 'zh-Hans']) {
  vm.runInContext(readWeb(`locales/${tag}.js`), ctx, { filename: `locales/${tag}.js` });
}
const TABLES = ctx.XQ_LOCALES;
const TAGS = Object.keys(TABLES);
const REFERENCE = 'en';

// --- reading keys out of the sources ---------------------------------------

// A key looks like "area.name": lowercase letters and digits, a dot, then the
// name. Property accesses are not string literals, so this does not collide with
// ordinary code, and the shape rules out the other literals in these files
// ('application/json', 'Content-Type', '/api/state').
const KEY_SHAPE = /^[a-z][A-Za-z0-9]*\.[A-Za-z0-9_]+$/;

// STORAGE_PREFIX marks the namespace that is not a translation: keys like
// "xq.level" name an entry in localStorage, and are shaped exactly like a
// translation key. They are skipped rather than given strings, which would
// translate a storage name into three languages for no reader's benefit.
// (The locale's key is declared in i18n.js, which this file does not scan, so
// only app.js's keys would be picked up here at all.)
const STORAGE_PREFIX = 'xq.';

// stringLiterals returns every single- or double-quoted literal in a source
// file. Deliberately dumb and per-line: these files are hand-written, and a
// scanner that understood templates would hide a key assembled from one.
function stringLiterals(src) {
  const out = [];
  const re = /'((?:[^'\\\n]|\\.)*)'|"((?:[^"\\\n]|\\.)*)"/g;
  let m;
  while ((m = re.exec(src)) !== null) out.push(m[1] !== undefined ? m[1] : m[2]);
  return out;
}

// tableKeys returns every key the interface asks for.
function referencedKeys() {
  const keys = new Set();

  // Written out in full at the call site, including from a ternary.
  for (const lit of stringLiterals(readWeb('app.js'))) {
    if (KEY_SHAPE.test(lit) && !lit.startsWith(STORAGE_PREFIX)) keys.add(lit);
  }

  // Marked up in the document: data-i18n, data-i18n-html, and
  // data-i18n-attr="attribute:key,...".
  const html = readWeb('index.html');
  for (const m of html.matchAll(/data-i18n(?:-html)?="([^"]+)"/g)) keys.add(m[1]);
  for (const m of html.matchAll(/data-i18n-attr="([^"]+)"/g)) {
    for (const pair of m[1].split(',')) {
      const at = pair.indexOf(':');
      if (at >= 0) keys.add(pair.slice(at + 1).trim());
    }
  }

  // Assembled at run time, so their members come from whoever produces them.
  for (const code of serverErrorCodes()) keys.add(`error.${code}`);
  for (const code of ['network', 'unknown']) keys.add(`error.${code}`); // client-side only
  for (const code of reasonCodes()) keys.add(`reason.${code}`);
  keys.add('reason.unknown');
  for (const name of levelValues()) keys.add(`level.${name}`);

  return keys;
}

// serverErrorCodes reads the failure codes out of the Go source that declares
// them, so a code added to the server without a string in every table fails
// here rather than reaching a player as a bare identifier.
function serverErrorCodes() {
  const src = readRepo('internal/httpapi/server.go');
  return [...src.matchAll(/^\s*Code\w+\s*=\s*"([a-z0-9_]+)"/gm)].map((m) => m[1]);
}

// reasonCodes reads the game-over reason codes out of the Go source likewise.
function reasonCodes() {
  const src = readRepo('internal/xiangqi/xiangqi.go');
  return [...src.matchAll(/^\s*Reason\w+\s+Reason\s*=\s*"([a-z0-9_]+)"/gm)].map((m) => m[1]);
}

// levelValues reads the level names out of the markup, which is what the picker
// offers and therefore what renderLevelOptions() will look up.
function levelValues() {
  const select = readWeb('index.html').match(/<select id="level">([\s\S]*?)<\/select>/);
  assert.ok(select, 'index.html should still have a #level select to read levels from');
  return [...select[1].matchAll(/<option value="([a-z]+)"/g)].map((m) => m[1]);
}

// placeholders returns the {names} a template asks to be given.
function placeholders(text) {
  return [...String(text).matchAll(/\{(\w+)\}/g)].map((m) => m[1]).sort();
}

// --- the checks ------------------------------------------------------------

let checks = 0;
function check(what, fn) {
  fn();
  checks++;
  console.log(`ok  ${what}`);
}

// 1. One key set across the three tables.
check('the three tables have identical key sets', () => {
  const reference = Object.keys(TABLES[REFERENCE]).sort();
  for (const tag of TAGS) {
    const keys = Object.keys(TABLES[tag]).sort();
    const missing = reference.filter((k) => !keys.includes(k));
    const extra = keys.filter((k) => !reference.includes(k));
    assert.deepStrictEqual(
      { missing, extra }, { missing: [], extra: [] },
      `${tag} differs from ${REFERENCE}: missing ${JSON.stringify(missing)}, extra ${JSON.stringify(extra)}`);
  }
});

// 2. Every key the interface asks for exists in every table.
check('every referenced key exists in every table', () => {
  const referenced = [...referencedKeys()].sort();
  const unresolved = referenced.filter((k) => !KEY_SHAPE.test(k));
  assert.deepStrictEqual(unresolved, [], `not key-shaped: ${JSON.stringify(unresolved)}`);

  for (const tag of TAGS) {
    const absent = referenced.filter((k) => !(k in TABLES[tag]));
    assert.deepStrictEqual(absent, [], `${tag} has no string for: ${JSON.stringify(absent)}`);
  }
  console.log(`    (${referenced.length} keys referenced, ${TAGS.length} tables)`);
});

// 3. A key's placeholders survive translation.
check('placeholders match across locales for every key', () => {
  const bad = [];
  for (const key of Object.keys(TABLES[REFERENCE])) {
    const want = placeholders(TABLES[REFERENCE][key]);
    for (const tag of TAGS) {
      if (tag === REFERENCE) continue;
      const got = placeholders(TABLES[tag][key]);
      if (JSON.stringify(want) !== JSON.stringify(got)) {
        bad.push(`${key}: ${REFERENCE}${JSON.stringify(want)} vs ${tag}${JSON.stringify(got)}`);
      }
    }
  }
  assert.deepStrictEqual(bad, [], `placeholders differ:\n    ${bad.join('\n    ')}`);
});

// 4. Markup only where markup is understood.
//
// The invariant runs one way: a template that carries tags must be inserted as
// markup, in every locale. The converse is deliberately not checked — a
// translation that drops the bold from a sentence inserted with tHTML renders
// correctly, just unbolded, and is not a bug.
check('markup lives only in templates inserted as markup', () => {
  const src = readWeb('app.js');
  const htmlKeys = new Set();
  for (const m of src.matchAll(/\btHTML\(\s*'([^'\n]+)'/g)) htmlKeys.add(m[1]);
  for (const m of readWeb('index.html').matchAll(/data-i18n-html="([^"]+)"/g)) htmlKeys.add(m[1]);

  const wrong = [];
  for (const tag of TAGS) {
    for (const [key, value] of Object.entries(TABLES[tag])) {
      if (/[<>]/.test(value) && !htmlKeys.has(key)) {
        wrong.push(`${tag}: ${key} carries markup but is inserted as text (use tHTML or data-i18n-html)`);
      }
    }
  }
  assert.deepStrictEqual(wrong, [], wrong.join('\n    '));
});

console.log(`\n${checks} checks passed across ${TAGS.join(', ')}`);
