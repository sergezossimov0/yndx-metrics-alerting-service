package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/config"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/handler"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
	"go.uber.org/zap"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	shutdownTimeout     = 5 * time.Second
	dbConnectionTimeout = 2 * time.Second
)

func main() {
	if err := start(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// start wires the server: configuration, logger, storage and router. It is
// separate from main so that deferred cleanups run before os.Exit.
func start() error {
	cfg, err := resolveConfig()
	if err != nil {
		return err
	}

	log, err := logger.New(config.LogLevel())
	if err != nil {
		return err
	}
	defer log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	s, err := newStore(ctx, cfg, log)
	if err != nil {
		return err
	}
	// runs after run has returned, i.e. after Shutdown drained in-flight requests
	defer s.close()

	return run(ctx, cfg.serverAddress, newRouter(s, log), log)
}

func newRouter(s *storage, log *zap.Logger) http.Handler {
	uc := usecase.NewMetricUpdate(s.update)
	readUC := usecase.NewMetricRead(s.read)
	r := chi.NewRouter()

	httpLog := log.With(zap.String("component", "http"))
	r.Use(handler.WithLogging(httpLog), handler.CompressionHandler(httpLog))

	r.Post("/update/", handler.UpdateMetricsJSONHandler(&uc, httpLog))
	r.Post("/updates/", handler.UpdateMetricsBatchJSONHandler(&uc, httpLog))
	r.Post("/value/", handler.GetMetricValueJSONHandler(&readUC, httpLog))
	r.Post("/update/{type}/{name}/{value}", handler.UpdateMetricsHandler(&uc, httpLog))
	r.Get("/value/{type}/{name}", handler.GetMetricValueHandler(&readUC))
	r.Get("/", handler.ListMetricsHandler(&readUC))
	r.Get("/ping", handler.PingHandler(s.db, dbConnectionTimeout, httpLog))
	return r
}

// run serves h on addr until ctx is cancelled or the listener fails,
// then shuts the server down gracefully.
func run(ctx context.Context, addr string, h http.Handler, log *zap.Logger) error {
	srv := &http.Server{
		Addr:    addr,
		Handler: h,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("Running server", zap.String("address", addr))
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("Shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	case err := <-serverErr:
		return err
	}
}
