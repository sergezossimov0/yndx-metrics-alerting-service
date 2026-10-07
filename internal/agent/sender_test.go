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
	updateErr  error
	metrics    []models.Metrics
	subtracted []models.Metrics
}

func (m *metricStoreMock) Update(_ context.Context, metric *models.Metrics) error {
	return m.updateErr
}

func (m *metricStoreMock) Snapshot() []models.Metrics {
	return m.metrics
}

func (m *metricStoreMock) SubtractCounters(_ context.Context, sent []models.Metrics) {
	m.subtracted = sent
}

// newObservedLogger returns a logger that keeps entries in memory, so a test
// can inspect what a component logged without touching any global state.
func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

func gaugeMetric(id string, v float64) models.Metrics {
	return models.Metrics{ID: id, MType: models.Gauge, Value: &v}
}

func counterMetric(id string, d int64) models.Metrics {
	return models.Metrics{ID: id, MType: models.Counter, Delta: &d}
}

// countingServer counts the requests it receives and answers with status.
func countingServer(t *testing.T, status int) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(ts.Close)
	return ts, func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}
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

	if err := a.sendBatchJSON(context.Background(), []models.Metrics{gaugeMetric("Alloc", 1)}); err == nil {
		t.Fatal("expected error when server is unreachable")
	}
}

func TestCollectOnceStoresMetrics(t *testing.T) {
	store := repository.NewMemStorage()
	a := NewAgent("http://localhost:8080", 2*time.Second, 10*time.Second, store, zap.NewNop())

	a.collectOnce(context.Background())
	a.collectOnce(context.Background())

	gauges := store.ListGauges(context.Background())
	counters := store.ListCounters(context.Background())

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

func TestReportOnceJSON_SendsAllMetricsInOneBatch(t *testing.T) {
	store := repository.NewMemStorage()
	g := 12.5
	c := int64(7)
	_ = store.Update(context.Background(), &models.Metrics{ID: "TestGauge", MType: models.Gauge, Value: &g})
	_ = store.Update(context.Background(), &models.Metrics{ID: "TestCounter", MType: models.Counter, Delta: &c})

	var mu sync.Mutex
	requestCount := 0
	got := make(map[string]models.Metrics)
	var handlerErr error

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requestCount++
		if r.URL.Path != "/updates/" {
			handlerErr = fmt.Errorf("expected path /updates/, got %s", r.URL.Path)
		}
		var batch []models.Metrics
		if err := decodeGzipJSONBody(r, &batch); err != nil {
			handlerErr = fmt.Errorf("failed to decode request body: %w", err)
		}
		for _, m := range batch {
			got[m.ID] = m
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, store, zap.NewNop())
	a.reportOnceJSON(context.Background())

	mu.Lock()
	defer mu.Unlock()
	if handlerErr != nil {
		t.Fatal(handlerErr)
	}
	if requestCount != 1 {
		t.Fatalf("expected a single batch request, got %d", requestCount)
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

func TestReportOnceJSON_SubtractsSentCountersAfterSuccess(t *testing.T) {
	ts, _ := countingServer(t, http.StatusOK)
	store := repository.NewMemStorage()
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, store, zap.NewNop())

	a.collectOnce(context.Background())
	a.collectOnce(context.Background())
	a.reportOnceJSON(context.Background())

	if got, _ := store.GetCounter(context.Background(), "PollCount"); got != 0 {
		t.Fatalf("expected PollCount delta to be reset after a successful report, got %d", got)
	}
	if _, ok := store.GetGauge(context.Background(), "Alloc"); !ok {
		t.Fatal("expected gauges to stay in the store")
	}
}

func TestReportOnceJSON_KeepsCountersWhenSendFails(t *testing.T) {
	ts, _ := countingServer(t, http.StatusInternalServerError)
	store := repository.NewMemStorage()
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, store, zap.NewNop())

	a.collectOnce(context.Background())
	a.collectOnce(context.Background())
	a.reportOnceJSON(context.Background())

	if got, _ := store.GetCounter(context.Background(), "PollCount"); got != 2 {
		t.Fatalf("expected PollCount=2 to be kept for the next report, got %d", got)
	}
}

func TestReportOnceJSON_DoesNotSendEmptyBatch(t *testing.T) {
	ts, requests := countingServer(t, http.StatusOK)
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, &metricStoreMock{}, zap.NewNop())

	a.reportOnceJSON(context.Background())

	if n := requests(); n != 0 {
		t.Fatalf("expected no request for an empty batch, got %d", n)
	}
}

func TestCollectOnce_LogsErrorWhenStoreUpdateFails(t *testing.T) {
	log, logs := newObservedLogger()
	a := NewAgent("http://localhost:8080", 2*time.Second, 10*time.Second, &metricStoreMock{updateErr: errors.New("store unavailable")}, log)

	a.collectOnce(context.Background())

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
	store := &metricStoreMock{metrics: []models.Metrics{gaugeMetric("Alloc", 1.5), counterMetric("PollCount", 1)}}
	a := NewAgent(unreachableAddr, 2*time.Second, 10*time.Second, store, log)

	a.reportOnceJSON(context.Background())

	if logs.FilterMessage("send metrics batch error").FilterLevelExact(zapcore.ErrorLevel).Len() == 0 {
		t.Fatalf("expected batch send error to be logged, got %v", logs.All())
	}
	if store.subtracted != nil {
		t.Fatalf("expected counters not to be subtracted after a failed send, got %+v", store.subtracted)
	}
}

func TestReportOnceJSON_SendsNothingWhenContextAlreadyCancelled(t *testing.T) {
	ts, requests := countingServer(t, http.StatusOK)

	log, logs := newObservedLogger()
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, &metricStoreMock{
		metrics: []models.Metrics{gaugeMetric("Alloc", 1.5), counterMetric("PollCount", 1)},
	}, log)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	a.reportOnceJSON(ctx)

	if n := requests(); n != 0 {
		t.Fatalf("expected no requests after cancellation, got %d", n)
	}
	if n := logs.FilterMessage("report interrupted").Len(); n != 1 {
		t.Fatalf("expected a single 'report interrupted' entry, got %d: %v", n, logs.All())
	}
	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Fatalf("expected no error-level logs on cancellation, got %d: %v", n, logs.All())
	}
}

func TestAgent_Run_StopsWhenContextIsCancelled(t *testing.T) {
	a := NewAgent("http://localhost:8080", time.Second, time.Second, &metricStoreMock{}, zap.NewNop())

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
		t.Fatal("expected at least one metrics batch to reach the server")
	}
}
