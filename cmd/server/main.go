package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"

	"github.com/go-chi/chi/v5"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/handler"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
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
	fs := flag.NewFlagSet(os.Args[0], flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: %s [options]\n", os.Args[0])
		fmt.Fprintln(os.Stderr, "Server startup options:")
		fs.PrintDefaults()
	}

	serverAddr := fs.String("a", "localhost:8080", "HTTP server address")
	if err := fs.Parse(normalizeHelpArg(os.Args[1:])); err != nil {
		return err
	}

	store := repository.NewMemStorage()
	uc := usecase.NewMetricUpdate(store)
	readUC := usecase.NewMetricRead(store)
	r := chi.NewRouter()

	r.Post("/update/{type}/{name}/{value}", handler.UpdateMetricsHandler(&uc))
	r.Get("/value/{type}/{name}", handler.GetMetricValueHandler(&readUC))
	r.Get("/", handler.ListMetricsHandler(&readUC))

	log.Printf("server starting on addr=%s", *serverAddr)
	return http.ListenAndServe(*serverAddr, r)
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
