package usecase

import (
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
)

func NewMetricUpdate(store repository.UpdateMetricStore) MetricUpdate {
	return MetricUpdate{
		store: store,
	}
}

type MetricUpdate struct {
	store repository.UpdateMetricStore
}

func (mu *MetricUpdate) UpdateMetric(metric *models.Metrics) error {
	if err := mu.store.Update(metric); err != nil {
		return err
	}
	return nil
}
