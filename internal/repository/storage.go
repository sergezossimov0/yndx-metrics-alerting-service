package repository

import (
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

type UpdateMetricStore interface {
	Update(metric *models.Metrics) error
}

type MetricReader interface {
	ListGauges() map[string]float64
	ListCounters() map[string]int64
	GetGauge(name string) (float64, bool)
	GetCounter(name string) (int64, bool)
}
