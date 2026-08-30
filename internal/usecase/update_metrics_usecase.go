package usecase

import (
	"log"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
)

func NewMetricUpdate(store repository.UpdateMetricStore) MetricUpdate {
	return MetricUpdate{
		store: store,
	}
}

type MetricUpdate struct {
	metric *models.Metrics
	store  repository.UpdateMetricStore
}

func (mu *MetricUpdate) UpdateMetric(metric *models.Metrics) error {
	if err := mu.store.Update(metric); err != nil {
		return err
	}
	mu.Print()
	return nil
}

func (mu *MetricUpdate) Print() {
	log.Println("Current Metrics State:")
	for k, v := range mu.store.ListGauges() {
		//log.Println(k, v)
		println("gauge:", k, ", value:", v)
	}
	for k, v := range mu.store.ListCounters() {
		//log.Println(k, v)
		println("counter:", k, ", value:", v)
	}
}
