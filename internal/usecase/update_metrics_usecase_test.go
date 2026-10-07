package usecase

import (
	"context"
	"errors"
	"testing"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

type updateMetricStoreMock struct {
	updateErr    error
	updateCalled int
	batch        []models.Metrics
	batchCalled  int
}

func (m *updateMetricStoreMock) Update(_ context.Context, metric *models.Metrics) error {
	m.updateCalled++
	return m.updateErr
}

func (m *updateMetricStoreMock) UpdateBatch(_ context.Context, metrics []models.Metrics) error {
	m.batchCalled++
	m.batch = metrics
	return m.updateErr
}

func TestMetricUpdate_UpdateMetric_CallsStoreUpdate(t *testing.T) {
	store := &updateMetricStoreMock{}
	uc := NewMetricUpdate(store)

	g := 1.5
	metric := &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g}

	if err := uc.UpdateMetric(context.Background(), metric); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}

	if store.updateCalled != 1 {
		t.Fatalf("expected Update to be called once, got %d", store.updateCalled)
	}
}

func TestMetricUpdate_UpdateMetric_ReturnsStoreError(t *testing.T) {
	expectedErr := errors.New("update failed")
	store := &updateMetricStoreMock{updateErr: expectedErr}
	uc := NewMetricUpdate(store)

	g := 1.5
	metric := &models.Metrics{ID: "Alloc", MType: models.Gauge, Value: &g}

	err := uc.UpdateMetric(context.Background(), metric)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}

}

func TestMetricUpdate_UpdateMetrics_PassesWholeBatchToStore(t *testing.T) {
	store := &updateMetricStoreMock{}
	uc := NewMetricUpdate(store)

	g := 1.5
	d := int64(2)
	batch := []models.Metrics{
		{ID: "Alloc", MType: models.Gauge, Value: &g},
		{ID: "PollCount", MType: models.Counter, Delta: &d},
	}

	if err := uc.UpdateMetrics(context.Background(), batch); err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if store.batchCalled != 1 || store.updateCalled != 0 {
		t.Fatalf("expected one UpdateBatch call and no Update calls, got %d and %d", store.batchCalled, store.updateCalled)
	}
	if len(store.batch) != 2 {
		t.Fatalf("expected the whole batch to reach the store, got %d metrics", len(store.batch))
	}
}

func TestMetricUpdate_UpdateMetrics_ReturnsStoreError(t *testing.T) {
	expectedErr := errors.New("batch failed")
	uc := NewMetricUpdate(&updateMetricStoreMock{updateErr: expectedErr})

	g := 1.5
	err := uc.UpdateMetrics(context.Background(), []models.Metrics{{ID: "Alloc", MType: models.Gauge, Value: &g}})
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}
