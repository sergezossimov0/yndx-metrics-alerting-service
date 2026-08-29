package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"
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
	r := chi.NewRouter()

	r.Post("/update/{type}/{name}/{value}", handler.UpdateMetricsHandler(&uc))
	r.Get("/value/{type}/{name}", handler.GetMetricValueHandler(store))
	r.Get("/", handler.ListMetricsHandler(store))

	return http.ListenAndServe(":8080", r)
}
