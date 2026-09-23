package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

func TestSaveAndLoadSnapshotFromFile_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	g := 10.5
	c := int64(3)
	want := []models.Metrics{
		{ID: "Alloc", MType: models.Gauge, Value: &g},
		{ID: "PollCount", MType: models.Counter, Delta: &c},
	}

	if err := saveSnapshotToFile(path, want); err != nil {
		t.Fatalf("unexpected error saving: %v", err)
	}

	got, err := loadSnapshotFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error loading: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 metrics, got %d", len(got))
	}

	byID := make(map[string]models.Metrics)
	for _, m := range got {
		byID[m.ID] = m
	}
	if gauge := byID["Alloc"]; gauge.Value == nil || *gauge.Value != 10.5 {
		t.Fatalf("expected gauge Alloc=10.5, got %+v", gauge)
	}
	if counter := byID["PollCount"]; counter.Delta == nil || *counter.Delta != 3 {
		t.Fatalf("expected counter PollCount=3, got %+v", counter)
	}
}

func TestSaveSnapshotToFile_OverwritesPreviousContents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	g1, g2 := 1.0, 2.0

	_ = saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g1}})
	_ = saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g2}})

	got, err := loadSnapshotFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Value == nil || *got[0].Value != 2.0 {
		t.Fatalf("expected overwritten snapshot with Alloc=2.0, got %+v", got)
	}
}

func TestLoadSnapshotFromFile_ReturnsNotExistErrorForMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")

	_, err := loadSnapshotFromFile(path)
	if !os.IsNotExist(err) {
		t.Fatalf("expected a not-exist error, got %v", err)
	}
}

func TestLoadSnapshotFromFile_EmptyFileReturnsNilWithoutError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "empty.json")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("failed to create empty file: %v", err)
	}

	got, err := loadSnapshotFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error for an empty file: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected no metrics from an empty file, got %d", len(got))
	}
}

func TestSnapshotRestore_MissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	snapshot := NewSnapshot(path, 0, NewMemStorage())

	if err := snapshot.Restore(); err != nil {
		t.Fatalf("expected no error for a missing snapshot file, got %v", err)
	}
}

func TestSnapshotRestore_CorruptFileReturnsError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}
	snapshot := NewSnapshot(path, 0, NewMemStorage())

	if err := snapshot.Restore(); err == nil {
		t.Fatal("expected an error for a corrupt snapshot file")
	}
}

func TestSnapshotRestore_LoadsMetricsIntoStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	g := 7.5
	if err := saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g}}); err != nil {
		t.Fatalf("failed to prepare snapshot: %v", err)
	}
	store := NewMemStorage()

	if err := NewSnapshot(path, 0, store).Restore(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, ok := store.GetGauge("Alloc")
	if !ok || got != 7.5 {
		t.Fatalf("expected restored gauge Alloc=7.5, got %v (present=%v)", got, ok)
	}
}

func TestSnapshotUpdate_PersistsAfterEveryUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	snapshot := NewSnapshot(path, 0, NewMemStorage())

	g := 5.0
	if err := snapshot.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err := loadSnapshotFromFile(path)
	if err != nil {
		t.Fatalf("expected the snapshot to already be on disk after one update, got error: %v", err)
	}
	if len(got) != 1 || got[0].Value == nil || *got[0].Value != 5.0 {
		t.Fatalf("expected persisted gauge Alloc=5.0, got %+v", got)
	}

	c := int64(2)
	if err := snapshot.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &c}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got, err = loadSnapshotFromFile(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected the snapshot to accumulate both metrics, got %d", len(got))
	}
}

func TestSnapshotUpdate_SucceedsEvenWhenDiskWriteFails(t *testing.T) {
	// a path inside a non-existent directory makes os.WriteFile fail
	path := filepath.Join(t.TempDir(), "missing-dir", "snapshot.json")
	store := NewMemStorage()
	snapshot := NewSnapshot(path, 0, store)

	g := 5.0
	if err := snapshot.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g}); err != nil {
		t.Fatalf("expected Update to succeed despite the disk write failing, got: %v", err)
	}

	got, ok := store.GetGauge("Alloc")
	if !ok || got != 5.0 {
		t.Fatalf("expected the metric to still be stored in memory, got %v (present=%v)", got, ok)
	}
}

func TestSnapshotUpdate_ConcurrentUpdatesLeaveValidFileWithAllMetrics(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	snapshot := NewSnapshot(path, 0, NewMemStorage())

	const n = 50
	var wg sync.WaitGroup
	for i := range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			d := int64(1)
			_ = snapshot.Update(&models.Metrics{ID: fmt.Sprintf("Counter%d", i), MType: models.Counter, Delta: &d})
		}()
	}
	wg.Wait()

	got, err := loadSnapshotFromFile(path)
	if err != nil {
		t.Fatalf("expected a valid snapshot file after concurrent updates, got error: %v", err)
	}
	if len(got) != n {
		t.Fatalf("expected the last write to contain all %d metrics, got %d", n, len(got))
	}
}

func TestSnapshotRun_SavesOnEveryTick(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	store := NewMemStorage()
	snapshot := &Snapshot{path: path, storeInterval: 10 * time.Millisecond, store: store}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		snapshot.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Two different states must both reach the disk: that proves Run keeps
	// ticking instead of stopping after the first save.
	g := 1.0
	_ = store.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g})
	waitForSnapshot(t, path, 1)

	c := int64(1)
	_ = store.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &c})
	waitForSnapshot(t, path, 2)
}

func TestSnapshotRun_SavesOnceMoreWhenContextIsCancelled(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	store := NewMemStorage()
	// an interval far longer than the test, so only the shutdown save can write the file
	snapshot := NewSnapshot(path, 3600, store)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		snapshot.Run(ctx)
	}()

	g := 1.0
	_ = store.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g})
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected Run to return after the context was cancelled")
	}

	got, err := loadSnapshotFromFile(path)
	if err != nil {
		t.Fatalf("expected a final snapshot on disk, got error: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected the final snapshot to contain 1 metric, got %d", len(got))
	}
}

func TestSnapshotRun_NonPositiveIntervalReturnsWithoutPanicking(t *testing.T) {
	for _, interval := range []int{0, -1} {
		path := filepath.Join(t.TempDir(), "snapshot.json")
		snapshot := NewSnapshot(path, interval, NewMemStorage())

		done := make(chan struct{})
		go func() {
			defer close(done)
			snapshot.Run(context.Background())
		}()

		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatalf("expected Run to return immediately for interval %d", interval)
		}
	}
}

func waitForSnapshot(t *testing.T, path string, wantLen int) {
	t.Helper()

	deadline := time.After(time.Second)
	for {
		if got, err := loadSnapshotFromFile(path); err == nil && len(got) == wantLen {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("expected a snapshot with %d metrics to appear on disk within 1s", wantLen)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
