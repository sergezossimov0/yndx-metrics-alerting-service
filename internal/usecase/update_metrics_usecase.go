package usecase

import (
	"context"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

// MetricUpdateStore is what MetricUpdate needs from a storage. It is declared
// here, next to its consumer, not in the repository package.
type MetricUpdateStore interface {
	Update(ctx context.Context, metric *models.Metrics) error
	// UpdateBatch stores all metrics atomically: either all or none.
	UpdateBatch(ctx context.Context, metrics []models.Metrics) error
}

func NewMetricUpdate(store MetricUpdateStore) MetricUpdate {
	return MetricUpdate{
		store: store,
	}
}

type MetricUpdate struct {
	store MetricUpdateStore
}

func (mu *MetricUpdate) UpdateMetric(ctx context.Context, metric *models.Metrics) error {
	if err := mu.store.Update(ctx, metric); err != nil {
		return err
	}
	return nil
}

func (mu *MetricUpdate) UpdateMetrics(ctx context.Context, metrics []models.Metrics) error {
	if err := mu.store.UpdateBatch(ctx, metrics); err != nil {
		return err
	}
	return nil
}
