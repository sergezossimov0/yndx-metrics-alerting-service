package handler

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
)

func newTestHandler() (http.Handler, *repository.MemStorage) {
	store := repository.NewMemStorage()
	uc := usecase.NewMetricUpdate(store)
	readUC := usecase.NewMetricRead(store)
	r := chi.NewRouter()
	r.Post("/update/{type}/{name}/{value}", UpdateMetricsHandler(&uc))
	r.Get("/value/{type}/{name}", GetMetricValueHandler(&readUC))
	r.Get("/", ListMetricsHandler(&readUC))
	return r, store
}

func TestUpdateMetricsHandler_SuccessGauge(t *testing.T) {
	h, store := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/update/gauge/Alloc/123.45", nil)
	req.Header.Set("Content-Type", "text/plain")
	res := httptest.NewRecorder()

	h.ServeHTTP(res, req)

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

func TestUpdateMetricsHandler_SuccessCounterAccumulate(t *testing.T) {
	h, store := newTestHandler()

	req1 := httptest.NewRequest(http.MethodPost, "/update/counter/PollCount/1", nil)
	req1.Header.Set("Content-Type", "text/plain")
	res1 := httptest.NewRecorder()
	h.ServeHTTP(res1, req1)

	req2 := httptest.NewRequest(http.MethodPost, "/update/counter/PollCount/2", nil)
	req2.Header.Set("Content-Type", "text/plain")
	res2 := httptest.NewRecorder()
	h.ServeHTTP(res2, req2)

	if res1.Code != http.StatusOK || res2.Code != http.StatusOK {
		t.Fatalf("expected status 200 for both requests, got %d and %d", res1.Code, res2.Code)
	}

	counters := store.ListCounters()
	if got := counters["PollCount"]; got != 3 {
		t.Fatalf("expected counter PollCount=3, got %d", got)
	}
}

func TestUpdateMetricsHandler_Errors(t *testing.T) {
	h, _ := newTestHandler()

	tests := []struct {
		name        string
		method      string
		target      string
		contentType string
		expected    int
	}{
		{
			name:        "method not allowed by contract",
			method:      http.MethodGet,
			target:      "/update/gauge/Alloc/1",
			contentType: "text/plain",
			expected:    http.StatusMethodNotAllowed,
		},
		{
			name:        "invalid content type",
			method:      http.MethodPost,
			target:      "/update/gauge/Alloc/1",
			contentType: "application/json",
			expected:    http.StatusBadRequest,
		},
		{
			name:        "missing metric name",
			method:      http.MethodPost,
			target:      "/update/gauge//123",
			contentType: "text/plain",
			expected:    http.StatusNotFound,
		},
		{
			name:        "missing metric value",
			method:      http.MethodPost,
			target:      "/update/gauge/Alloc/",
			contentType: "text/plain",
			expected:    http.StatusNotFound,
		},
		{
			name:        "invalid metric type",
			method:      http.MethodPost,
			target:      "/update/bad/Alloc/1",
			contentType: "text/plain",
			expected:    http.StatusBadRequest,
		},
		{
			name:        "missing metric type",
			method:      http.MethodPost,
			target:      "/update//Alloc/1",
			contentType: "text/plain",
			expected:    http.StatusBadRequest,
		},
		{
			name:        "invalid gauge value",
			method:      http.MethodPost,
			target:      "/update/gauge/Alloc/abc",
			contentType: "text/plain",
			expected:    http.StatusBadRequest,
		},
		{
			name:        "invalid counter value",
			method:      http.MethodPost,
			target:      "/update/counter/PollCount/abc",
			contentType: "text/plain",
			expected:    http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, tt.target, nil)
			req.Header.Set("Content-Type", tt.contentType)
			res := httptest.NewRecorder()

			h.ServeHTTP(res, req)

			if res.Code != tt.expected {
				t.Fatalf("expected status %d, got %d", tt.expected, res.Code)
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

func TestUpdateMetricsHandler_ReturnsInternalServerErrorWhenUpdaterFails(t *testing.T) {
	updater := &metricUpdaterMock{err: errors.New("store unavailable")}
	r := chi.NewRouter()
	r.Post("/update/{type}/{name}/{value}", UpdateMetricsHandler(updater))

	req := httptest.NewRequest(http.MethodPost, "/update/gauge/Alloc/1", nil)
	req.Header.Set("Content-Type", "text/plain")
	res := httptest.NewRecorder()

	r.ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, res.Code)
	}
}

func TestValidateMetricRequest(t *testing.T) {
	tests := []struct {
		name    string
		req     *metricRequest
		wantErr error
	}{
		{
			name:    "type missing",
			req:     &metricRequest{Type: "", Name: "Alloc", Value: "1"},
			wantErr: errMetricTypeMissing,
		},
		{
			name:    "value missing",
			req:     &metricRequest{Type: "gauge", Name: "Alloc", Value: ""},
			wantErr: errMetricValueMissing,
		},
		{
			name:    "valid request",
			req:     &metricRequest{Type: "gauge", Name: "Alloc", Value: "1"},
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

func TestConvertMetric(t *testing.T) {
	gaugeValue := 10.5
	counterDelta := int64(5)

	tests := []struct {
		name       string
		req        *metricRequest
		wantErr    bool
		wantMetric *models.Metrics
	}{
		{
			name:       "valid gauge",
			req:        &metricRequest{Type: "gauge", Name: "Alloc", Value: "10.5"},
			wantMetric: &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &gaugeValue},
		},
		{
			name:    "invalid gauge value",
			req:     &metricRequest{Type: "gauge", Name: "Alloc", Value: "abc"},
			wantErr: true,
		},
		{
			name:       "valid counter",
			req:        &metricRequest{Type: "counter", Name: "PollCount", Value: "5"},
			wantMetric: &models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &counterDelta},
		},
		{
			name:    "invalid counter value",
			req:     &metricRequest{Type: "counter", Name: "PollCount", Value: "abc"},
			wantErr: true,
		},
		{
			name:    "unknown metric type",
			req:     &metricRequest{Type: "unknown", Name: "X", Value: "1"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			metric, err := convertMetric(tt.req)

			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("expected nil error, got %v", err)
			}
			assertMetricEqual(t, tt.wantMetric, metric)
		})
	}
}

func assertMetricEqual(t *testing.T, want, got *models.Metrics) {
	t.Helper()

	if got.ID != want.ID {
		t.Fatalf("expected ID %q, got %q", want.ID, got.ID)
	}
	if got.MType != want.MType {
		t.Fatalf("expected MType %q, got %q", want.MType, got.MType)
	}
	if want.Value != nil {
		if got.Value == nil || *got.Value != *want.Value {
			t.Fatalf("expected Value %v, got %v", want.Value, got.Value)
		}
	}
	if want.Delta != nil {
		if got.Delta == nil || *got.Delta != *want.Delta {
			t.Fatalf("expected Delta %v, got %v", want.Delta, got.Delta)
		}
	}
}
