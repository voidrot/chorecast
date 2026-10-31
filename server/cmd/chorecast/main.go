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
  openapi   print the OpenAPI spec as JSON
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
		err = printOpenAPI()
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

	r := chi.NewRouter()
	r.Use(middleware.RequestID, middleware.Recoverer)
	api.New(r, cfg, store, clock.Real{})
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

func printOpenAPI() error {
	hapi := api.New(chi.NewRouter(), config.Load(), nil, clock.Real{})
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(hapi.OpenAPI())
}
