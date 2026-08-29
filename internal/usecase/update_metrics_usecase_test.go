package usecase

import (
	"errors"
	"testing"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

type mockUpdateMetricStore struct {
	updateErr        error
	updateCalled     int
	listGaugesCalled int
	listCountCalled  int
}

func (m *mockUpdateMetricStore) Update(metric *models.Metrics) error {
	m.updateCalled++
	return m.updateErr
}

func (m *mockUpdateMetricStore) ListGuages() map[string]float64 {
	m.listGaugesCalled++
	return map[string]float64{"Alloc": 123.45}
}

func (m *mockUpdateMetricStore) ListCounters() map[string]int64 {
	m.listCountCalled++
	return map[string]int64{"PollCount": 7}
}

func TestMetricUpdate_UpdateMetric_Success(t *testing.T) {
	store := &mockUpdateMetricStore{}
	uc := NewMetricUpdate(store)

	g := 1.5
	metric := &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g}

	if err := uc.UpdateMetric(metric); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if store.updateCalled != 1 {
		t.Fatalf("expected Update to be called once, got %d", store.updateCalled)
	}

	if store.listGaugesCalled == 0 || store.listCountCalled == 0 {
		t.Fatalf("expected Print() to call both list methods, got gauges=%d counters=%d", store.listGaugesCalled, store.listCountCalled)
	}
}

func TestMetricUpdate_UpdateMetric_Error(t *testing.T) {
	expectedErr := errors.New("update failed")
	store := &mockUpdateMetricStore{updateErr: expectedErr}
	uc := NewMetricUpdate(store)

	g := 1.5
	metric := &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g}

	err := uc.UpdateMetric(metric)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

	if store.listGaugesCalled != 0 || store.listCountCalled != 0 {
		t.Fatalf("expected Print() not to run on update error, got gauges=%d counters=%d", store.listGaugesCalled, store.listCountCalled)
	}
}

func TestMetricUpdate_Print(t *testing.T) {
	store := &mockUpdateMetricStore{}
	uc := NewMetricUpdate(store)

	uc.Print()

	if store.listGaugesCalled != 1 || store.listCountCalled != 1 {
		t.Fatalf("expected one read from both maps, got gauges=%d counters=%d", store.listGaugesCalled, store.listCountCalled)
	}
}
