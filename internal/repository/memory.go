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
