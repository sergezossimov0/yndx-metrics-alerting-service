package repository

import (
	"fmt"
	"sync"
	"testing"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

func TestNewMemStorage_ReturnsEmptyStorage(t *testing.T) {
	s := NewMemStorage()

	if len(s.ListGauges()) != 0 {
		t.Fatalf("expected no gauges, got %d", len(s.ListGauges()))
	}
	if len(s.ListCounters()) != 0 {
		t.Fatalf("expected no counters, got %d", len(s.ListCounters()))
	}
}

func TestMemStorage_Update_SetsGaugeValue(t *testing.T) {
	s := NewMemStorage()
	v := 10.5

	if err := s.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &v}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	got, ok := s.GetGauge("Alloc")
	if !ok {
		t.Fatal("expected gauge Alloc to be present")
	}
	if got != 10.5 {
		t.Fatalf("expected 10.5, got %v", got)
	}
}

func TestMemStorage_Update_OverwritesPreviousGaugeValue(t *testing.T) {
	s := NewMemStorage()
	v1, v2 := 1.0, 2.0

	_ = s.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &v1})
	_ = s.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &v2})

	got, _ := s.GetGauge("Alloc")
	if got != 2.0 {
		t.Fatalf("expected gauge to be overwritten with 2.0, got %v", got)
	}
}

func TestMemStorage_Update_AccumulatesCounterDelta(t *testing.T) {
	s := NewMemStorage()
	d1, d2 := int64(2), int64(3)

	_ = s.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &d1})
	_ = s.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &d2})

	got, ok := s.GetCounter("PollCount")
	if !ok {
		t.Fatal("expected counter PollCount to be present")
	}
	if got != 5 {
		t.Fatalf("expected accumulated counter 5, got %v", got)
	}
}

func TestMemStorage_Update_GaugeWithNilValueIsNoop(t *testing.T) {
	s := NewMemStorage()

	if err := s.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: nil}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if _, ok := s.GetGauge("Alloc"); ok {
		t.Fatal("expected gauge to not be stored when Value is nil")
	}
}

func TestMemStorage_Update_CounterWithNilDeltaIsNoop(t *testing.T) {
	s := NewMemStorage()

	if err := s.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: nil}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if _, ok := s.GetCounter("PollCount"); ok {
		t.Fatal("expected counter to not be stored when Delta is nil")
	}
}

func TestMemStorage_Update_UnknownMetricTypeIsNoop(t *testing.T) {
	s := NewMemStorage()
	v := 1.0

	if err := s.Update(&models.Metrics{ID: "Something", MType: "unknown", Value: &v}); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if len(s.ListGauges()) != 0 || len(s.ListCounters()) != 0 {
		t.Fatal("expected no metric stored for unknown type")
	}
}

func TestMemStorage_GetGauge_ReturnsFalseWhenMissing(t *testing.T) {
	s := NewMemStorage()

	if _, ok := s.GetGauge("Unknown"); ok {
		t.Fatal("expected ok=false for missing gauge")
	}
}

func TestMemStorage_GetCounter_ReturnsFalseWhenMissing(t *testing.T) {
	s := NewMemStorage()

	if _, ok := s.GetCounter("Unknown"); ok {
		t.Fatal("expected ok=false for missing counter")
	}
}

func TestMemStorage_ListGauges_ReturnsIndependentCopy(t *testing.T) {
	s := NewMemStorage()
	v := 1.0
	_ = s.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &v})

	gauges := s.ListGauges()
	gauges["Alloc"] = 999

	got, _ := s.GetGauge("Alloc")
	if got != 1.0 {
		t.Fatalf("expected internal state to stay 1.0 after mutating returned map, got %v", got)
	}
}

func TestMemStorage_Snapshot_ReturnsAllGaugesAndCounters(t *testing.T) {
	s := NewMemStorage()
	g := 10.5
	c := int64(3)
	_ = s.Update(&models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g})
	_ = s.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &c})

	snapshot := s.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 metrics in snapshot, got %d", len(snapshot))
	}

	byID := make(map[string]models.Metrics)
	for _, m := range snapshot {
		byID[m.ID] = m
	}

	gauge, ok := byID["Alloc"]
	if !ok || gauge.MType != models.Gauge || gauge.Value == nil || *gauge.Value != 10.5 {
		t.Fatalf("expected gauge Alloc=10.5 in snapshot, got %+v (present=%v)", gauge, ok)
	}
	counter, ok := byID["PollCount"]
	if !ok || counter.MType != models.Counter || counter.Delta == nil || *counter.Delta != 3 {
		t.Fatalf("expected counter PollCount=3 in snapshot, got %+v (present=%v)", counter, ok)
	}
}

func TestMemStorage_Snapshot_OfEmptyStorageReturnsEmptySlice(t *testing.T) {
	s := NewMemStorage()

	snapshot := s.Snapshot()
	if len(snapshot) != 0 {
		t.Fatalf("expected empty snapshot, got %d metrics", len(snapshot))
	}
}

func TestMemStorage_Restore_PopulatesGaugesAndCounters(t *testing.T) {
	s := NewMemStorage()
	g := 42.5
	c := int64(7)

	s.Restore([]models.Metrics{
		{ID: "Alloc", MType: models.Gauge, Value: &g},
		{ID: "PollCount", MType: models.Counter, Delta: &c},
	})

	gotGauge, ok := s.GetGauge("Alloc")
	if !ok || gotGauge != 42.5 {
		t.Fatalf("expected restored gauge Alloc=42.5, got %v (present=%v)", gotGauge, ok)
	}
	gotCounter, ok := s.GetCounter("PollCount")
	if !ok || gotCounter != 7 {
		t.Fatalf("expected restored counter PollCount=7, got %v (present=%v)", gotCounter, ok)
	}
}

func TestMemStorage_Restore_SetsCounterRatherThanAccumulating(t *testing.T) {
	s := NewMemStorage()
	existing := int64(100)
	_ = s.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &existing})

	restored := int64(7)
	s.Restore([]models.Metrics{{ID: "PollCount", MType: models.Counter, Delta: &restored}})

	got, _ := s.GetCounter("PollCount")
	if got != 7 {
		t.Fatalf("expected restore to set (not add) the counter to 7, got %v", got)
	}
}

func TestMemStorage_Restore_SkipsEntriesWithNilValueOrDelta(t *testing.T) {
	s := NewMemStorage()

	s.Restore([]models.Metrics{
		{ID: "Alloc", MType: models.Gauge, Value: nil},
		{ID: "PollCount", MType: models.Counter, Delta: nil},
	})

	if _, ok := s.GetGauge("Alloc"); ok {
		t.Fatal("expected gauge with nil Value to not be restored")
	}
	if _, ok := s.GetCounter("PollCount"); ok {
		t.Fatal("expected counter with nil Delta to not be restored")
	}
}

func TestMemStorage_ListCounters_ReturnsIndependentCopy(t *testing.T) {
	s := NewMemStorage()
	d := int64(1)
	_ = s.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &d})

	counters := s.ListCounters()
	counters["PollCount"] = 999

	got, _ := s.GetCounter("PollCount")
	if got != 1 {
		t.Fatalf("expected internal state to stay 1 after mutating returned map, got %v", got)
	}
}

// Run with -race: writers and readers hit the storage at the same time, so a
// missing or wrong lock shows up as a data race. The final counter value also
// catches lost updates in the read-modify-write of a counter.
func TestMemStorage_ConcurrentAccessIsSafe(t *testing.T) {
	const writers, readers, iterations = 8, 4, 500

	s := NewMemStorage()
	var wg sync.WaitGroup

	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			one := int64(1)
			for i := 0; i < iterations; i++ {
				g := float64(i)
				_ = s.Update(&models.Metrics{ID: fmt.Sprintf("gauge%d", w), MType: models.Gauge, Value: &g})
				_ = s.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &one})
			}
		}(w)
	}

	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				_, _ = s.GetGauge("gauge0")
				_, _ = s.GetCounter("PollCount")
				_ = s.ListGauges()
				_ = s.ListCounters()
				_ = s.Snapshot()
			}
		}()
	}

	wg.Wait()

	if got, _ := s.GetCounter("PollCount"); got != writers*iterations {
		t.Fatalf("expected PollCount=%d after concurrent increments, got %d", writers*iterations, got)
	}
	if got := len(s.ListGauges()); got != writers {
		t.Fatalf("expected %d gauges, got %d", writers, got)
	}
}
