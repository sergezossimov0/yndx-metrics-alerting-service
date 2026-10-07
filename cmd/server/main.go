package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/handler"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
	"go.uber.org/zap"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const (
	shutdownTimeout     = 5 * time.Second
	dbConnectionTimeout = 2 * time.Second
)

func main() {
	if err := run(); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			os.Exit(0)
		}
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := resolveConfig()
	if err != nil {
		return err
	}

	log, err := logger.New(resolveLogLevel())
	if err != nil {
		return err
	}
	defer log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var store usecase.MetricReadStore
	var updateStore usecase.MetricUpdateStore

	var db *sql.DB
	var snapshotDone chan struct{}
	if cfg.dbConnection != "" {
		db, err = sql.Open("pgx", cfg.dbConnection)
		if err != nil {
			return err
		}
		defer db.Close()

		if err := repository.ApplyMigrations(ctx, cfg.dbConnection, dbConnectionTimeout, log.With(zap.String("component", "migrations"))); err != nil {
			return err
		}
		dbStore := repository.NewPostgresStorage(db, dbConnectionTimeout, log.With(zap.String("component", "postgres")))

		store = dbStore
		updateStore = dbStore
	} else {
		memStore := repository.NewMemStorage()

		// config file storage path and restore flag
		snapshot := repository.NewSnapshot(cfg.fileStoragePath, cfg.storeInterval.interval, memStore,
			cfg.restore.isRestore, log.With(zap.String("component", "snapshot")))

		updateStore = memStore
		if cfg.storeInterval.interval == 0 {
			updateStore = snapshot
		} else {
			snapshotDone = make(chan struct{})
			go func() {
				defer close(snapshotDone)
				snapshot.Run(ctx)
			}()
		}
		store = memStore
	}

	uc := usecase.NewMetricUpdate(updateStore)
	readUC := usecase.NewMetricRead(store)
	r := chi.NewRouter()

	httpLog := log.With(zap.String("component", "http"))
	r.Use(handler.WithLogging(httpLog), handler.CompressionHandler(httpLog))

	r.Post("/update/", handler.UpdateMetricsJSONHandler(&uc, httpLog))
	r.Post("/updates/", handler.UpdateMetricsBatchJSONHandler(&uc, httpLog))
	r.Post("/value/", handler.GetMetricValueJSONHandler(&readUC, httpLog))
	r.Post("/update/{type}/{name}/{value}", handler.UpdateMetricsHandler(&uc, httpLog))
	r.Get("/value/{type}/{name}", handler.GetMetricValueHandler(&readUC))
	r.Get("/", handler.ListMetricsHandler(&readUC))
	r.Get("/ping", handler.PingHandler(db, dbConnectionTimeout, httpLog))

	srv := &http.Server{
		Addr:    cfg.serverAddress,
		Handler: r,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("Running server", zap.String("address", cfg.serverAddress))
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		log.Info("Shutting down server")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		err = srv.Shutdown(shutdownCtx)
	case err = <-serverErr:
	}

	// Cancel ctx (if a listen error got us here) and wait for the final snapshot,
	// which runs after in-flight requests have been drained by Shutdown.
	stop()
	if snapshotDone != nil {
		<-snapshotDone
	}
	return err
}

func normalizeHelpArg(args []string) []string {
	normalized := make([]string, len(args))
	copy(normalized, args)
	for i, arg := range normalized {
		if arg == "--help" {
			normalized[i] = "-h"
		}
	}
	return normalized
}

func resolveLogLevel() string {
	if envLogLevel, ok := os.LookupEnv("LOG_LEVEL"); ok {
		return envLogLevel
	}
	return "INFO"
}
