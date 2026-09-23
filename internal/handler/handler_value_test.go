package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
)

func postValueRequest(h http.Handler, metricID, metricType string) *httptest.ResponseRecorder {
	raw, _ := json.Marshal(&models.Metrics{ID: metricID, MType: metricType})
	return postJSONRequest(h, "/value/", "application/json", raw)
}

func TestGetMetricValueJSONHandler_ReturnsGaugeValue(t *testing.T) {
	h, store := newTestHandler()
	g := 10.5
	_ = store.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g})

	res := postValueRequest(h, "Alloc", models.Gauge)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}

	var got models.Metrics
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if got.Value == nil || *got.Value != 10.5 {
		t.Fatalf("expected value 10.5, got %+v", got)
	}
}

func TestGetMetricValueJSONHandler_ReturnsCounterValue(t *testing.T) {
	h, store := newTestHandler()
	c := int64(3)
	_ = store.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &c})

	res := postValueRequest(h, "PollCount", models.Counter)
	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}

	var got models.Metrics
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response body: %v", err)
	}
	if got.Delta == nil || *got.Delta != 3 {
		t.Fatalf("expected delta 3, got %+v", got)
	}
}

func TestGetMetricValueJSONHandler_Errors(t *testing.T) {
	h, _ := newTestHandler()

	tests := []struct {
		name       string
		metricID   string
		metricType string
		wantStatus int
		wantBody   string // empty means "don't check the body"
	}{
		{
			name:       "unknown gauge metric",
			metricID:   "Unknown",
			metricType: models.Gauge,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "unknown counter metric",
			metricID:   "Unknown",
			metricType: models.Counter,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "invalid metric type",
			metricID:   "Alloc",
			metricType: "invalid",
			wantStatus: http.StatusBadRequest,
			wantBody:   "invalid metric type",
		},
		{
			name:       "missing metric type",
			metricID:   "Alloc",
			metricType: "",
			wantStatus: http.StatusBadRequest,
			wantBody:   "metric type is missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := postValueRequest(h, tt.metricID, tt.metricType)

			if res.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, res.Code)
			}
			if tt.wantBody != "" && strings.TrimSpace(res.Body.String()) != tt.wantBody {
				t.Fatalf("expected body %q, got %q", tt.wantBody, res.Body.String())
			}
		})
	}
}

func TestGetMetricValueJSONHandler_InvalidContentTypeReturnsBadRequest(t *testing.T) {
	h, _ := newTestHandler()

	res := postJSONRequest(h, "/value/", "text/plain", []byte(`{"id":"Alloc","type":"gauge"}`))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}
}

func TestValidateMetricTypeRequest(t *testing.T) {
	store := repository.NewMemStorage()
	g := 1.5
	c := int64(2)
	_ = store.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g})
	_ = store.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &c})

	tests := []struct {
		name    string
		req     *models.Metrics
		wantErr error
	}{
		{
			name:    "name missing",
			req:     &models.Metrics{MType: models.Gauge},
			wantErr: errMetricNameMissing,
		},
		{
			name:    "type missing",
			req:     &models.Metrics{ID: "Alloc"},
			wantErr: errMetricTypeMissing,
		},
		{
			name:    "unknown type",
			req:     &models.Metrics{ID: "Alloc", MType: "unknown"},
			wantErr: errInvalidMetricType,
		},
		{
			name:    "gauge not found",
			req:     &models.Metrics{ID: "Missing", MType: models.Gauge},
			wantErr: errMetricNotFound,
		},
		{
			name:    "counter not found",
			req:     &models.Metrics{ID: "Missing", MType: models.Counter},
			wantErr: errMetricNotFound,
		},
		{
			name:    "valid gauge",
			req:     &models.Metrics{ID: "Alloc", MType: models.Gauge},
			wantErr: nil,
		},
		{
			name:    "valid counter",
			req:     &models.Metrics{ID: "PollCount", MType: models.Counter},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMetricTypeRequest(tt.req, store)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("expected error %v, got %v", tt.wantErr, err)
			}
		})
	}

	t.Run("valid gauge populates value", func(t *testing.T) {
		req := &models.Metrics{ID: "Alloc", MType: models.Gauge}
		if err := validateMetricTypeRequest(req, store); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Value == nil || *req.Value != 1.5 {
			t.Fatalf("expected value 1.5, got %+v", req)
		}
	})

	t.Run("valid counter populates delta", func(t *testing.T) {
		req := &models.Metrics{ID: "PollCount", MType: models.Counter}
		if err := validateMetricTypeRequest(req, store); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if req.Delta == nil || *req.Delta != 2 {
			t.Fatalf("expected delta 2, got %+v", req)
		}
	})
}

func TestGetMetricValueJSONHandler_MissingMetricNameReturnsBadRequest(t *testing.T) {
	h, _ := newTestHandler()

	res := postValueRequest(h, "", models.Gauge)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}
	if strings.TrimSpace(res.Body.String()) != "metric name is missing" {
		t.Fatalf("expected only the error message body, got %q", res.Body.String())
	}
}

func TestGetMetricValueJSONHandler_MalformedJSONReturnsBadRequest(t *testing.T) {
	h, _ := newTestHandler()

	res := postJSONRequest(h, "/value/", "application/json", []byte("not-json"))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
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
