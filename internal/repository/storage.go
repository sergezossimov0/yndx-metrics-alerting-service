package repository

import (
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

type UpdateMetricStore interface {
	Update(metric *models.Metrics) error
	ListGuages() map[string]float64
	ListCounters() map[string]int64
}
