package usecase

import (
	"testing"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"go.uber.org/zap"
)

// The storage interfaces are declared here, in their consumer, so the
// repository types no longer name them. These compile-time checks keep the
// real storages in sync with what usecase expects.
var (
	_ MetricReadStore   = (*repository.MemStorage)(nil)
	_ MetricUpdateStore = (*repository.MemStorage)(nil)
	_ MetricUpdateStore = (*repository.Snapshot)(nil)
)

func TestUsecases_WorkOverRealMemStorage(t *testing.T) {
	store := repository.NewMemStorage()
	update := NewMetricUpdate(store)
	read := NewMetricRead(store)

	g := 1.5
	if err := update.UpdateMetric(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	d := int64(3)
	if err := update.UpdateMetric(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &d}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, ok := read.GetGauge("Alloc"); !ok || got != 1.5 {
		t.Fatalf("expected Alloc=1.5, got %v (present=%v)", got, ok)
	}
	if got, ok := read.GetCounter("PollCount"); !ok || got != 3 {
		t.Fatalf("expected PollCount=3, got %v (present=%v)", got, ok)
	}
	if len(read.ListGauges()) != 1 || len(read.ListCounters()) != 1 {
		t.Fatalf("expected one gauge and one counter, got %v and %v", read.ListGauges(), read.ListCounters())
	}
}

func TestMetricUpdate_WorksOverSnapshotInSyncMode(t *testing.T) {
	// with STORE_INTERVAL=0 the server passes a Snapshot as the update store
	store := repository.NewMemStorage()
	snapshot := repository.NewSnapshot(t.TempDir()+"/snapshot.json", 0, store, false, zap.NewNop())
	update := NewMetricUpdate(snapshot)

	g := 2.5
	if err := update.UpdateMetric(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got, ok := store.GetGauge("Alloc"); !ok || got != 2.5 {
		t.Fatalf("expected the update to reach the underlying store, got %v (present=%v)", got, ok)
	}
}
