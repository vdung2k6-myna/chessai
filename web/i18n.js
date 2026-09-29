// i18n.js — the interface's translation layer.
//
// The server never learns the player's language. It answers with stable codes
// and stable board notation, and this file turns those codes into text in the
// locale the player chose. That keeps every response identical for every player
// and keeps translation out of the Go code.
//
// The tables live in web/locales/<tag>.js as plain globals loaded before this
// script and before app.js, so nothing is fetched before the first paint: there
// is no window where the interface is drawn but has no words.
//
// This file is deliberately small. It is a lookup and a substitution, not a
// framework: the interface has no plurals, no dates, and no gender agreement to
// negotiate.

// FALLBACK_LOCALE is the reference locale. Every key is expected to exist here,
// and any locale that lacks one falls back to it rather than showing a hole.
const FALLBACK_LOCALE = 'en';

// SUPPORTED_LOCALES is the locales the interface ships, in the order the picker
// shows them. Labels are written in their own language and are never translated:
// a player who has landed in a language they cannot read must still be able to
// find their way out.
const SUPPORTED_LOCALES = [
  { tag: 'vi', label: 'Tiếng Việt' },
  { tag: 'en', label: 'English' },
  { tag: 'zh-Hans', label: '中文' },
];

// DEFAULT_LOCALE is what the interface uses when the player has expressed no
// preference at all.
const DEFAULT_LOCALE = 'vi';

// activeLocale is the locale currently being rendered. Which one that is gets
// decided once at startup and whenever the player picks another.
let activeLocale = FALLBACK_LOCALE;

// localeTables returns the registered tables, keyed by tag.
function localeTables() {
  return globalThis.XQ_LOCALES || {};
}

// table returns the table for a tag, or null when there is no table for it.
//
// A tag can be listed in SUPPORTED_LOCALES while its table is missing — a locale
// file that failed to load, or a locale added to the list before its file — and
// that is treated as an absent translation rather than a crash.
function table(tag) {
  const tables = localeTables();
  return Object.prototype.hasOwnProperty.call(tables, tag) ? tables[tag] : null;
}

// currentLocale returns the tag of the locale being rendered.
function currentLocale() {
  return activeLocale;
}

// setLocale makes tag active, if it is a locale the interface can actually
// render, and returns the tag that ended up active.
//
// It changes nothing on screen: only the caller knows what is drawn, so
// re-rendering is the caller's business.
function setLocale(tag) {
  activeLocale = table(tag) ? tag : FALLBACK_LOCALE;
  return activeLocale;
}

// localeLabel returns the name of a locale in its own language.
function localeLabel(tag) {
  const entry = SUPPORTED_LOCALES.find((l) => l.tag === tag);
  return entry ? entry.label : tag;
}

// LOCALE_STORAGE_KEY is where the player's choice is remembered.
const LOCALE_STORAGE_KEY = 'xq.locale';

// storedLocale returns the remembered choice, or null.
//
// Reading storage can throw outright — a blocked or private-mode store — and
// that is not a failure worth surfacing: it only means the choice will not
// outlive the tab, so the player gets the right language either way.
function storedLocale() {
  try {
    return localStorage.getItem(LOCALE_STORAGE_KEY);
  } catch {
    return null;
  }
}

// storeLocale remembers the player's choice. Failure to store is likewise
// harmless: it costs persistence, not the language.
function storeLocale(tag) {
  try {
    localStorage.setItem(LOCALE_STORAGE_KEY, tag);
  } catch {
    // Storage is unavailable; the choice holds for this page and no longer.
  }
}

// matchLocale finds the supported locale a browser tag refers to, or null.
//
// Browser tags carry more than we ship: "vi-VN", "en-GB", "zh-Hans-CN",
// "zh-CN". The rules, in order, are the whole tag, then the primary subtag, and
// then Chinese as a special case — Simplified is the only Chinese this interface
// ships, so a tag asking for Chinese in general, or for the PRC's convention
// without naming a script, gets zh-Hans rather than nothing.
function matchLocale(tag) {
  if (!tag) return null;
  const want = String(tag).toLowerCase();

  const exact = SUPPORTED_LOCALES.find((l) => l.tag.toLowerCase() === want);
  if (exact) return exact.tag;

  const primary = want.split('-')[0];
  const byPrimary = SUPPORTED_LOCALES.find((l) => l.tag.toLowerCase() === primary);
  if (byPrimary) return byPrimary.tag;

  if (primary === 'zh') {
    const chinese = SUPPORTED_LOCALES.find((l) => l.tag.toLowerCase().startsWith('zh'));
    if (chinese) return chinese.tag;
  }
  return null;
}

// resolveLocale decides which locale to render: the player's remembered choice,
// failing that the browser's stated preferences, failing that the default.
//
// A remembered tag that is not a locale this interface ships is not a choice it
// can honour, so it falls through to the browser rather than pinning the
// interface to the fallback forever.
function resolveLocale(stored, preferred) {
  const remembered = matchLocale(stored);
  if (remembered) return remembered;

  for (const tag of preferred || []) {
    const match = matchLocale(tag);
    if (match) return match;
  }
  return DEFAULT_LOCALE;
}

// lookup returns the text for a key in one locale, or null when that locale has
// no string for it.
function lookup(tag, key) {
  const strings = table(tag);
  if (!strings) return null;
  return Object.prototype.hasOwnProperty.call(strings, key) ? strings[key] : null;
}

// hasKey reports whether a key exists in the active locale or in the fallback.
// It exists so a caller can offer a deliberate alternative for a key that may be
// absent — an unrecognized server code, say — instead of rendering the key name.
function hasKey(key) {
  return lookup(activeLocale, key) !== null || lookup(FALLBACK_LOCALE, key) !== null;
}

// verbatim is the value treatment for text that will be set as text: the browser
// escaped nothing yet, and escaping here would show the reader a literal
// "&amp;".
function verbatim(value) {
  return String(value);
}

const HTML_ESCAPES = {
  '&': '&amp;',
  '<': '&lt;',
  '>': '&gt;',
  '"': '&quot;',
  "'": '&#39;',
};

// escapeHTML neutralizes a value that is about to be inserted into markup.
function escapeHTML(value) {
  return String(value).replace(/[&<>"']/g, (ch) => HTML_ESCAPES[ch]);
}

// substitute replaces {name} with the matching value, treating values with the
// given function. The template itself is never escaped or transformed: it comes
// from a locale table, which is the one place markup is allowed to live.
//
// A placeholder with no matching param is left as written, so a template that
// names a value the caller forgot to pass is visible in the page rather than
// silently empty.
function substitute(text, params, treat) {
  if (!params) return text;
  return text.replace(/\{(\w+)\}/g, (whole, name) =>
    (Object.prototype.hasOwnProperty.call(params, name)
      ? treat(params[name])
      : whole));
}

// renderText looks up a key and substitutes its params.
//
// A key the active locale does not translate falls back to English, so a partly
// translated interface reads as English in the gaps rather than showing blanks
// or key names. A key in neither table is a programming error — a t() call with
// a typo — so it returns the key itself, which is visible in the page and names
// the mistake. The key-set test in testdata/ is what keeps that from happening.
//
// The name is deliberate: these scripts share one global scope, and app.js
// already owns a `render`. A collision here would silently redirect t().
function renderText(key, params, treat) {
  const own = lookup(activeLocale, key);
  if (own !== null) return substitute(own, params, treat);

  const fallback = lookup(FALLBACK_LOCALE, key);
  if (fallback !== null) return substitute(fallback, params, treat);

  return key;
}

// t returns the text for a key, for use as text (textContent).
function t(key, params) {
  return renderText(key, params, verbatim);
}

// tHTML returns the text for a key, for insertion as HTML (innerHTML).
//
// Markup belongs to the template, which is how one sentence keeps its bold or
// its link in every language the interface ships. Values are escaped instead: a
// value is data, and data must never be able to introduce markup.
function tHTML(key, params) {
  return renderText(key, params, escapeHTML);
}

// formatNumber renders a count with the active locale's conventions, which are
// not necessarily the browser's: a player who chose Vietnamese in an English
// browser expects Vietnamese separators.
function formatNumber(value) {
  try {
    return Number(value).toLocaleString(activeLocale);
  } catch {
    return String(value);
  }
}

// applyStatic fills the elements that opt into translation from the active
// locale. Three forms, because a few strings are markup or live in an attribute:
//
//   data-i18n="key"                  sets the text
//   data-i18n-html="key"             sets markup (the template has tags)
//   data-i18n-attr="attr:key,..."     sets the named attribute
//
// It is safe to call again after a language change, and it touches only elements
// that ask for it, so an element whose text the app computes is left alone.
function applyStatic(root) {
  const scope = root || document;

  for (const el of scope.querySelectorAll('[data-i18n]')) {
    el.textContent = t(el.getAttribute('data-i18n'));
  }
  for (const el of scope.querySelectorAll('[data-i18n-html]')) {
    el.innerHTML = tHTML(el.getAttribute('data-i18n-html'));
  }
  for (const el of scope.querySelectorAll('[data-i18n-attr]')) {
    for (const pair of el.getAttribute('data-i18n-attr').split(',')) {
      const at = pair.indexOf(':');
      if (at < 0) continue;
      const attr = pair.slice(0, at).trim();
      const key = pair.slice(at + 1).trim();
      if (attr && key) el.setAttribute(attr, t(key));
    }
  }
}
