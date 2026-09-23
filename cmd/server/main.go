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

	if err := logger.Initialize(resolveLogLevel()); err != nil {
		return err
	}
	defer logger.Log.Sync()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store := repository.NewMemStorage()
	snapshot := repository.NewSnapshot(cfg.fileStoragePath, cfg.storeInterval, store)

	if cfg.restore {
		if err := snapshot.Restore(); err != nil {
			logger.Log.Error("failed to restore metrics snapshot", zap.String("path", cfg.fileStoragePath), zap.Error(err))
		}
	}

	var updateStore repository.UpdateMetricStore = store
	var snapshotDone chan struct{}
	if cfg.storeInterval == 0 {
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

	r.Post("/update/", handler.UpdateMetricsJsonHandler(&uc))
	r.Post("/value/", handler.GetMetricValueJsonHandler(&readUC))
	r.Post("/update/{type}/{name}/{value}", handler.UpdateMetricsHandler(&uc))
	r.Get("/value/{type}/{name}", handler.GetMetricValueHandler(&readUC))
	r.Get("/", handler.ListMetricsHandler(&readUC))

	srv := &http.Server{
		Addr:    cfg.serverAddress,
		Handler: handler.WithLogging(handler.CompressionHandler(r)),
	}

	serverErr := make(chan error, 1)
	go func() {
		logger.Log.Info("Running server", zap.String("address", cfg.serverAddress))
		serverErr <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		logger.Log.Info("Shutting down server")
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

type config struct {
	serverAddress   string
	storeInterval   int
	fileStoragePath string
	restore         bool
}

func resolveConfig() (*config, error) {
	cfg := &config{}

	// environment variables take precedence over command line arguments
	if envServerAddr := os.Getenv("ADDRESS"); envServerAddr != "" {
		cfg.serverAddress = envServerAddr
	}

	var errConvert error
	storeIntervalSet := false
	if envStoreInterval := os.Getenv("STORE_INTERVAL"); envStoreInterval != "" {
		cfg.storeInterval, errConvert = strconv.Atoi(envStoreInterval)
		if errConvert != nil {
			return nil, errors.New("Error parsing env STORE_INTERVAL:" + errConvert.Error())
		}
		storeIntervalSet = true
	}

	if envFileStoragePath := os.Getenv("FILE_STORAGE_PATH"); envFileStoragePath != "" {
		cfg.fileStoragePath = envFileStoragePath
	}

	restoreSet := false
	if envRestore := os.Getenv("RESTORE"); envRestore != "" {
		cfg.restore, errConvert = strconv.ParseBool(envRestore)
		if errConvert != nil {
			return nil, errors.New("Error parsing env RESTORE:" + errConvert.Error())
		}
		restoreSet = true
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
	restore := fs.Bool("r", true, "restore previously saved metrics on startup")

	if err := fs.Parse(normalizeHelpArg(os.Args[1:])); err != nil {
		return nil, err
	}

	if cfg.serverAddress == "" {
		cfg.serverAddress = *serverAddr
	}
	if !storeIntervalSet {
		cfg.storeInterval = *storeInterval
	}
	if cfg.storeInterval < 0 {
		return nil, fmt.Errorf("store interval must be >= 0, got %d", cfg.storeInterval)
	}
	if cfg.fileStoragePath == "" {
		cfg.fileStoragePath = *fileStoragePath
	}
	if !restoreSet {
		cfg.restore = *restore
	}

	return cfg, nil
}

func resolveLogLevel() string {
	if envLogLevel := os.Getenv("LOG_LEVEL"); envLogLevel != "" {
		return envLogLevel
	}
	return "INFO"
}
