package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
	"go.uber.org/zap"
)

func newTestHandler() (http.Handler, *repository.MemStorage) {
	return newTestHandlerWithLogger(zap.NewNop())
}

// newTestHandlerWithLogger builds the router with the given logger, so a test
// can inspect what the handlers logged.
func newTestHandlerWithLogger(log *zap.Logger) (http.Handler, *repository.MemStorage) {
	store := repository.NewMemStorage()
	uc := usecase.NewMetricUpdate(store)
	readUC := usecase.NewMetricRead(store)
	r := chi.NewRouter()
	r.Post("/update/", UpdateMetricsJSONHandler(&uc, log))
	r.Post("/value/", GetMetricValueJSONHandler(&readUC, log))
	r.Post("/update/{type}/{name}/{value}", UpdateMetricsHandler(&uc, log))
	r.Get("/value/{type}/{name}", GetMetricValueHandler(&readUC))
	r.Get("/", ListMetricsHandler(&readUC))
	return r, store
}

func postJSONRequest(h http.Handler, target string, contentType string, rawBody []byte) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, target, bytes.NewReader(rawBody))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func postMetric(h http.Handler, metric *models.Metrics) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(metric)
	return postJSONRequest(h, "/update/", "application/json", raw)
}

func TestUpdateMetricsJSONHandler_SuccessGauge(t *testing.T) {
	h, store := newTestHandler()

	value := 123.45
	res := postMetric(h, &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &value})

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}

	body, _ := io.ReadAll(res.Body)
	if string(body) != "OK" {
		t.Fatalf("expected body OK, got %q", string(body))
	}

	gauges := store.ListGauges()
	if got := gauges["Alloc"]; got != 123.45 {
		t.Fatalf("expected gauge Alloc=123.45, got %v", got)
	}
}

func TestUpdateMetricsJSONHandler_SuccessCounterAccumulate(t *testing.T) {
	h, store := newTestHandler()

	delta1 := int64(1)
	delta2 := int64(2)
	res1 := postMetric(h, &models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &delta1})
	res2 := postMetric(h, &models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &delta2})

	if res1.Code != http.StatusOK || res2.Code != http.StatusOK {
		t.Fatalf("expected status 200 for both requests, got %d and %d", res1.Code, res2.Code)
	}

	counters := store.ListCounters()
	if got := counters["PollCount"]; got != 3 {
		t.Fatalf("expected counter PollCount=3, got %d", got)
	}
}

func TestUpdateMetricsJSONHandler_Errors(t *testing.T) {
	h, _ := newTestHandler()

	gaugeValue := 1.0
	validBody, _ := json.Marshal(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &gaugeValue})
	missingTypeBody, _ := json.Marshal(&models.Metrics{ID: "Alloc"})
	gaugeMissingValueBody, _ := json.Marshal(&models.Metrics{ID: "Alloc", MType: models.Gauge})
	counterMissingDeltaBody, _ := json.Marshal(&models.Metrics{ID: "PollCount", MType: models.Counter})
	unknownTypeBody, _ := json.Marshal(&models.Metrics{ID: "Alloc", MType: "unknown"})
	missingNameBody, _ := json.Marshal(&models.Metrics{MType: models.Gauge, Value: &gaugeValue})

	tests := []struct {
		name        string
		method      string
		contentType string
		body        []byte
		expected    int
	}{
		{
			name:        "method not allowed by contract",
			method:      http.MethodGet,
			contentType: "application/json",
			body:        validBody,
			expected:    http.StatusMethodNotAllowed,
		},
		{
			name:        "invalid content type",
			method:      http.MethodPost,
			contentType: "text/plain",
			body:        validBody,
			expected:    http.StatusBadRequest,
		},
		{
			name:        "malformed JSON body",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        []byte("not-json"),
			expected:    http.StatusBadRequest,
		},
		{
			name:        "missing metric type",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        missingTypeBody,
			expected:    http.StatusBadRequest,
		},
		{
			name:        "gauge missing value",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        gaugeMissingValueBody,
			expected:    http.StatusBadRequest,
		},
		{
			name:        "counter missing delta",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        counterMissingDeltaBody,
			expected:    http.StatusBadRequest,
		},
		{
			name:        "unknown metric type",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        unknownTypeBody,
			expected:    http.StatusBadRequest,
		},
		{
			name:        "missing metric name",
			method:      http.MethodPost,
			contentType: "application/json",
			body:        missingNameBody,
			expected:    http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "/update/", bytes.NewReader(tt.body))
			req.Header.Set("Content-Type", tt.contentType)
			res := httptest.NewRecorder()

			h.ServeHTTP(res, req)

			if res.Code != tt.expected {
				t.Fatalf("expected status %d, got %d", tt.expected, res.Code)
			}
		})
	}
}

func TestUpdateMetricsJSONHandler_RejectsIncompleteMetricAndDoesNotStoreIt(t *testing.T) {
	h, store := newTestHandler()

	res := postMetric(h, &models.Metrics{ID: "Alloc", MType: models.Gauge})

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}

	body, _ := io.ReadAll(res.Body)
	if strings.TrimSpace(string(body)) != "invalid gauge value" {
		t.Fatalf("expected only the validation error in the body, got %q", string(body))
	}

	if _, ok := store.ListGauges()["Alloc"]; ok {
		t.Fatal("expected metric not to be stored when validation fails")
	}
}

func TestUpdateMetricsJSONHandler_RejectsUnknownMetricTypeAndDoesNotStoreIt(t *testing.T) {
	h, store := newTestHandler()

	res := postMetric(h, &models.Metrics{ID: "Alloc", MType: "unknown"})

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}

	body, _ := io.ReadAll(res.Body)
	if strings.TrimSpace(string(body)) != "invalid metric type" {
		t.Fatalf("expected only the validation error in the body, got %q", string(body))
	}

	if _, ok := store.ListGauges()["Alloc"]; ok {
		t.Fatal("expected metric not to be stored when validation fails")
	}
}

func TestUpdateMetricsJSONHandler_RejectsMissingNameAndDoesNotStoreIt(t *testing.T) {
	h, store := newTestHandler()

	value := 1.0
	res := postMetric(h, &models.Metrics{MType: models.Gauge, Value: &value})

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}

	body, _ := io.ReadAll(res.Body)
	if strings.TrimSpace(string(body)) != "metric name is missing" {
		t.Fatalf("expected only the validation error in the body, got %q", string(body))
	}

	if _, ok := store.ListGauges()[""]; ok {
		t.Fatal("expected metric not to be stored when validation fails")
	}
}

func TestValidateMetricRequest(t *testing.T) {
	gaugeValue := 1.0
	counterDelta := int64(1)

	tests := []struct {
		name    string
		req     *models.Metrics
		wantErr error
	}{
		{
			name:    "name missing",
			req:     &models.Metrics{MType: models.Gauge, Value: &gaugeValue},
			wantErr: errMetricNameMissing,
		},
		{
			name:    "type missing",
			req:     &models.Metrics{ID: "Alloc"},
			wantErr: errMetricTypeMissing,
		},
		{
			name:    "gauge missing value",
			req:     &models.Metrics{ID: "Alloc", MType: models.Gauge},
			wantErr: errInvalidGaugeValue,
		},
		{
			name:    "counter missing delta",
			req:     &models.Metrics{ID: "PollCount", MType: models.Counter},
			wantErr: errInvalidCounterValue,
		},
		{
			name:    "unknown metric type",
			req:     &models.Metrics{ID: "Alloc", MType: "unknown"},
			wantErr: errInvalidMetricType,
		},
		{
			name:    "valid gauge",
			req:     &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &gaugeValue},
			wantErr: nil,
		},
		{
			name:    "valid counter",
			req:     &models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &counterDelta},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMetricRequest(tt.req)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}
}

type metricUpdaterMock struct {
	err error
}

func (m *metricUpdaterMock) UpdateMetric(_ *models.Metrics) error {
	return m.err
}

func TestUpdateMetricsJSONHandler_ReturnsInternalServerErrorWhenUpdaterFails(t *testing.T) {
	updater := &metricUpdaterMock{err: errors.New("store unavailable")}
	r := chi.NewRouter()
	r.Post("/update/", UpdateMetricsJSONHandler(updater, zap.NewNop()))

	value := 1.0
	raw, _ := json.Marshal(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &value})
	res := postJSONRequest(r, "/update/", "application/json", raw)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, res.Code)
	}
}
