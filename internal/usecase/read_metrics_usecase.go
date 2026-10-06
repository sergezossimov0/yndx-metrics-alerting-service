package usecase

import "context"

// MetricReadStore is what MetricRead needs from a storage. It is declared
// here, next to its consumer, not in the repository package.
type MetricReadStore interface {
	GetGauge(ctx context.Context, name string) (float64, bool)
	GetCounter(ctx context.Context, name string) (int64, bool)
	ListGauges(ctx context.Context) map[string]float64
	ListCounters(ctx context.Context) map[string]int64
}

func NewMetricRead(store MetricReadStore) MetricRead {
	return MetricRead{store: store}
}

type MetricRead struct {
	store MetricReadStore
}

func (mr *MetricRead) GetGauge(ctx context.Context, name string) (float64, bool) {
	return mr.store.GetGauge(ctx, name)
}

func (mr *MetricRead) GetCounter(ctx context.Context, name string) (int64, bool) {
	return mr.store.GetCounter(ctx, name)
}

func (mr *MetricRead) ListGauges(ctx context.Context) map[string]float64 {
	return mr.store.ListGauges(ctx)
}

func (mr *MetricRead) ListCounters(ctx context.Context) map[string]int64 {
	return mr.store.ListCounters(ctx)
}
