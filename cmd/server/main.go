package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/handler"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
	"go.uber.org/zap"
)

const shutdownTimeout = 5 * time.Second

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

	store := repository.NewMemStorage()
	snapshot := repository.NewSnapshot(cfg.fileStoragePath, cfg.storeInterval.interval, store,
		cfg.restore.isRestore, log.With(zap.String("component", "snapshot")))

	var updateStore usecase.MetricUpdateStore = store
	var snapshotDone chan struct{}
	if cfg.storeInterval.interval == 0 {
		updateStore = snapshot
	} else {
		snapshotDone = make(chan struct{})
		go func() {
			defer close(snapshotDone)
			snapshot.Run(ctx)
		}()
	}

	uc := usecase.NewMetricUpdate(updateStore)
	readUC := usecase.NewMetricRead(store)
	r := chi.NewRouter()

	httpLog := log.With(zap.String("component", "http"))
	r.Use(handler.WithLogging(httpLog), handler.CompressionHandler(httpLog))

	r.Post("/update/", handler.UpdateMetricsJSONHandler(&uc, httpLog))
	r.Post("/value/", handler.GetMetricValueJSONHandler(&readUC, httpLog))
	r.Post("/update/{type}/{name}/{value}", handler.UpdateMetricsHandler(&uc, httpLog))
	r.Get("/value/{type}/{name}", handler.GetMetricValueHandler(&readUC))
	r.Get("/", handler.ListMetricsHandler(&readUC))

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

// ---- Configuration parsing ----

type intervalValid struct {
	interval time.Duration
	isSet    bool
}

type restoreValid struct {
	isRestore bool
	isSet     bool
}

type config struct {
	serverAddress   string
	storeInterval   intervalValid // default 300 seconds, 0 means synchronous writes, negative is invalid
	fileStoragePath string
	restore         restoreValid
}

func (i *intervalValid) Set(value string) error {
	intervalInt, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("error parsing interval: %w", err)
	}

	if err := i.UpdateToSecond(intervalInt); err != nil {
		return err
	}
	i.isSet = true
	return nil
}

func (i *intervalValid) UpdateToSecond(sec int) error {
	if sec < 0 {
		return fmt.Errorf("interval must be >= 0, got %d", sec)
	}
	i.interval = time.Duration(sec) * time.Second
	return nil
}

func (r *restoreValid) Set(value string) error {
	restoreBool, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("error parsing restore: %w", err)
	}
	r.isRestore = restoreBool
	r.isSet = true
	return nil
}

func (c config) validation() bool {
	return c.serverAddress != "" && c.storeInterval.isSet && c.fileStoragePath != "" && c.restore.isSet
}

// ---- resolveConfig reads configuration from environment variables and command line arguments.

func resolveConfig() (*config, error) {
	cfg := &config{}

	// environment variables take precedence over command line arguments
	if envServerAddr, ok := os.LookupEnv("ADDRESS"); ok {
		cfg.serverAddress = envServerAddr
	}

	// STORE_INTERVAL: default 300 seconds, 0 means synchronous writes, negative is invalid
	storeIntervalValid := intervalValid{}
	cfg.storeInterval = storeIntervalValid
	if envStoreInterval, ok := os.LookupEnv("STORE_INTERVAL"); ok {
		err := cfg.storeInterval.Set(envStoreInterval)
		if err != nil {
			return nil, errors.New("failed to set STORE_INTERVAL:" + err.Error())
		}
	}

	if envFileStoragePath, ok := os.LookupEnv("FILE_STORAGE_PATH"); ok {
		cfg.fileStoragePath = envFileStoragePath
	}

	rv := restoreValid{}
	cfg.restore = rv
	if envRestore, ok := os.LookupEnv("RESTORE"); ok {
		err := cfg.restore.Set(envRestore)
		if err != nil {
			return nil, errors.New("failed to set RESTORE:" + err.Error())
		}
	}

	if cfg.validation() {
		return cfg, nil
	}

	// parse command line arguments
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Server startup options:")
		fs.PrintDefaults()
	}

	serverAddr := fs.String("a", "localhost:8080", "HTTP server address")
	storeInterval := fs.Int("i", 300, "store interval in seconds (0 makes writes synchronous)")
	fileStoragePath := fs.String("f", "metrics-db.json", "file storage path")
	restoreFlag := fs.Bool("r", true, "restore previously saved metrics on startup")

	if err := fs.Parse(normalizeHelpArg(os.Args[1:])); err != nil {
		return nil, err
	}

	if cfg.serverAddress == "" {
		cfg.serverAddress = *serverAddr
	}
	if !cfg.storeInterval.isSet {
		err := cfg.storeInterval.UpdateToSecond(*storeInterval)
		if err != nil {
			return nil, errors.New("failed to set STORE_INTERVAL from flag:" + err.Error())
		}
	}

	if cfg.fileStoragePath == "" {
		cfg.fileStoragePath = *fileStoragePath
	}

	if !cfg.restore.isSet {
		cfg.restore.isRestore = *restoreFlag
	}

	return cfg, nil
}

func resolveLogLevel() string {
	if envLogLevel, ok := os.LookupEnv("LOG_LEVEL"); ok {
		return envLogLevel
	}
	return "INFO"
}
