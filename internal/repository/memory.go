package repository

import (
	"sync"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

type MemStorage struct {
	mu       sync.RWMutex
	Gauges   map[string]float64
	Counters map[string]int64
}

func NewMemStorage() *MemStorage {
	return &MemStorage{
		Gauges:   make(map[string]float64),
		Counters: make(map[string]int64),
	}
}

func (s *MemStorage) Update(metric *models.Metrics) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if metric.MType == models.Gauge && metric.Value != nil {
		s.Gauges[metric.ID] = *metric.Value
	} else if metric.MType == models.Counter && metric.Delta != nil {
		s.Counters[metric.ID] += *metric.Delta
	}

	return nil
}

func (s *MemStorage) ListGauges() map[string]float64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a copy of the Gauges map to avoid exposing internal state
	gaugesCopy := make(map[string]float64)
	for k, v := range s.Gauges {
		gaugesCopy[k] = v
	}

	return gaugesCopy
}

func (s *MemStorage) ListCounters() map[string]int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Create a copy of the Counters map to avoid exposing internal state
	countersCopy := make(map[string]int64)
	for k, v := range s.Counters {
		countersCopy[k] = v
	}

	return countersCopy
}

func (s *MemStorage) GetGauge(name string) (float64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.Gauges[name]
	return v, ok
}

func (s *MemStorage) GetCounter(name string) (int64, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, ok := s.Counters[name]
	return v, ok
}

// Snapshot returns all currently stored metrics as a flat slice, suitable for
// serializing to disk (see Snapshot).
func (s *MemStorage) Snapshot() []models.Metrics {
	s.mu.RLock()
	defer s.mu.RUnlock()

	metrics := make([]models.Metrics, 0, len(s.Gauges)+len(s.Counters))
	for name, value := range s.Gauges {
		v := value
		metrics = append(metrics, models.Metrics{ID: name, MType: models.Gauge, Value: &v})
	}
	for name, delta := range s.Counters {
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
				s.Gauges[m.ID] = *m.Value
			}
		case models.Counter:
			if m.Delta != nil {
				s.Counters[m.ID] = *m.Delta
			}
		}
	}
}
