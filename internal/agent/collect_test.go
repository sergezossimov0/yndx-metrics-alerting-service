package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/shirou/gopsutil/v4/cpu"
	"go.uber.org/zap"
)

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

func TestCollectSystemOnce_StoresMemoryAndPerCPUGauges(t *testing.T) {
	store := &metricStoreMock{}
	a := NewAgent("http://localhost:8080", 2*time.Second, 10*time.Second, store, zap.NewNop())

	a.collectSystemOnce(context.Background())

	// one UpdateBatch, so a report never snapshots half of a poll
	if len(store.updates) != 1 {
		t.Fatalf("expected one UpdateBatch, got %d", len(store.updates))
	}
	got := make(map[string]float64)
	for _, m := range store.updates[0] {
		if m.MType != models.Gauge || m.Value == nil {
			t.Fatalf("expected only gauges, got %+v", m)
		}
		got[m.ID] = *m.Value
	}

	if got["TotalMemory"] <= 0 {
		t.Fatalf("expected TotalMemory > 0, got %v", got["TotalMemory"])
	}
	if free, ok := got["FreeMemory"]; !ok || free > got["TotalMemory"] {
		t.Fatalf("expected FreeMemory within TotalMemory %v, got %v (present=%v)", got["TotalMemory"], free, ok)
	}

	nCPU, err := cpu.Counts(true)
	if err != nil {
		t.Fatalf("count CPUs: %v", err)
	}
	for i := 1; i <= nCPU; i++ {
		name := fmt.Sprintf("CPUutilization%d", i)
		v, ok := got[name]
		if !ok || v < 0 || v > 100 {
			t.Fatalf("expected %s in [0, 100], got %v (present=%v)", name, v, ok)
		}
	}
	if want := 2 + nCPU; len(got) != want {
		t.Fatalf("expected %d metrics (2 memory + %d CPU), got %d: %v", want, nCPU, len(got), got)
	}
}

func TestCollectSystemOnce_LogsErrorWhenStoreUpdateFails(t *testing.T) {
	log, logs := newObservedLogger()
	a := NewAgent("http://localhost:8080", 2*time.Second, 10*time.Second, &metricStoreMock{updateErr: errors.New("store unavailable")}, log)

	a.collectSystemOnce(context.Background())

	if logs.FilterMessage("collect system metrics error").Len() != 1 {
		t.Fatalf("expected one collect error to be logged, got %v", logs.All())
	}
}

func TestCollectOnce_LogsErrorWhenStoreUpdateFails(t *testing.T) {
	log, logs := newObservedLogger()
	a := NewAgent("http://localhost:8080", 2*time.Second, 10*time.Second, &metricStoreMock{updateErr: errors.New("store unavailable")}, log)

	a.collectOnce(context.Background())

	if logs.FilterMessage("collect metrics error").Len() != 1 {
		t.Fatalf("expected one collect error to be logged, got %v", logs.All())
	}
}

// A poll is written with a single UpdateBatch, so a report running in the
// other goroutine never snapshots half of it.
func TestCollectOnce_WritesWholePollInOneBatch(t *testing.T) {
	store := &metricStoreMock{}
	a := NewAgent("http://localhost:8080", 2*time.Second, 10*time.Second, store, zap.NewNop())

	a.collectOnce(context.Background())

	if len(store.updates) != 1 {
		t.Fatalf("expected one UpdateBatch call per poll, got %d", len(store.updates))
	}
	byID := make(map[string]models.Metrics)
	for _, m := range store.updates[0] {
		byID[m.ID] = m
	}
	if len(byID) != 29 {
		t.Fatalf("expected 28 gauges and PollCount in the batch, got %d metrics", len(byID))
	}
	if pc := byID["PollCount"]; pc.MType != models.Counter || pc.Delta == nil || *pc.Delta != 1 {
		t.Fatalf("expected PollCount=+1 in the batch, got %+v", pc)
	}
	if alloc, ok := byID["Alloc"]; !ok || alloc.MType != models.Gauge || alloc.Value == nil {
		t.Fatalf("expected gauge Alloc in the batch, got %+v", alloc)
	}
	// every gauge must point to its own value, not to a shared loop variable
	seen := make(map[*float64]string)
	for id, m := range byID {
		if m.Value == nil {
			continue
		}
		if other, dup := seen[m.Value]; dup {
			t.Fatalf("gauges %s and %s share one value pointer", id, other)
		}
		seen[m.Value] = id
	}
}
