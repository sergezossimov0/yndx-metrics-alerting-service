package handler

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

// Legacy URL-parameter endpoints (iterations 1–6), kept alongside the JSON API:
//   POST /update/{type}/{name}/{value}
//   GET  /value/{type}/{name}

// UpdateMetricsHandler updates a metric passed in the URL path.
func UpdateMetricsHandler(updater MetricUpdater, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contentType := r.Header.Get(headerContentType)
		if contentType != "" && !strings.HasPrefix(contentType, contentTypeTextPlain) {
			http.Error(w, msgInvalidContentType, http.StatusBadRequest)
			return
		}

		metric, err := parseURLMetric(chi.URLParam(r, "type"), chi.URLParam(r, "name"), chi.URLParam(r, "value"))
		if err != nil {
			if errors.Is(err, errMetricNameMissing) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if err := updater.UpdateMetric(metric); err != nil {
			// server error: logged, the client only gets a generic message
			log.Error(msgFailedUpdateMetric,
				zap.String("metric", metric.ID), zap.String("type", metric.MType), zap.Error(err))
			http.Error(w, msgFailedUpdateMetric, http.StatusInternalServerError)
			return
		}

		w.Header().Set(headerContentType, contentTypeTextPlain)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	}
}

// GetMetricValueHandler returns the value of a metric named in the URL path as plain text.
func GetMetricValueHandler(reader MetricReader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		req := models.Metrics{ID: chi.URLParam(r, "name"), MType: chi.URLParam(r, "type")}

		if err := validateMetricTypeRequest(&req, reader); err != nil {
			if errors.Is(err, errMetricNotFound) || errors.Is(err, errMetricNameMissing) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		var value string
		if req.MType == models.Gauge {
			value = strconv.FormatFloat(*req.Value, 'f', -1, 64)
		} else {
			value = strconv.FormatInt(*req.Delta, 10)
		}

		w.Header().Set(headerContentType, contentTypeTextPlain)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(value))
	}
}

// parseURLMetric converts raw URL path parts into a metric, validated the
// same way as a JSON request.
func parseURLMetric(metricType, name, rawValue string) (*models.Metrics, error) {
	metric := &models.Metrics{ID: name, MType: metricType}

	switch metricType {
	case models.Gauge:
		if v, err := strconv.ParseFloat(rawValue, 64); err == nil {
			metric.Value = &v
		}
	case models.Counter:
		if d, err := strconv.ParseInt(rawValue, 10, 64); err == nil {
			metric.Delta = &d
		}
	}

	if err := validateMetricRequest(metric); err != nil {
		return nil, err
	}
	return metric, nil
}
