package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
)

// metricStoreMock lets tests force Update to fail, which the real
// repository.MemStorage never does — needed to exercise the error-logging
// branches in collectOnce.
type metricStoreMock struct {
	updateErr error
	gauges    map[string]float64
	counters  map[string]int64
}

func (m *metricStoreMock) Update(metric *models.Metrics) error {
	return m.updateErr
}

func (m *metricStoreMock) ListGauges() map[string]float64 {
	return m.gauges
}

func (m *metricStoreMock) ListCounters() map[string]int64 {
	return m.counters
}

// captureLogOutput redirects the standard logger into a buffer for the
// duration of fn and returns everything it wrote.
func captureLogOutput(fn func()) string {
	var buf bytes.Buffer
	orig := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(orig)

	fn()

	return buf.String()
}

func TestBuildUpdateURL_EscapesPathSegments(t *testing.T) {
	metricURL, err := buildUpdateURL("http://localhost:8080", models.Gauge, "Heap/Alloc Value", "12.5")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(metricURL, "/update/gauge/") {
		t.Fatalf("expected update path, got %s", metricURL)
	}

	if !strings.Contains(metricURL, "Heap%2FAlloc%20Value") {
		t.Fatalf("expected escaped metric name in URL, got %s", metricURL)
	}
}

func TestBuildUpdateURL_TrimsTrailingSlashFromBase(t *testing.T) {
	metricURL, err := buildUpdateURL("http://localhost:8080/", models.Counter, "PollCount", "1")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if metricURL != "http://localhost:8080/update/counter/PollCount/1" {
		t.Fatalf("expected trimmed base URL, got %s", metricURL)
	}
}

func TestBuildUpdateURL_ReturnsErrorForInvalidBaseURL(t *testing.T) {
	_, err := buildUpdateURL("http://[::1]:badport", models.Gauge, "Alloc", "1")
	if err == nil {
		t.Fatal("expected error for invalid base URL")
	}
}

func TestSendMetric_ReturnsErrorOnNonOKStatus(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL, 2, 10, repository.NewMemStorage())

	if err := a.sendMetric(models.Gauge, "Alloc", "1"); err == nil {
		t.Fatal("expected error for non-200 response status")
	}
}

func TestSendMetric_ReturnsErrorOnNetworkFailure(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableAddr := ts.URL
	ts.Close()

	a := NewAgent(unreachableAddr, 2, 10, repository.NewMemStorage())

	if err := a.sendMetric(models.Gauge, "Alloc", "1"); err == nil {
		t.Fatal("expected error when server is unreachable")
	}
}

func TestCollectOnceStoresMetrics(t *testing.T) {
	a := NewAgent("http://localhost:8080", 2, 10, repository.NewMemStorage())

	a.collectOnce()
	a.collectOnce()

	gauges := a.store.ListGauges()
	counters := a.store.ListCounters()

	if _, ok := gauges["Alloc"]; !ok {
		t.Fatalf("expected Alloc gauge to be collected")
	}
	if _, ok := gauges["RandomValue"]; !ok {
		t.Fatalf("expected RandomValue gauge to be collected")
	}
	if got := counters["PollCount"]; got != 2 {
		t.Fatalf("expected PollCount=2 after two collects, got %d", got)
	}
}

func TestReportOnceSendsAllMetrics(t *testing.T) {
	a := NewAgent("http://localhost:8080", 2, 10, repository.NewMemStorage())

	g := 12.5
	c := int64(7)
	_ = a.store.Update(&models.Metrics{ID: "TestGauge", MType: models.Gauge, Value: &g})
	_ = a.store.Update(&models.Metrics{ID: "TestCounter", MType: models.Counter, Delta: &c})

	got := make(map[string]bool)
	var mu sync.Mutex
	var handlerErr error

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Method != http.MethodPost {
			handlerErr = fmt.Errorf("expected POST method, got %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "text/plain" {
			handlerErr = fmt.Errorf("expected Content-Type text/plain, got %s", r.Header.Get("Content-Type"))
		}
		got[r.URL.Path] = true
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a.serverAddr = ts.URL
	a.reportOnce()

	mu.Lock()
	defer mu.Unlock()
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}

	if !got["/update/gauge/TestGauge/12.5"] {
		t.Fatalf("expected gauge request path not received")
	}
	if !got["/update/counter/TestCounter/7"] {
		t.Fatalf("expected counter request path not received")
	}
}

func TestCollectOnce_LogsErrorWhenStoreUpdateFails(t *testing.T) {
	a := NewAgent("http://localhost:8080", 2, 10, &metricStoreMock{updateErr: errors.New("store unavailable")})

	output := captureLogOutput(func() {
		a.collectOnce()
	})

	if !strings.Contains(output, "collect gauge error") {
		t.Fatalf("expected gauge collect error to be logged, got %q", output)
	}
	if !strings.Contains(output, "collect counter error") {
		t.Fatalf("expected counter collect error to be logged, got %q", output)
	}
}

func TestReportOnce_LogsErrorWhenSendFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableAddr := ts.URL
	ts.Close()

	a := NewAgent(unreachableAddr, 2, 10, &metricStoreMock{
		gauges:   map[string]float64{"Alloc": 1.5},
		counters: map[string]int64{"PollCount": 1},
	})

	output := captureLogOutput(func() {
		a.reportOnce()
	})

	if !strings.Contains(output, "send gauge error") {
		t.Fatalf("expected gauge send error to be logged, got %q", output)
	}
	if !strings.Contains(output, "send counter error") {
		t.Fatalf("expected counter send error to be logged, got %q", output)
	}
}

func TestAgent_Run_StopsWhenContextIsCancelled(t *testing.T) {
	a := NewAgent("http://localhost:8080", 1, 1, &metricStoreMock{gauges: map[string]float64{}, counters: map[string]int64{}})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected Run to return after context cancellation")
	}
}

func TestAgent_Run_CollectsAndReportsBeforeStopping(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// Built directly (bypassing NewAgent) so poll/report intervals can be
	// sub-second, keeping the test fast and deterministic.
	a := &Agent{
		store:          repository.NewMemStorage(),
		serverAddr:     ts.URL,
		pollInterval:   15 * time.Millisecond,
		reportInterval: 25 * time.Millisecond,
		client:         &http.Client{Timeout: 3 * time.Second},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected Run to return after context deadline")
	}

	mu.Lock()
	defer mu.Unlock()
	if requestCount == 0 {
		t.Fatal("expected at least one metric report to reach the server")
	}
}
