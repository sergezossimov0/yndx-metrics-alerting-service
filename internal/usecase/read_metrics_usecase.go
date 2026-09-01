package usecase

import "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"

func NewMetricRead(store repository.MetricReader) MetricRead {
	return MetricRead{store: store}
}

type MetricRead struct {
	store repository.MetricReader
}

func (mr *MetricRead) GetGauge(name string) (float64, bool) {
	return mr.store.GetGauge(name)
}

func (mr *MetricRead) GetCounter(name string) (int64, bool) {
	return mr.store.GetCounter(name)
}

func (mr *MetricRead) ListGauges() map[string]float64 {
	return mr.store.ListGauges()
}

func (mr *MetricRead) ListCounters() map[string]int64 {
	return mr.store.ListCounters()
}
