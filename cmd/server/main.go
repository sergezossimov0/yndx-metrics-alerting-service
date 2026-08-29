package main

import (
	"net/http"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/handler"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
)

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}

func run() error {
	store := repository.NewMemStorage()
	uc := usecase.NewMetricUpdate(store)
	mux := http.NewServeMux()

	mux.HandleFunc("POST /update/{type}/{name}/{value}", handler.UpdateMetricsHandler(&uc))

	return http.ListenAndServe(":8080", mux)
}
