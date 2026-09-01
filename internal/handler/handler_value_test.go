package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

// emptyMetricReaderStub satisfies MetricReader without ever being called;
// it exists only to exercise the metricName=="" short-circuit, which chi's
// routing never lets a real request reach.
type emptyMetricReaderStub struct{}

func (emptyMetricReaderStub) GetGauge(string) (float64, bool) { return 0, false }
func (emptyMetricReaderStub) GetCounter(string) (int64, bool) { return 0, false }
func (emptyMetricReaderStub) ListGauges() map[string]float64  { return nil }
func (emptyMetricReaderStub) ListCounters() map[string]int64  { return nil }

func TestGetMetricValueHandler(t *testing.T) {
	h, store := newTestHandler()

	g := 10.5
	c := int64(3)
	_ = store.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g})
	_ = store.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &c})

	t.Run("gauge value", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/value/gauge/Alloc", nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
		}
		if strings.TrimSpace(res.Body.String()) != "10.5" {
			t.Fatalf("expected body 10.5, got %q", res.Body.String())
		}
	})

	t.Run("counter value", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/value/counter/PollCount", nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)

		if res.Code != http.StatusOK {
			t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
		}
		if strings.TrimSpace(res.Body.String()) != "3" {
			t.Fatalf("expected body 3, got %q", res.Body.String())
		}
	})

	t.Run("unknown gauge metric", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/value/gauge/Unknown", nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)

		if res.Code != http.StatusNotFound {
			t.Fatalf("expected status %d, got %d", http.StatusNotFound, res.Code)
		}
	})

	t.Run("unknown counter metric", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/value/counter/Unknown", nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)

		if res.Code != http.StatusNotFound {
			t.Fatalf("expected status %d, got %d", http.StatusNotFound, res.Code)
		}
	})

	t.Run("invalid metric type", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/value/invalid/Alloc", nil)
		res := httptest.NewRecorder()
		h.ServeHTTP(res, req)

		if res.Code != http.StatusBadRequest {
			t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
		}
	})
}

func TestGetMetricValueHandler_MissingMetricNameReturnsNotFound(t *testing.T) {
	handlerFunc := GetMetricValueHandler(emptyMetricReaderStub{})

	req := httptest.NewRequest(http.MethodGet, "/value/gauge/", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("type", "gauge")
	rctx.URLParams.Add("name", "")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	res := httptest.NewRecorder()
	handlerFunc(res, req)

	if res.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, res.Code)
	}
}

func TestListMetricsHandler(t *testing.T) {
	h, store := newTestHandler()

	g := 22.3
	c := int64(5)
	_ = store.Update(&models.Metrics{ID: "HeapAlloc", MType: models.Gauge, Value: &g})
	_ = store.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &c})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}

	contentType := res.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/html") {
		t.Fatalf("expected html content type, got %s", contentType)
	}

	body := res.Body.String()
	if !strings.Contains(body, "HeapAlloc") || !strings.Contains(body, "PollCount") {
		t.Fatalf("expected metrics in html body, got %q", body)
	}
}
