package usecase

// MetricReadStore is what MetricRead needs from a storage. It is declared
// here, next to its consumer, not in the repository package.
type MetricReadStore interface {
	GetGauge(name string) (float64, bool)
	GetCounter(name string) (int64, bool)
	ListGauges() map[string]float64
	ListCounters() map[string]int64
}

func NewMetricRead(store MetricReadStore) MetricRead {
	return MetricRead{store: store}
}

type MetricRead struct {
	store MetricReadStore
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
