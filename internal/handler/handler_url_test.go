package handler

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func doURLRequest(h http.Handler, method, target string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	req.Header.Set("Content-Type", "text/plain")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	return res
}

func TestUpdateMetricsHandler_SuccessGauge(t *testing.T) {
	h, store := newTestHandler()

	res := doURLRequest(h, http.MethodPost, "/update/gauge/Alloc/123.45")

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
	if body, _ := io.ReadAll(res.Body); string(body) != "OK" {
		t.Fatalf("expected body OK, got %q", string(body))
	}
	if got, ok := store.GetGauge("Alloc"); !ok || got != 123.45 {
		t.Fatalf("expected gauge Alloc=123.45, got %v (present=%v)", got, ok)
	}
}

func TestUpdateMetricsHandler_SuccessCounterAccumulate(t *testing.T) {
	h, store := newTestHandler()

	doURLRequest(h, http.MethodPost, "/update/counter/PollCount/1")
	res := doURLRequest(h, http.MethodPost, "/update/counter/PollCount/2")

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
	if got, ok := store.GetCounter("PollCount"); !ok || got != 3 {
		t.Fatalf("expected counter PollCount=3, got %v (present=%v)", got, ok)
	}
}

func TestUpdateMetricsHandler_Errors(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		target     string
		wantStatus int
	}{
		{"gauge without name", http.MethodPost, "/update/gauge/", http.StatusNotFound},
		{"counter without name", http.MethodPost, "/update/counter/", http.StatusNotFound},
		{"invalid gauge value", http.MethodPost, "/update/gauge/testGauge/none", http.StatusBadRequest},
		{"invalid counter value", http.MethodPost, "/update/counter/testCounter/none", http.StatusBadRequest},
		{"float counter value", http.MethodPost, "/update/counter/testCounter/1.5", http.StatusBadRequest},
		{"unknown type", http.MethodPost, "/update/unknown/testCounter/100", http.StatusBadRequest},
		{"wrong method", http.MethodGet, "/update/gauge/Alloc/1", http.StatusMethodNotAllowed},
		{"unknown path", http.MethodPost, "/updater/counter/testCounter/100", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestHandler()

			res := doURLRequest(h, tt.method, tt.target)

			if res.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, res.Code)
			}
		})
	}
}

func TestUpdateMetricsHandler_InvalidContentTypeReturnsBadRequest(t *testing.T) {
	h, _ := newTestHandler()

	req := httptest.NewRequest(http.MethodPost, "/update/gauge/Alloc/1", nil)
	req.Header.Set("Content-Type", "application/xml")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}
}

func TestGetMetricValueHandler_ReturnsStoredValues(t *testing.T) {
	h, _ := newTestHandler()
	doURLRequest(h, http.MethodPost, "/update/gauge/Alloc/123.45")
	doURLRequest(h, http.MethodPost, "/update/counter/PollCount/7")

	tests := []struct {
		target   string
		wantBody string
	}{
		{"/value/gauge/Alloc", "123.45"},
		{"/value/counter/PollCount", "7"},
	}

	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			res := doURLRequest(h, http.MethodGet, tt.target)

			if res.Code != http.StatusOK {
				t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
			}
			if body, _ := io.ReadAll(res.Body); string(body) != tt.wantBody {
				t.Fatalf("expected body %q, got %q", tt.wantBody, string(body))
			}
		})
	}
}

func TestGetMetricValueHandler_Errors(t *testing.T) {
	tests := []struct {
		name       string
		target     string
		wantStatus int
	}{
		{"unknown gauge", "/value/gauge/Missing", http.StatusNotFound},
		{"unknown counter", "/value/counter/Missing", http.StatusNotFound},
		{"unknown type", "/value/unknown/Alloc", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := newTestHandler()

			res := doURLRequest(h, http.MethodGet, tt.target)

			if res.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, res.Code)
			}
		})
	}
}

func TestURLAndJSONEndpoints_ShareStorage(t *testing.T) {
	h, _ := newTestHandler()
	doURLRequest(h, http.MethodPost, "/update/counter/PollCount/5")

	res := postValueRequest(h, "PollCount", "counter")

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d from JSON /value/, got %d", http.StatusOK, res.Code)
	}
	if body, _ := io.ReadAll(res.Body); string(body) != `{"id":"PollCount","type":"counter","delta":5}` {
		t.Fatalf("unexpected JSON body %q", string(body))
	}
}
