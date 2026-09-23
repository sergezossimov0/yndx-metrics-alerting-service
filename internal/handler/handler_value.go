package handler

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/mailru/easyjson"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

const (
	headerContentType = "Content-Type"

	contentTypeTextPlain = "text/plain"
	contentTypeJSON      = "application/json"
	contentTypeHTML      = "text/html"

	msgInvalidContentType = "invalid content type"
	msgFailedUpdateMetric = "failed to update metric"
)

var errMetricNotFound = errors.New("metric not found")

type MetricReader interface {
	GetGauge(name string) (float64, bool)
	GetCounter(name string) (int64, bool)
	ListGauges() map[string]float64
	ListCounters() map[string]int64
}

func GetMetricValueJsonHandler(reader MetricReader) http.HandlerFunc {
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

		if err := validateMetricTypeRequest(&req, reader); err != nil {
			if errors.Is(err, errMetricNotFound) {
				http.NotFound(w, r)
				return
			}
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		resBody, err := easyjson.Marshal(req)
		if err != nil {
			logger.Log.Error("cannot encode response JSON body", zap.Error(err))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}

		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(resBody)
	}
}

func validateMetricTypeRequest(req *models.Metrics, reader MetricReader) error {
	if req.ID == "" {
		return errMetricNameMissing
	}

	if req.MType == "" {
		return errMetricTypeMissing
	}

	switch req.MType {
	case models.Gauge:
		value, ok := reader.GetGauge(req.ID)
		if !ok {
			return errMetricNotFound
		}
		req.Value = &value
	case models.Counter:
		value, ok := reader.GetCounter(req.ID)
		if !ok {
			return errMetricNotFound
		}
		req.Delta = &value
	default:
		return errInvalidMetricType
	}

	return nil
}

func ListMetricsHandler(reader MetricReader) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set(headerContentType, contentTypeHTML+"; charset=utf-8")

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
