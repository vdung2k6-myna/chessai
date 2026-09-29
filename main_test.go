package main

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// TestStaticHandlerCacheHeaders pins the difference between the two ways the web
// UI can be served.
//
// The embedded branch must refuse caching: an embed.FS entry has no modification
// time, so http.FileServerFS sends no validator, and a browser that has nothing
// to revalidate against may keep a page the rebuilt binary no longer contains.
// The failure is silent — the interface simply stays as it was — so it is worth
// a test rather than a comment.
//
// The -dev branch is the opposite: its files have real modification times, and
// it is the branch whose whole purpose is that an edit shows up on reload, so it
// must not be marked uncacheable. A "fix" that wrapped both branches would break
// the flag it exists to serve.
func TestStaticHandlerCacheHeaders(t *testing.T) {
	cases := []struct {
		name    string
		dev     bool
		path    string
		noStore bool
	}{
		{"embedded html", false, "/", true},
		{"embedded script", false, "/app.js", true},
		{"embedded locale", false, "/locales/vi.js", true},
		{"embedded stylesheet", false, "/style.css", true},
		{"dev serves from disk", true, "/", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, err := staticHandler(tc.dev)
			if err != nil {
				t.Fatalf("staticHandler(%v): %v", tc.dev, err)
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, tc.path, nil))

			got := rec.Header().Get("Cache-Control") == "no-store"
			if got != tc.noStore {
				t.Errorf("GET %s (dev=%v): Cache-Control = %q, want no-store present = %v",
					tc.path, tc.dev, rec.Header().Get("Cache-Control"), tc.noStore)
			}
		})
	}
}

// assetRef matches src="..." and href="..." in the document.
var assetRef = regexp.MustCompile(`(?:src|href)="([^"]+)"`)

// TestIndexAssetsAreVersioned holds the page to the convention described in
// index.html: every local asset URL carries the deploy's version token.
//
// The token is what gives a cache something to miss on. Without it a host that
// keys on the URL answers from whatever it already holds, and the page can be
// assembled from two different builds — the failure this convention was adopted
// to end. A forgotten token on one new <script> tag would reintroduce it, and
// the symptom (a stale asset serving a page that no longer matches) points at
// the cache rather than at the omission, so it is worth failing here instead.
//
// The check is that the assets carry a token, that they all carry the *same*
// one, and that each one actually resolves — a versioned URL naming a file that
// does not exist is a blank page, not a stale one.
func TestIndexAssetsAreVersioned(t *testing.T) {
	html, err := fs.ReadFile(webFS, "web/index.html")
	if err != nil {
		t.Fatalf("reading the embedded index.html: %v", err)
	}

	// Local means a path this server serves: no scheme, no protocol-relative
	// host, no data: URI. The external chessdb link in the tagline is not ours.
	var local []string
	for _, m := range assetRef.FindAllStringSubmatch(string(html), -1) {
		ref := m[1]
		if strings.Contains(ref, "://") || strings.HasPrefix(ref, "//") || strings.HasPrefix(ref, "data:") {
			continue
		}
		if !strings.HasSuffix(ref, ".js") && !strings.HasSuffix(ref, ".css") &&
			!strings.Contains(ref, ".js?") && !strings.Contains(ref, ".css?") {
			continue
		}
		local = append(local, ref)
	}
	if len(local) == 0 {
		t.Fatal("no local assets found in index.html; did the markup change shape?")
	}

	static, err := staticHandler(false)
	if err != nil {
		t.Fatalf("staticHandler(false): %v", err)
	}

	tokens := map[string]bool{}
	for _, ref := range local {
		_, query, found := strings.Cut(ref, "?")
		if !found || !strings.HasPrefix(query, "v=") || query == "v=" {
			t.Errorf("index.html references %q without a ?v= token; every local asset needs one", ref)
			continue
		}
		tokens[query] = true

		rec := httptest.NewRecorder()
		// Serve the path with its query, as a browser would request it.
		static.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+ref, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("GET /%s: status %d, want 200", ref, rec.Code)
		}
	}

	if len(tokens) > 1 {
		t.Errorf("assets carry %d different version tokens (%v); they must move together", len(tokens), tokens)
	}
}
