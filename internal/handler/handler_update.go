package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

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
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}

		if !strings.HasPrefix(r.Header.Get("Content-Type"), "text/plain") {
			http.Error(w, "invalid content type", http.StatusBadRequest)
			return
		}

		metricRequest, err := parseUpdatePath(r.URL.Path)
		if err != nil {
			switch {
			case errors.Is(err, errMetricNameMissing):
				http.NotFound(w, r)
			default:
				http.Error(w, err.Error(), http.StatusBadRequest)
			}
			return
		}

		metricModel, errConvert := validateMetric(metricRequest)
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

func validateMetric(mr *metricRequest) (*models.Metrics, error) {
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
	errMetricNameMissing  = errors.New("metric name is missing")
	errBadPathFormat      = errors.New("bad path format")
	errMetricValueMissing = errors.New("metric value is missing")
	errMetricTypeMissing  = errors.New("metric type is missing")
)

// Expected path: /update/{type}/{name}/{value}
func parseUpdatePath(path string) (metric *metricRequest, err error) {
	if !strings.HasPrefix(path, "/update/") {
		return nil, errBadPathFormat
	}

	rest := strings.TrimPrefix(path, "/update/")
	parts := strings.Split(rest, "/")

	if len(parts) != 3 {
		return nil, errBadPathFormat
	}

	metricType := parts[0]
	metricName := parts[1]
	metricValue := parts[2]

	if metricType == "" {
		return nil, errMetricTypeMissing
	}

	if metricName == "" {
		return nil, errMetricNameMissing
	}

	if metricValue == "" {
		return nil, errMetricValueMissing
	}

	m := &metricRequest{
		Type:  metricType,
		Name:  metricName,
		Value: metricValue,
	}

	return m, nil
}
