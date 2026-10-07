package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/mailru/easyjson"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

var (
	errMetricNameMissing   = errors.New("metric name is missing")
	errMetricTypeMissing   = errors.New("metric type is missing")
	errInvalidMetricType   = errors.New("invalid metric type")
	errInvalidGaugeValue   = errors.New("invalid gauge value")
	errInvalidCounterValue = errors.New("invalid counter value")
	errEmptyBatch          = errors.New("empty metrics batch")
)

const msgFailedUpdateMetrics = "failed to update metrics"

type MetricUpdater interface {
	UpdateMetric(ctx context.Context, metric *models.Metrics) error
}

func UpdateMetricsJSONHandler(updater MetricUpdater, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get(headerContentType)
		if contentType != "" && !strings.HasPrefix(contentType, contentTypeJSON) {
			http.Error(w, msgInvalidContentType, http.StatusBadRequest)
			return
		}

		var req models.Metrics
		if err := easyjson.UnmarshalFromReader(r.Body, &req); err != nil {
			// client error: not logged, the access log already records the 400
			http.Error(w, msgInvalidJSONBody, http.StatusBadRequest)
			return
		}

		if err := validateMetricRequest(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		if err := updater.UpdateMetric(ctx, &req); err != nil {
			// server error: logged, the client only gets a generic message
			log.Error(msgFailedUpdateMetric,
				zap.String("metric", req.ID), zap.String("type", req.MType), zap.Error(err))
			http.Error(w, msgFailedUpdateMetric, http.StatusInternalServerError)
			return
		}

		w.Header().Set(headerContentType, contentTypeTextPlain)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

type MetricBatchUpdater interface {
	UpdateMetrics(ctx context.Context, metrics []models.Metrics) error
}

// UpdateMetricsBatchJSONHandler handles POST /updates/ with a JSON array of
// metrics. The batch is all-or-nothing: if any metric is invalid, the request
// is rejected with 400 and nothing is stored. An empty batch is a client
// error too, since there is nothing to update.
func UpdateMetricsBatchJSONHandler(updater MetricBatchUpdater, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get(headerContentType)
		if contentType != "" && !strings.HasPrefix(contentType, contentTypeJSON) {
			http.Error(w, msgInvalidContentType, http.StatusBadRequest)
			return
		}

		var req models.MetricsList
		if err := easyjson.UnmarshalFromReader(r.Body, &req); err != nil {
			http.Error(w, msgInvalidJSONBody, http.StatusBadRequest)
			return
		}

		if len(req) == 0 {
			http.Error(w, errEmptyBatch.Error(), http.StatusBadRequest)
			return
		}

		for i := range req {
			if err := validateMetricRequest(&req[i]); err != nil {
				http.Error(w, fmt.Sprintf("metric #%d %q: %v", i, req[i].ID, err), http.StatusBadRequest)
				return
			}
		}

		if err := updater.UpdateMetrics(r.Context(), req); err != nil {
			log.Error(msgFailedUpdateMetrics, zap.Int("count", len(req)), zap.Error(err))
			http.Error(w, msgFailedUpdateMetrics, http.StatusInternalServerError)
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
