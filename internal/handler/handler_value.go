package handler

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"

	"github.com/go-chi/chi/v5"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

type MetricReader interface {
	GetGauge(name string) (float64, bool)
	GetCounter(name string) (int64, bool)
	ListGauges() map[string]float64
	ListCounters() map[string]int64
}

func GetMetricValueHandler(reader MetricReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		metricType := chi.URLParam(r, "type")
		metricName := chi.URLParam(r, "name")

		if metricName == "" {
			http.NotFound(w, r)
			return
		}

		w.Header().Set("Content-Type", "text/plain")

		switch metricType {
		case models.Gauge:
			value, ok := reader.GetGauge(metricName)
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(strconv.FormatFloat(value, 'f', -1, 64)))
		case models.Counter:
			value, ok := reader.GetCounter(metricName)
			if !ok {
				http.NotFound(w, r)
				return
			}
			_, _ = w.Write([]byte(strconv.FormatInt(value, 10)))
		default:
			http.Error(w, "invalid metric type", http.StatusBadRequest)
		}
	}
}

func ListMetricsHandler(reader MetricReader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		gauges := reader.ListGauges()
		counters := reader.ListCounters()

		gaugeNames := make([]string, 0, len(gauges))
		for name := range gauges {
			gaugeNames = append(gaugeNames, name)
		}
		sort.Strings(gaugeNames)

		counterNames := make([]string, 0, len(counters))
		for name := range counters {
			counterNames = append(counterNames, name)
		}
		sort.Strings(counterNames)

		_, _ = w.Write([]byte("<html><body><h1>Metrics</h1><ul>"))
		for _, name := range gaugeNames {
			_, _ = w.Write([]byte(fmt.Sprintf("<li>gauge %s = %s</li>", name, strconv.FormatFloat(gauges[name], 'f', -1, 64))))
		}
		for _, name := range counterNames {
			_, _ = w.Write([]byte(fmt.Sprintf("<li>counter %s = %d</li>", name, counters[name])))
		}
		_, _ = w.Write([]byte("</ul></body></html>"))
	}
}
