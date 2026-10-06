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
}

func (m *updateMetricStoreMock) Update(_ context.Context, metric *models.Metrics) error {
	m.updateCalled++
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
