// Command parsrv serves the game.
//
// Everything expensive happens once, at boot: the corpus is read, the rates are
// fitted, the matchup graph is built and both models are loaded. After that the
// engine is read-only, so requests share it without locking and a decision
// costs a few milliseconds.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"manhattan/internal/api"
	"manhattan/internal/engine"
	"manhattan/internal/puzzle"
	"manhattan/internal/session"
	"manhattan/internal/store"
	"manhattan/web"
)

func main() {
	var (
		addr     = flag.String("addr", "127.0.0.1:8080", "listen address")
		dbPath   = flag.String("db", filepath.Join("data", "out", "par.db"), "results database")
		queue    = flag.String("queue", filepath.Join("data", "out", "puzzles.json"), "approved puzzle queue")
		secret   = flag.String("secret", "", "master secret; defaults to $PAR_SECRET")
		devSlow  = flag.Bool("dev", false, "serve assets from disk instead of the binary")
		poolPath = flag.String("pool", filepath.Join("data", "out", "pool.json"), "validated practice situations")
	)
	flag.Parse()

	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	if err := run(log, *addr, *dbPath, *queue, *poolPath, *secret, *devSlow); err != nil {
		log.Error("server failed", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger, addr, dbPath, queuePath, poolPath, secretFlag string, dev bool) error {
	secret := secretFlag
	if secret == "" {
		secret = os.Getenv("PAR_SECRET")
	}
	if secret == "" {
		// A development default, and it says so. The daily key must not be
		// guessable in production: with it, every ball of the day could be
		// computed before a single decision was made.
		secret = "manhattan-development-secret"
		log.Warn("using the development secret; set PAR_SECRET before this is public")
	}

	started := time.Now()

	eng, err := engine.New(engine.DefaultPaths())
	if err != nil {
		return fmt.Errorf("load engine: %w", err)
	}
	log.Info("engine loaded", "took", time.Since(started).Round(time.Millisecond))

	db, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer db.Close()

	sessions, err := session.NewStore([]byte(secret+"/session"), 6*time.Hour)
	if err != nil {
		return err
	}

	var q *puzzle.Queue
	if loaded, err := puzzle.LoadQueue(queuePath); err == nil {
		q = loaded
		log.Info("puzzle queue loaded", "path", queuePath, "puzzles", len(q.Puzzles))
	} else {
		log.Warn("no validated puzzle queue; days will be generated unchecked",
			"path", queuePath, "run", "make puzzles")
	}

	var pool *puzzle.Pool
	if loaded, err := puzzle.LoadPool(poolPath); err == nil {
		pool = loaded
		log.Info("practice pool loaded", "path", poolPath, "situations", pool.Len())
	} else {
		log.Warn("no practice pool; practice mode will be unavailable",
			"path", poolPath, "run", "parpuzzle -pool 20")
	}

	assets, err := webAssets(dev)
	if err != nil {
		return err
	}

	srv := &api.Server{
		Engine:   eng,
		Sessions: sessions,
		DB:       db,
		Queue:    q,
		Pool:     pool,
		Secret:   []byte(secret),
		Log:      log,
		Now:      time.Now,
	}

	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Routes(assets),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)
	go func() {
		log.Info("par is listening",
			"addr", "http://"+addr,
			"boot", time.Since(started).Round(time.Millisecond))
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- err
		}
	}()

	select {
	case err := <-errc:
		return fmt.Errorf("listen: %w", err)
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Give in-flight overs a chance to finish rather than cutting a player off
	// mid-innings.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}

// webAssets serves the page and its static files.
//
// In development they come from disk so a CSS change needs only a refresh; in
// every other case they come from the binary.
func webAssets(dev bool) (http.Handler, error) {
	var root fs.FS = web.FS()
	if dev {
		root = os.DirFS("web")
	}

	static, err := fs.Sub(root, "static")
	if err != nil {
		return nil, fmt.Errorf("static assets: %w", err)
	}

	index, err := fs.ReadFile(root, "templates/index.html")
	if err != nil {
		return nil, fmt.Errorf("read index: %w", err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /static/", http.StripPrefix("/static/", cacheFor(time.Hour, http.FileServerFS(static))))
	mux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		page := index
		if dev {
			// Re-read so an edit shows up without a restart.
			if b, err := fs.ReadFile(root, "templates/index.html"); err == nil {
				page = b
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(stamp(root, page))
	})
	return mux, nil
}

// stamp rewrites the asset links so their URL changes whenever their contents
// do.
//
// Serving the script at a fixed path and caching it for an hour meant a browser
// could hold yesterday's script against today's page: the two were each
// correct, and together they failed on the first click. Worse, the fix could
// not take effect, because a response still inside its freshness window is
// never revalidated, so the stale copy kept being used regardless of what the
// server now said.
//
// A content hash in the query string removes the whole problem. A changed file
// is a changed URL, which the browser has no cached answer for, and an
// unchanged file keeps its long cache.
func stamp(root fs.FS, page []byte) []byte {
	for _, name := range []string{"par.css", "par.js"} {
		b, err := fs.ReadFile(root, "static/"+name)
		if err != nil {
			continue
		}
		sum := sha256.Sum256(b)
		versioned := fmt.Sprintf("/static/%s?v=%s", name, hex.EncodeToString(sum[:4]))
		page = bytes.ReplaceAll(page, []byte("/static/"+name), []byte(versioned))
	}
	return page
}

// cacheFor allows assets to be held, which is safe because their URLs carry a
// content hash and therefore change whenever they do.
func cacheFor(d time.Duration, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", int(d.Seconds())))
		next.ServeHTTP(w, r)
	})
}
