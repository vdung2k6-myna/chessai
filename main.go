// Command chessai serves a playable game of Chinese Chess (Xiangqi).
//
// The computer's moves come from the godogpaw engine running in this process,
// with the chessdb.cn cloudbook consulted first as an opening book. The web UI
// is embedded in the binary.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"chessai/internal/cloud"
	"chessai/internal/game"
	"chessai/internal/httpapi"
)

//go:embed all:web
var webFS embed.FS

func main() {
	var (
		addr    = flag.String("addr", "127.0.0.1:8080", "address to listen on; the default is loopback only")
		offline = flag.Bool("offline", false, "do not consult the chessdb.cn opening book")
		dev     = flag.Bool("dev", false, "serve web/ from disk so edits show up on reload")
		level   = flag.String("level", game.DefaultLevel, "difficulty, one of: "+strings.Join(game.LevelNames(), ", "))
		verbose = flag.Bool("v", false, "log at debug level")
	)
	flag.Parse()

	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	// Logs go to stderr, never stdout: the engine prints its "info ..." lines to
	// stdout on every search iteration and we capture that stream to read the
	// search statistics. Logging into stdout would corrupt the capture.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(log)

	// A nil client is the offline case: the computer still plays, it just never
	// consults the book.
	var book cloud.Client
	if !*offline {
		book = cloud.New(cloud.WithLogger(log))
	}

	cfg := game.DefaultConfig()
	cfg.Level = *level
	g, err := game.New(cfg, book, log)
	if err != nil {
		// An unknown level is a mistake in how the server was started, not a
		// failure to start, so it gets the usage exit code and the list to
		// choose from rather than a log line.
		if errors.Is(err, game.ErrBadLevel) {
			fmt.Fprintf(os.Stderr, "-level %s is not a level; choose one of: %s\n",
				*level, strings.Join(game.LevelNames(), ", "))
			os.Exit(2)
		}
		log.Error("could not start a game", "err", err)
		os.Exit(1)
	}

	static, err := staticHandler(*dev)
	if err != nil {
		log.Error("could not serve the web UI", "err", err)
		os.Exit(1)
	}

	mux := httpapi.New(g, log).Handler(static)

	srv := &http.Server{
		Addr:    *addr,
		Handler: httpapi.Middleware(mux, log),
		// Bound how long a slow client may hold a connection, but leave the
		// write deadline alone: a deep search legitimately takes tens of
		// seconds, and the response cannot be sent until it finishes.
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
		WriteTimeout:      0,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		log.Info("listening", "url", "http://"+*addr, "offline", *offline, "level", *level)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		log.Error("server stopped", "err", err)
		os.Exit(1)
	case <-ctx.Done():
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Warn("graceful shutdown did not complete", "err", err)
		}
	}
}

// staticHandler serves the web UI, either from disk for development or from the
// copy embedded in the binary.
//
// The two branches answer caching differently, because they have different
// things to offer a browser. Files served from disk carry a real modification
// time, so http.FileServer sends Last-Modified and a plain reload revalidates
// cheaply. Embedded files carry none, so the embedded branch refuses caching
// outright — see noStore.
func staticHandler(dev bool) (http.Handler, error) {
	if dev {
		return http.FileServer(http.Dir("web")), nil
	}
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		return nil, err
	}
	return noStore(http.FileServerFS(sub)), nil
}

// noStore marks every response uncacheable.
//
// An embed.FS entry has a zero modification time, so http.FileServerFS has no
// validator to send: neither Last-Modified nor ETag appears on the response,
// and a browser left with nothing to revalidate against is free to go on using
// whatever copy it already has. The symptom is an edit that appears not to have
// taken — a rebuilt binary serving a page the browser never asked for again,
// which is indistinguishable from the change simply not working.
//
// The embedded bytes can only ever change by rebuilding the binary, and that is
// precisely the moment a browser must not reuse what it holds. So the whole
// resource is declared uncacheable rather than conditionally revalidated; over
// loopback the re-download is free, and it removes the failure mode instead of
// making it rarer.
func noStore(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		h.ServeHTTP(w, r)
	})
}
