package agent

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/signature"
	"go.uber.org/zap"
)

func TestSendMetricJSON_SendsCorrectRequest(t *testing.T) {
	var gotMethod, gotPath, gotContentType, gotContentEncoding string
	var gotBody models.Metrics

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotContentEncoding = r.Header.Get("Content-Encoding")
		_ = decodeGzipJSONBody(r, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())

	value := 12.5
	if err := a.sendMetricJSON(context.Background(), &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &value}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST method, got %s", gotMethod)
	}
	if gotPath != "/update/" {
		t.Fatalf("expected path /update/, got %s", gotPath)
	}
	if gotContentType != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", gotContentType)
	}
	if gotContentEncoding != "gzip" {
		t.Fatalf("expected Content-Encoding gzip, got %s", gotContentEncoding)
	}
	if gotBody.ID != "Alloc" || gotBody.MType != models.Gauge || gotBody.Value == nil || *gotBody.Value != 12.5 {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
}

func TestSendMetricJSON_TrimsTrailingSlashFromServerAddr(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL+"/", 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())

	value := 1.0
	if err := a.sendMetricJSON(context.Background(), &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &value}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/update/" {
		t.Fatalf("expected path /update/ without a doubled slash, got %s", gotPath)
	}
}

func TestSendMetricJSON_ReturnsErrorOnNonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())

	value := 1.0
	if err := a.sendMetricJSON(context.Background(), &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &value}); err == nil {
		t.Fatal("expected error for non-200 response status")
	}
}

func TestSendMetricJSON_ReturnsErrorOnNetworkFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableAddr := ts.URL
	ts.Close()

	a := NewAgent(unreachableAddr, 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())
	a.retry.Delays = testRetryDelays

	value := 1.0
	if err := a.sendMetricJSON(context.Background(), &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &value}); err == nil {
		t.Fatal("expected error when server is unreachable")
	}
}

func TestSendBatchJSON_SendsCorrectRequest(t *testing.T) {
	var gotMethod, gotPath, gotContentType, gotContentEncoding string
	var gotBody []models.Metrics

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		gotContentType = r.Header.Get("Content-Type")
		gotContentEncoding = r.Header.Get("Content-Encoding")
		_ = decodeGzipJSONBody(r, &gotBody)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())

	batch := []models.Metrics{gaugeMetric("Alloc", 12.5), counterMetric("PollCount", 3)}
	if err := a.sendBatchJSON(context.Background(), batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("expected POST method, got %s", gotMethod)
	}
	if gotPath != "/updates/" {
		t.Fatalf("expected path /updates/, got %s", gotPath)
	}
	if gotContentType != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %s", gotContentType)
	}
	if gotContentEncoding != "gzip" {
		t.Fatalf("expected Content-Encoding gzip, got %s", gotContentEncoding)
	}
	if len(gotBody) != 2 ||
		gotBody[0].ID != "Alloc" || gotBody[0].Value == nil || *gotBody[0].Value != 12.5 ||
		gotBody[1].ID != "PollCount" || gotBody[1].Delta == nil || *gotBody[1].Delta != 3 {
		t.Fatalf("unexpected request body: %+v", gotBody)
	}
}

func TestSendBatchJSON_TrimsTrailingSlashFromServerAddr(t *testing.T) {
	var gotPath string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL+"/", 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())

	if err := a.sendBatchJSON(context.Background(), []models.Metrics{gaugeMetric("Alloc", 1)}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if gotPath != "/updates/" {
		t.Fatalf("expected path /updates/ without a doubled slash, got %s", gotPath)
	}
}

func TestSendBatchJSON_ReturnsErrorOnNonOKStatus(t *testing.T) {
	ts, _ := countingServer(t, http.StatusInternalServerError)
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())

	if err := a.sendBatchJSON(context.Background(), []models.Metrics{gaugeMetric("Alloc", 1)}); err == nil {
		t.Fatal("expected error for non-200 response status")
	}
}

func TestSendBatchJSON_ReturnsErrorOnNetworkFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableAddr := ts.URL
	ts.Close()

	a := NewAgent(unreachableAddr, 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())
	a.retry.Delays = testRetryDelays

	if err := a.sendBatchJSON(context.Background(), []models.Metrics{gaugeMetric("Alloc", 1)}); err == nil {
		t.Fatal("expected error when server is unreachable")
	}
}

// signatureServer records the HashSHA256 header and the decompressed body of
// the last request it receives.
func signatureServer(t *testing.T) (*httptest.Server, func() (sig string, body []byte)) {
	t.Helper()
	var (
		mu      sync.Mutex
		gotSig  string
		gotBody []byte
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer zr.Close()
		body, err := io.ReadAll(zr)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		mu.Lock()
		gotSig = r.Header.Get(signature.Header)
		gotBody = body
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts, func() (string, []byte) {
		mu.Lock()
		defer mu.Unlock()
		return gotSig, gotBody
	}
}

func TestSendBatchJSON_SignsUncompressedBodyWhenKeySet(t *testing.T) {
	ts, last := signatureServer(t)
	key := []byte("secret")
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop(), WithSignKey(key))

	if err := a.sendBatchJSON(context.Background(), []models.Metrics{gaugeMetric("Alloc", 1.5)}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	sig, body := last()
	if sig == "" {
		t.Fatalf("expected %s header to be set", signature.Header)
	}
	if !signature.Verify(body, key, sig) {
		t.Fatalf("expected %s to sign the uncompressed body %q, got %s", signature.Header, body, sig)
	}
}

func TestSendBatchJSON_DoesNotSignWithoutKey(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{name: "no option"},
		{name: "empty key", opts: []Option{WithSignKey([]byte(""))}},
		{name: "nil key", opts: []Option{WithSignKey(nil)}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts, last := signatureServer(t)
			a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop(), tt.opts...)

			if err := a.sendBatchJSON(context.Background(), []models.Metrics{gaugeMetric("Alloc", 1.5)}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if sig, _ := last(); sig != "" {
				t.Fatalf("expected no %s header without a key, got %q", signature.Header, sig)
			}
		})
	}
}
