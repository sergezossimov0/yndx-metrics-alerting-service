package handler

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mailru/easyjson"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

var (
	errMetricNameMissing   = errors.New("metric name is missing")
	errMetricTypeMissing   = errors.New("metric type is missing")
	errInvalidMetricType   = errors.New("invalid metric type")
	errInvalidGaugeValue   = errors.New("invalid gauge value")
	errInvalidCounterValue = errors.New("invalid counter value")
)

type MetricUpdater interface {
	UpdateMetric(metric *models.Metrics) error
}

func UpdateMetricsJsonHandler(updater MetricUpdater) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get(headerContentType)
		if contentType != "" && !strings.HasPrefix(contentType, contentTypeJSON) {
			http.Error(w, msgInvalidContentType, http.StatusBadRequest)
			return
		}

		var req models.Metrics
		if err := easyjson.UnmarshalFromReader(r.Body, &req); err != nil {
			logger.Log.Error("cannot decode request JSON body", zap.Error(err))
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		if err := validateMetricRequest(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := updater.UpdateMetric(&req); err != nil {
			http.Error(w, msgFailedUpdateMetric, http.StatusInternalServerError)
			return
		}

		w.Header().Set(headerContentType, contentTypeTextPlain)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

func validateMetricRequest(req *models.Metrics) error {
	if req.ID == "" {
		return errMetricNameMissing
	}

	if req.MType == "" {
		return errMetricTypeMissing
	}

	switch req.MType {
	case models.Gauge:
		if req.Value == nil {
			return errInvalidGaugeValue
		}
	case models.Counter:
		if req.Delta == nil {
			return errInvalidCounterValue
		}
	default:
		return errInvalidMetricType
	}

	return nil
}
