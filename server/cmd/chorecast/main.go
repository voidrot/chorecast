// Command chorecast is the self-hosted Chorecast server: API, web admin, and background jobs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
	_ "time/tzdata" // embed the timezone database so household timezones work in any image

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/voidrot/chorecast/server/internal/api"
	"github.com/voidrot/chorecast/server/internal/clock"
	"github.com/voidrot/chorecast/server/internal/config"
	"github.com/voidrot/chorecast/server/internal/db"
	"github.com/voidrot/chorecast/server/internal/version"
	"github.com/voidrot/chorecast/server/internal/webui"
)

const usage = `usage: chorecast <command>

commands:
  serve     run the server (default)
  openapi   print the OpenAPI spec as JSON (-3.0 for an OpenAPI 3.0 copy, for client generators)
  version   print the version`

func main() {
	cmd := "serve"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}
	var err error
	switch cmd {
	case "serve":
		err = serve()
	case "openapi":
		err = printOpenAPI(len(os.Args) > 2 && os.Args[2] == "-3.0")
	case "version":
		fmt.Printf("chorecast %s (%s) api v%d\n", version.Version, version.Commit, version.APIVersion)
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func serve() error {
	cfg := config.Load()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := db.Open(ctx, cfg.DataDir)
	if err != nil {
		return err
	}
	defer func() { _ = store.Close() }()

	clk := clock.Real{}
	runner, sched, bus := newBackground(store, clk, slog.Default())
	if err := runner.Recover(ctx); err != nil {
		return err
	}
	// Background work gets its own context so it also stops when the HTTP server fails.
	bgCtx, stopBackground := context.WithCancel(ctx)
	var background sync.WaitGroup
	background.Go(func() { runner.Start(bgCtx) })
	background.Go(func() { sched.Start(bgCtx, schedulerInterval) })
	background.Go(func() { bus.Start(bgCtx) })
	defer func() {
		stopBackground()
		background.Wait()
	}()

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer)
	api.New(r, cfg, store, clk)
	r.Handle("/*", webui.Handler())

	srv := &http.Server{Addr: cfg.Addr, Handler: r, ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() {
		slog.Info("listening", "addr", cfg.Addr, "version", version.Version, "data", cfg.DataDir)
		errc <- srv.ListenAndServe()
	}()

	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// printOpenAPI writes the spec. Huma generates OpenAPI 3.1; v30 writes a 3.0 downgrade, which
// client generators (openapi-generator for the Android client) support fully.
func printOpenAPI(v30 bool) error {
	hapi := api.New(chi.NewRouter(), config.Load(), nil, clock.Real{})
	var spec any = hapi.OpenAPI()
	if v30 {
		b, err := hapi.OpenAPI().Downgrade()
		if err != nil {
			return err
		}
		spec = json.RawMessage(b)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(spec)
}
