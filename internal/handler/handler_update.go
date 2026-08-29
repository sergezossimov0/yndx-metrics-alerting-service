package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

type metricRequest struct {
	Type  string
	Name  string
	Value string
}

type MetricUpdater interface {
	UpdateMetric(metric *models.Metrics) error
}

func UpdateMetricsHandler(updater MetricUpdater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") {
			http.Error(w, "invalid content type", http.StatusBadRequest)
			return
		}

		metricType := chi.URLParam(r, "type")
		metricName := chi.URLParam(r, "name")
		metricValue := chi.URLParam(r, "value")

		if metricName == "" {
			http.NotFound(w, r)
			return
		}

		metricRequest := &metricRequest{Type: metricType, Name: metricName, Value: metricValue}
		if err := validateMetricRequest(metricRequest); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		metricModel, errConvert := convertMetric(metricRequest)
		if errConvert != nil {
			http.Error(w, errConvert.Error(), http.StatusBadRequest)
			return
		}

		if err := updater.UpdateMetric(metricModel); err != nil {
			http.Error(w, "failed to update metric", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

func validateMetricRequest(mr *metricRequest) error {
	if mr.Type == "" {
		return errMetricTypeMissing
	}
	if mr.Value == "" {
		return errMetricValueMissing
	}
	return nil
}

func convertMetric(mr *metricRequest) (*models.Metrics, error) {
	switch mr.Type {
	case "gauge":
		convValue, err := strconv.ParseFloat(mr.Value, 64)
		if err != nil {
			return nil, errors.New("invalid gauge value")
		}
		return &models.Metrics{
			ID:    mr.Name,
			MType: mr.Type,
			Value: &convValue,
		}, nil
	case "counter":
		convDelta, err := strconv.ParseInt(mr.Value, 10, 64)
		if err != nil {
			return nil, errors.New("invalid counter value")
		}
		return &models.Metrics{
			ID:    mr.Name,
			MType: mr.Type,
			Delta: &convDelta,
		}, nil
	default:
		return nil, errors.New("invalid metric type")
	}
}

var (
	errMetricValueMissing = errors.New("metric value is missing")
	errMetricTypeMissing  = errors.New("metric type is missing")
)
