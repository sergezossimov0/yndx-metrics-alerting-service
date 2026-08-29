package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
)

func newTestHandler() (http.Handler, *repository.MemStorage) {
	store := repository.NewMemStorage()
	uc := usecase.NewMetricUpdate(store)
	return UpdateMetricsHandler(&uc), store
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

	gauges := store.ListGuages()
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
			expected:    http.StatusNotFound,
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
			expected:    http.StatusBadRequest,
		},
		{
			name:        "invalid metric type",
			method:      http.MethodPost,
			target:      "/update/bad/Alloc/1",
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
