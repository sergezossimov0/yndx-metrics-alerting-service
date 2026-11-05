package agent

import (
	"context"
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
)

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

func TestReportOnceJSON_LogsErrorWhenSendFails(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	unreachableAddr := ts.URL
	ts.Close()

	log, logs := newObservedLogger()
	store := &metricStoreMock{metrics: []models.Metrics{gaugeMetric("Alloc", 1.5), counterMetric("PollCount", 1)}}
	a := NewAgent(unreachableAddr, 2*time.Second, 10*time.Second, store, log)
	a.retry.Delays = testRetryDelays

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

func TestReportOnceJSON_LogsSuccessfulSend(t *testing.T) {
	ts, _ := countingServer(t, http.StatusOK)
	log, logs := newObservedLogger()
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, &metricStoreMock{
		metrics: []models.Metrics{gaugeMetric("Alloc", 1.5), counterMetric("PollCount", 3)},
	}, log)

	a.reportOnceJSON(context.Background())

	sent := logs.FilterMessage("metrics batch sent").FilterLevelExact(zapcore.InfoLevel).All()
	if len(sent) != 1 {
		t.Fatalf("expected one 'metrics batch sent' entry, got %d: %v", len(sent), logs.All())
	}
	fields := sent[0].ContextMap()
	if fields["count"] != int64(2) {
		t.Fatalf("expected count=2 in the log entry, got %v", fields["count"])
	}
	if fields["server"] != ts.URL {
		t.Fatalf("expected server=%s in the log entry, got %v", ts.URL, fields["server"])
	}
	if _, ok := fields["duration"]; !ok {
		t.Fatalf("expected duration in the log entry, got %v", fields)
	}
}

func TestReportOnceJSON_DoesNotLogSuccessWhenSendFails(t *testing.T) {
	ts, _ := countingServer(t, http.StatusBadRequest)
	log, logs := newObservedLogger()
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, &metricStoreMock{
		metrics: []models.Metrics{gaugeMetric("Alloc", 1.5)},
	}, log)

	a.reportOnceJSON(context.Background())

	if n := logs.FilterMessage("metrics batch sent").Len(); n != 0 {
		t.Fatalf("expected no success entry after a failed send, got %d: %v", n, logs.All())
	}
}
