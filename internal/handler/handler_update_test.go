package handler

import (
	"bytes"
	"compress/gzip"
	"context"
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
	r.Post("/updates/", UpdateMetricsBatchJSONHandler(&uc, log))
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

	gauges := store.ListGauges(context.Background())
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

	counters := store.ListCounters(context.Background())
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

	if _, ok := store.ListGauges(context.Background())["Alloc"]; ok {
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

	if _, ok := store.ListGauges(context.Background())["Alloc"]; ok {
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

	if _, ok := store.ListGauges(context.Background())[""]; ok {
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

func (m *metricUpdaterMock) UpdateMetric(_ context.Context, _ *models.Metrics) error {
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

func postBatch(h http.Handler, batch []models.Metrics) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(batch)
	return postJSONRequest(h, "/updates/", "application/json", raw)
}

func TestUpdateMetricsBatchJSONHandler_StoresAllMetrics(t *testing.T) {
	h, store := newTestHandler()
	g := 1.5
	d1, d2 := int64(2), int64(3)

	res := postBatch(h, []models.Metrics{
		{ID: "Alloc", MType: models.Gauge, Value: &g},
		{ID: "PollCount", MType: models.Counter, Delta: &d1},
		{ID: "PollCount", MType: models.Counter, Delta: &d2},
	})

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, res.Code, res.Body.String())
	}
	if got, ok := store.GetGauge(context.Background(), "Alloc"); !ok || got != 1.5 {
		t.Fatalf("expected Alloc=1.5, got %v (present=%v)", got, ok)
	}
	if got, _ := store.GetCounter(context.Background(), "PollCount"); got != 5 {
		t.Fatalf("expected duplicate counters to add up to 5, got %d", got)
	}
}

func TestUpdateMetricsBatchJSONHandler_AcceptsGzipBody(t *testing.T) {
	store := repository.NewMemStorage()
	uc := usecase.NewMetricUpdate(store)
	r := chi.NewRouter()
	r.Use(CompressionHandler(zap.NewNop()))
	r.Post("/updates/", UpdateMetricsBatchJSONHandler(&uc, zap.NewNop()))

	g := 2.5
	raw, _ := json.Marshal([]models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g}})
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	_, _ = gz.Write(raw)
	_ = gz.Close()

	req := httptest.NewRequest(http.MethodPost, "/updates/", &buf)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	res := httptest.NewRecorder()
	r.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, res.Code, res.Body.String())
	}
	if got, ok := store.GetGauge(context.Background(), "Alloc"); !ok || got != 2.5 {
		t.Fatalf("expected Alloc=2.5, got %v (present=%v)", got, ok)
	}
}

func TestUpdateMetricsBatchJSONHandler_Errors(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "invalid content type", contentType: "text/plain", body: `[]`},
		{name: "invalid JSON", contentType: "application/json", body: `[{"id":`},
		{name: "single object instead of array", contentType: "application/json", body: `{"id":"Alloc","type":"gauge","value":1}`},
		{name: "empty batch", contentType: "application/json", body: `[]`},
		{name: "null batch", contentType: "application/json", body: `null`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestHandler()
			res := postJSONRequest(h, "/updates/", tt.contentType, []byte(tt.body))
			if res.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
			}
		})
	}
}

func TestUpdateMetricsBatchJSONHandler_RejectsWholeBatchWhenOneMetricIsInvalid(t *testing.T) {
	h, store := newTestHandler()
	g := 1.5

	res := postBatch(h, []models.Metrics{
		{ID: "Alloc", MType: models.Gauge, Value: &g},
		{ID: "PollCount", MType: models.Counter}, // missing delta
	})

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}
	if !strings.Contains(res.Body.String(), "PollCount") {
		t.Fatalf("expected the error to name the invalid metric, got %q", res.Body.String())
	}
	if len(store.Snapshot()) != 0 {
		t.Fatalf("expected nothing to be stored, got %+v", store.Snapshot())
	}
}

type metricBatchUpdaterMock struct {
	err error
}

func (m *metricBatchUpdaterMock) UpdateMetrics(_ context.Context, _ []models.Metrics) error {
	return m.err
}

func TestUpdateMetricsBatchJSONHandler_ReturnsInternalServerErrorWhenUpdaterFails(t *testing.T) {
	r := chi.NewRouter()
	r.Post("/updates/", UpdateMetricsBatchJSONHandler(&metricBatchUpdaterMock{err: errors.New("store unavailable")}, zap.NewNop()))

	value := 1.0
	raw, _ := json.Marshal([]models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &value}})
	res := postJSONRequest(r, "/updates/", "application/json", raw)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, res.Code)
	}
}
