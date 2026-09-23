package main

import (
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/handler"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
	"go.uber.org/zap"
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
	serverAddr, err := resolveServerAddr()
	if err != nil {
		return err
	}

	if err := logger.Initialize(resolveLogLevel()); err != nil {
		return err
	}
	defer logger.Log.Sync()

	store := repository.NewMemStorage()
	uc := usecase.NewMetricUpdate(store)
	readUC := usecase.NewMetricRead(store)
	r := chi.NewRouter()

	r.Post("/update/", handler.UpdateMetricsJsonHandler(&uc))
	r.Post("/value/", handler.GetMetricValueJsonHandler(&readUC))
	r.Get("/", handler.ListMetricsHandler(&readUC))

	logger.Log.Info("Running server", zap.String("address", serverAddr))
	return http.ListenAndServe(serverAddr, handler.WithLogging(r))
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

func resolveServerAddr() (string, error) {
	// environment variable takes precedence over command line argument
	if envServerAddr := os.Getenv("ADDRESS"); envServerAddr != "" {
		return envServerAddr, nil
	}

	// parse command line argument
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Server startup options:")
		fs.PrintDefaults()
	}

	serverAddr := fs.String("a", "localhost:8080", "HTTP server address")
	if err := fs.Parse(normalizeHelpArg(os.Args[1:])); err != nil {
		return "", err
	}

	return *serverAddr, nil
}

func resolveLogLevel() string {
	if envLogLevel := os.Getenv("LOG_LEVEL"); envLogLevel != "" {
		return envLogLevel
	}
	return "INFO"
}
