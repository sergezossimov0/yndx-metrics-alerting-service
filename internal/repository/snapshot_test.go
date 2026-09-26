package repository

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mailru/easyjson"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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

// dirEntries returns the names of the files in dir.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("failed to read dir: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestSaveSnapshotToFile_LeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	g := 1.0

	for i := 0; i < 3; i++ {
		if err := saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g}}); err != nil {
			t.Fatalf("unexpected error on save %d: %v", i, err)
		}
	}

	if names := dirEntries(t, dir); len(names) != 1 || names[0] != "snapshot.json" {
		t.Fatalf("expected only snapshot.json in the directory, got %v", names)
	}
}

func TestSaveSnapshotToFile_KeepsFileMode(t *testing.T) {
	// os.CreateTemp creates files with 0600: without an explicit chmod the
	// snapshot would silently lose the permissions it had with os.WriteFile
	path := filepath.Join(t.TempDir(), "snapshot.json")

	if err := saveSnapshotToFile(path, nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("failed to stat snapshot: %v", err)
	}
	if got := info.Mode().Perm(); got != snapshotFileMode {
		t.Fatalf("expected mode %o, got %o", snapshotFileMode, got)
	}
}

func TestSaveSnapshotToFile_FailedRenameRemovesTempFile(t *testing.T) {
	// a non-empty directory in place of the target makes the final rename fail
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("failed to create blocking dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("x"), 0o644); err != nil {
		t.Fatalf("failed to fill blocking dir: %v", err)
	}

	g := 1.0
	if err := saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g}}); err == nil {
		t.Fatal("expected an error when the target cannot be replaced")
	}

	if names := dirEntries(t, dir); len(names) != 1 || names[0] != "snapshot.json" {
		t.Fatalf("expected the temp file to be removed after the failure, got %v", names)
	}
}

func TestSaveSnapshotToFile_FailureKeepsPreviousSnapshot(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	g1, g2 := 1.0, 2.0
	if err := saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g1}}); err != nil {
		t.Fatalf("failed to prepare snapshot: %v", err)
	}

	// a read-only directory makes the temp file impossible to create
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("failed to make dir read-only: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	if err := saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g2}}); err == nil {
		t.Fatal("expected an error when the directory is read-only")
	}

	got, err := loadSnapshotFromFile(path)
	if err != nil {
		t.Fatalf("expected the previous snapshot to stay readable, got error: %v", err)
	}
	if len(got) != 1 || got[0].Value == nil || *got[0].Value != 1.0 {
		t.Fatalf("expected the previous snapshot Alloc=1.0 to stay intact, got %+v", got)
	}
}

func TestNewSnapshot_RestoreIgnoresLeftoverTempFile(t *testing.T) {
	// a SIGKILL in the middle of a save leaves a partial temp file behind;
	// the real snapshot next to it must still restore
	dir := t.TempDir()
	path := filepath.Join(dir, "snapshot.json")
	g := 7.5
	if err := saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g}}); err != nil {
		t.Fatalf("failed to prepare snapshot: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".snapshot.json.tmp-123"), []byte(`[{"id":"Al`), 0o600); err != nil {
		t.Fatalf("failed to write leftover temp file: %v", err)
	}
	store := NewMemStorage()

	NewSnapshot(path, 0, store, true, zap.NewNop())

	if got, ok := store.GetGauge("Alloc"); !ok || got != 7.5 {
		t.Fatalf("expected Alloc=7.5 restored from the real snapshot, got %v (present=%v)", got, ok)
	}
}

// BenchmarkSaveSnapshotToFile measures the cost of the atomic write, mostly
// fsync, against the old direct os.WriteFile. With STORE_INTERVAL=0 the save
// runs on every metric update, so this is the price of each POST /update/.
func BenchmarkSaveSnapshotToFile(b *testing.B) {
	metrics := make([]models.Metrics, 0, 30)
	for i := 0; i < 30; i++ {
		v := float64(i)
		metrics = append(metrics, models.Metrics{ID: fmt.Sprintf("gauge%d", i), MType: models.Gauge, Value: &v})
	}
	path := filepath.Join(b.TempDir(), "snapshot.json")

	b.Run("atomic", func(b *testing.B) {
		for b.Loop() {
			if err := saveSnapshotToFile(path, metrics); err != nil {
				b.Fatal(err)
			}
		}
	})

	b.Run("direct WriteFile (old)", func(b *testing.B) {
		for b.Loop() {
			data, err := easyjson.Marshal(models.MetricsList(metrics))
			if err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(path, data, snapshotFileMode); err != nil {
				b.Fatal(err)
			}
		}
	})
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

// newObservedLogger returns a logger that keeps entries in memory, so a test
// can inspect what a component logged without touching any global state.
func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

func TestNewSnapshot_RestoreMissingFileIsNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist.json")
	store := NewMemStorage()
	log, logs := newObservedLogger()

	NewSnapshot(path, 0, store, true, log)

	if logs.Len() != 0 {
		t.Fatalf("expected nothing to be logged for a missing snapshot file, got %v", logs.All())
	}
	if len(store.Snapshot()) != 0 {
		t.Fatalf("expected store to stay empty, got %v", store.Snapshot())
	}
}

func TestNewSnapshot_RestoreCorruptFileLogsErrorAndLeavesStoreEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("failed to write corrupt file: %v", err)
	}
	store := NewMemStorage()
	log, logs := newObservedLogger()

	NewSnapshot(path, 0, store, true, log)

	if logs.FilterMessage("failed to restore metrics snapshot").FilterLevelExact(zapcore.ErrorLevel).Len() != 1 {
		t.Fatalf("expected restore error to be logged, got %v", logs.All())
	}
	if len(store.Snapshot()) != 0 {
		t.Fatalf("expected store to stay empty, got %v", store.Snapshot())
	}
}

func TestNewSnapshot_RestoreDisabledDoesNotLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	g := 7.5
	if err := saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g}}); err != nil {
		t.Fatalf("failed to prepare snapshot: %v", err)
	}
	store := NewMemStorage()

	NewSnapshot(path, 0, store, false, zap.NewNop())

	if _, ok := store.GetGauge("Alloc"); ok {
		t.Fatal("expected NewSnapshot with restore=false to leave the store empty, but Alloc was restored")
	}
}

func TestNewSnapshot_RestoreLoadsMetricsIntoStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	g := 7.5
	if err := saveSnapshotToFile(path, []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g}}); err != nil {
		t.Fatalf("failed to prepare snapshot: %v", err)
	}
	store := NewMemStorage()

	NewSnapshot(path, 0, store, true, zap.NewNop())

	got, ok := store.GetGauge("Alloc")
	if !ok || got != 7.5 {
		t.Fatalf("expected restored gauge Alloc=7.5, got %v (present=%v)", got, ok)
	}
}

func TestSnapshotUpdate_PersistsAfterEveryUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.json")
	snapshot := NewSnapshot(path, 0, NewMemStorage(), false, zap.NewNop())

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
	snapshot := NewSnapshot(path, 0, store, false, zap.NewNop())

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
	snapshot := NewSnapshot(path, 0, NewMemStorage(), false, zap.NewNop())

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
	snapshot := NewSnapshot(path, 3600, store, false, zap.NewNop())

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
	for _, interval := range []time.Duration{0, -time.Second} {
		path := filepath.Join(t.TempDir(), "snapshot.json")
		snapshot := NewSnapshot(path, interval, NewMemStorage(), false, zap.NewNop())

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
