// Command chorecast is the self-hosted Chorecast server: API, web admin, and background jobs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
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
	"github.com/voidrot/chorecast/server/internal/jobs"
	"github.com/voidrot/chorecast/server/internal/media"
	"github.com/voidrot/chorecast/server/internal/setup"
	"github.com/voidrot/chorecast/server/internal/version"
	"github.com/voidrot/chorecast/server/internal/webui"
)

const usage = `usage: chorecast <command>

commands:
  serve     run the server (default)
  admin     account recovery: reset-password, disable-2fa (run "chorecast admin" for help)
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
	case "admin":
		err = admin(context.Background(), os.Args[2:], os.Stdout)
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
	// Retention only deletes files, so this storage needs no URL signer.
	runner, sched, bus := newBackground(store, clk, media.NewLocal(cfg.DataDir, "", nil), slog.Default())
	if err := runner.Recover(ctx); err != nil {
		return err
	}
	// Bring the horizon up to date once the runner starts (the server may have been off).
	if _, _, err := runner.Enqueue(ctx, kindHorizon, struct{}{}, jobs.EnqueueOptions{}); err != nil {
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

	setupSvc, err := setup.New(ctx, store, clk)
	if err != nil {
		return err
	}
	if token, err := setupSvc.Begin(ctx); err != nil {
		return err
	} else if token != "" {
		slog.Warn("Setup required: open " + setup.SetupURL(publicBase(cfg), token))
	}

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer)
	api.New(r, api.Deps{Config: cfg, DB: store, Clock: clk, Setup: setupSvc, Bus: bus})
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

// publicBase is the server's URL for printed links: CHORECAST_PUBLIC_URL, or localhost on the
// listen port.
func publicBase(cfg config.Config) string {
	if cfg.PublicURL != "" {
		return cfg.PublicURL
	}
	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil || port == "" {
		port = "8080"
	}
	return "http://localhost:" + port
}

// printOpenAPI writes the spec. Huma generates OpenAPI 3.1; v30 writes a 3.0 downgrade, which
// client generators (openapi-generator for the Android client) support fully.
func printOpenAPI(v30 bool) error {
	hapi := api.New(chi.NewRouter(), api.Deps{Config: config.Load(), Clock: clock.Real{}})
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
