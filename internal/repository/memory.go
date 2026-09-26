package repository

import (
	"sync"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

// MemStorage keeps metrics in memory and is safe for concurrent use.
// The maps are unexported so every access goes through mu. A plain Mutex is
// used instead of RWMutex: metrics are written about as often as they are
// read, and contention is low, where Mutex is simpler and slightly faster.
// Either way the lock costs tens of nanoseconds, negligible next to handling
// an HTTP request (see BenchmarkMemStorage).
type MemStorage struct {
	mu       sync.Mutex
	gauges   map[string]float64
	counters map[string]int64
}

func NewMemStorage() *MemStorage {
	return &MemStorage{
		gauges:   make(map[string]float64),
		counters: make(map[string]int64),
	}
}

func (s *MemStorage) Update(metric *models.Metrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if metric.MType == models.Gauge && metric.Value != nil {
		s.gauges[metric.ID] = *metric.Value
	} else if metric.MType == models.Counter && metric.Delta != nil {
		s.counters[metric.ID] += *metric.Delta
	}

	return nil
}

func (s *MemStorage) ListGauges() map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create a copy of the Gauges map to avoid exposing internal state
	gaugesCopy := make(map[string]float64)
	for k, v := range s.gauges {
		gaugesCopy[k] = v
	}

	return gaugesCopy
}

func (s *MemStorage) ListCounters() map[string]int64 {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Create a copy of the Counters map to avoid exposing internal state
	countersCopy := make(map[string]int64)
	for k, v := range s.counters {
		countersCopy[k] = v
	}

	return countersCopy
}

func (s *MemStorage) GetGauge(name string) (float64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.gauges[name]
	return v, ok
}

func (s *MemStorage) GetCounter(name string) (int64, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	v, ok := s.counters[name]
	return v, ok
}

// Snapshot returns all currently stored metrics as a flat slice, suitable for
// serializing to disk (see Snapshot).
func (s *MemStorage) Snapshot() []models.Metrics {
	s.mu.Lock()
	defer s.mu.Unlock()

	metrics := make([]models.Metrics, 0, len(s.gauges)+len(s.counters))
	for name, value := range s.gauges {
		v := value
		metrics = append(metrics, models.Metrics{ID: name, MType: models.Gauge, Value: &v})
	}
	for name, delta := range s.counters {
		d := delta
		metrics = append(metrics, models.Metrics{ID: name, MType: models.Counter, Delta: &d})
	}

	return metrics
}

// Restore loads a previously saved snapshot (see Snapshot.Restore),
// overwriting any gauge/counter with the same ID already present.
func (s *MemStorage) Restore(metrics []models.Metrics) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, m := range metrics {
		switch m.MType {
		case models.Gauge:
			if m.Value != nil {
				s.gauges[m.ID] = *m.Value
			}
		case models.Counter:
			if m.Delta != nil {
				s.counters[m.ID] = *m.Delta
			}
		}
	}
}
