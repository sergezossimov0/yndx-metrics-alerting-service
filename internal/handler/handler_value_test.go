package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

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

	t.Run("unknown metric", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/value/gauge/Unknown", nil)
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
