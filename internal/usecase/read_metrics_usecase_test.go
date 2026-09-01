package usecase

import (
	"testing"
)

type metricReadStoreMock struct {
	gauges             map[string]float64
	counters           map[string]int64
	getGaugeCalled     int
	getCounterCalled   int
	listGaugesCalled   int
	listCountersCalled int
}

func (m *metricReadStoreMock) GetGauge(name string) (float64, bool) {
	m.getGaugeCalled++
	v, ok := m.gauges[name]
	return v, ok
}

func (m *metricReadStoreMock) GetCounter(name string) (int64, bool) {
	m.getCounterCalled++
	v, ok := m.counters[name]
	return v, ok
}

func (m *metricReadStoreMock) ListGauges() map[string]float64 {
	m.listGaugesCalled++
	return m.gauges
}

func (m *metricReadStoreMock) ListCounters() map[string]int64 {
	m.listCountersCalled++
	return m.counters
}

func TestMetricRead_GetGauge_ReturnsValueFromStore(t *testing.T) {
	store := &metricReadStoreMock{gauges: map[string]float64{"Alloc": 10.5}}
	mr := NewMetricRead(store)

	value, ok := mr.GetGauge("Alloc")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if value != 10.5 {
		t.Fatalf("expected 10.5, got %v", value)
	}
	if store.getGaugeCalled != 1 {
		t.Fatalf("expected GetGauge to be called once, got %d", store.getGaugeCalled)
	}
}

func TestMetricRead_GetGauge_ReturnsFalseWhenMissing(t *testing.T) {
	store := &metricReadStoreMock{gauges: map[string]float64{}}
	mr := NewMetricRead(store)

	_, ok := mr.GetGauge("Unknown")
	if ok {
		t.Fatal("expected ok=false")
	}
}

func TestMetricRead_GetCounter_ReturnsValueFromStore(t *testing.T) {
	store := &metricReadStoreMock{counters: map[string]int64{"PollCount": 3}}
	mr := NewMetricRead(store)

	value, ok := mr.GetCounter("PollCount")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if value != 3 {
		t.Fatalf("expected 3, got %v", value)
	}
	if store.getCounterCalled != 1 {
		t.Fatalf("expected GetCounter to be called once, got %d", store.getCounterCalled)
	}
}

func TestMetricRead_GetCounter_ReturnsFalseWhenMissing(t *testing.T) {
	store := &metricReadStoreMock{counters: map[string]int64{}}
	mr := NewMetricRead(store)

	_, ok := mr.GetCounter("Unknown")
	if ok {
		t.Fatal("expected ok=false")
	}
}

func TestMetricRead_ListGauges_ReturnsAllGaugesFromStore(t *testing.T) {
	store := &metricReadStoreMock{gauges: map[string]float64{"Alloc": 10.5, "HeapAlloc": 22.3}}
	mr := NewMetricRead(store)

	gauges := mr.ListGauges()
	if len(gauges) != 2 {
		t.Fatalf("expected 2 gauges, got %d", len(gauges))
	}
	if gauges["Alloc"] != 10.5 {
		t.Fatalf("expected Alloc=10.5, got %v", gauges["Alloc"])
	}
	if store.listGaugesCalled != 1 {
		t.Fatalf("expected ListGauges to be called once, got %d", store.listGaugesCalled)
	}
}

func TestMetricRead_ListCounters_ReturnsAllCountersFromStore(t *testing.T) {
	store := &metricReadStoreMock{counters: map[string]int64{"PollCount": 3}}
	mr := NewMetricRead(store)

	counters := mr.ListCounters()
	if len(counters) != 1 {
		t.Fatalf("expected 1 counter, got %d", len(counters))
	}
	if counters["PollCount"] != 3 {
		t.Fatalf("expected PollCount=3, got %v", counters["PollCount"])
	}
	if store.listCountersCalled != 1 {
		t.Fatalf("expected ListCounters to be called once, got %d", store.listCountersCalled)
	}
}
