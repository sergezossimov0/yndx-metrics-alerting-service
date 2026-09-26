package agent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// decodeGzipJSONBody decompresses a gzip-encoded request body and decodes it as JSON.
func decodeGzipJSONBody(r *http.Request, out interface{}) error {
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		return err
	}
	defer zr.Close()
	return json.NewDecoder(zr).Decode(out)
}

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

// newObservedLogger returns a logger that keeps entries in memory, so a test
// can inspect what a component logged without touching any global state.
func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

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

	value := 1.0
	if err := a.sendMetricJSON(context.Background(), &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &value}); err == nil {
		t.Fatal("expected error when server is unreachable")
	}
}

func TestCollectOnceStoresMetrics(t *testing.T) {
	a := NewAgent("http://localhost:8080", 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())

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

func TestReportOnceJSON_SendsAllMetricsAsJSON(t *testing.T) {
	a := NewAgent("http://localhost:8080", 2*time.Second, 10*time.Second, repository.NewMemStorage(), zap.NewNop())

	g := 12.5
	c := int64(7)
	_ = a.store.Update(&models.Metrics{ID: "TestGauge", MType: models.Gauge, Value: &g})
	_ = a.store.Update(&models.Metrics{ID: "TestCounter", MType: models.Counter, Delta: &c})

	got := make(map[string]models.Metrics)
	var mu sync.Mutex
	var handlerErr error

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		if r.Method != http.MethodPost {
			handlerErr = fmt.Errorf("expected POST method, got %s", r.Method)
		}
		if r.URL.Path != "/update/" {
			handlerErr = fmt.Errorf("expected path /update/, got %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			handlerErr = fmt.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Content-Encoding") != "gzip" {
			handlerErr = fmt.Errorf("expected Content-Encoding gzip, got %s", r.Header.Get("Content-Encoding"))
		}
		var m models.Metrics
		if err := decodeGzipJSONBody(r, &m); err != nil {
			handlerErr = fmt.Errorf("failed to decode request body: %w", err)
		}
		got[m.ID] = m
		mu.Unlock()

		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a.serverAddr = ts.URL
	a.reportOnceJSON(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}

	gauge, ok := got["TestGauge"]
	if !ok || gauge.MType != models.Gauge || gauge.Value == nil || *gauge.Value != 12.5 {
		t.Fatalf("expected gauge TestGauge=12.5 to be reported, got %+v (present=%v)", gauge, ok)
	}
	counter, ok := got["TestCounter"]
	if !ok || counter.MType != models.Counter || counter.Delta == nil || *counter.Delta != 7 {
		t.Fatalf("expected counter TestCounter=7 to be reported, got %+v (present=%v)", counter, ok)
	}
}

func TestCollectOnce_LogsErrorWhenStoreUpdateFails(t *testing.T) {
	log, logs := newObservedLogger()
	a := NewAgent("http://localhost:8080", 2*time.Second, 10*time.Second, &metricStoreMock{updateErr: errors.New("store unavailable")}, log)

	a.collectOnce()

	if logs.FilterMessage("collect gauge error").Len() == 0 {
		t.Fatalf("expected gauge collect error to be logged, got %v", logs.All())
	}
	if logs.FilterMessage("collect counter error").Len() == 0 {
		t.Fatalf("expected counter collect error to be logged, got %v", logs.All())
	}
}

func TestReportOnceJSON_LogsErrorWhenSendFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableAddr := ts.URL
	ts.Close()

	log, logs := newObservedLogger()
	a := NewAgent(unreachableAddr, 2*time.Second, 10*time.Second, &metricStoreMock{
		gauges:   map[string]float64{"Alloc": 1.5},
		counters: map[string]int64{"PollCount": 1},
	}, log)

	a.reportOnceJSON(context.Background())

	if logs.FilterMessage("send gauge error").FilterLevelExact(zapcore.ErrorLevel).Len() == 0 {
		t.Fatalf("expected gauge send error to be logged, got %v", logs.All())
	}
	if logs.FilterMessage("send counter error").FilterLevelExact(zapcore.ErrorLevel).Len() == 0 {
		t.Fatalf("expected counter send error to be logged, got %v", logs.All())
	}
}

func TestReportOnceJSON_SendsNothingWhenContextAlreadyCancelled(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	log, logs := newObservedLogger()
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, &metricStoreMock{
		gauges:   map[string]float64{"Alloc": 1.5, "HeapAlloc": 2.5},
		counters: map[string]int64{"PollCount": 1},
	}, log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a.reportOnceJSON(ctx)

	mu.Lock()
	defer mu.Unlock()
	if requestCount != 0 {
		t.Fatalf("expected no requests after cancellation, got %d", requestCount)
	}
	if n := logs.FilterMessage("report interrupted").Len(); n != 1 {
		t.Fatalf("expected a single 'report interrupted' entry, got %d: %v", n, logs.All())
	}
	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Fatalf("expected no error-level logs on cancellation, got %d: %v", n, logs.All())
	}
}

func TestReportOnceJSON_StopsAfterCancellationMidReport(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	requestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()
		// simulate a shutdown signal arriving while the first metric is in flight
		cancel()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	log, logs := newObservedLogger()
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, &metricStoreMock{
		gauges:   map[string]float64{"Alloc": 1.5, "HeapAlloc": 2.5, "Sys": 3.5},
		counters: map[string]int64{"PollCount": 1},
	}, log)

	a.reportOnceJSON(ctx)

	mu.Lock()
	defer mu.Unlock()
	if requestCount != 1 {
		t.Fatalf("expected report to stop after the in-flight request, got %d requests", requestCount)
	}
	if n := logs.FilterMessage("report interrupted").Len(); n != 1 {
		t.Fatalf("expected a single 'report interrupted' entry, got %d: %v", n, logs.All())
	}
	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Fatalf("expected no error-level logs on cancellation, got %d: %v", n, logs.All())
	}
}

func TestAgent_Run_StopsWhenContextIsCancelled(t *testing.T) {
	a := NewAgent("http://localhost:8080", time.Second, time.Second, &metricStoreMock{gauges: map[string]float64{}, counters: map[string]int64{}}, zap.NewNop())

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

	// sub-second intervals keep the test fast and deterministic
	a := NewAgent(ts.URL, 15*time.Millisecond, 25*time.Millisecond, repository.NewMemStorage(), zap.NewNop())

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
