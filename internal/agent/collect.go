package agent

import (
	"context"
	"fmt"
	"math/rand"
	"runtime"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"go.uber.org/zap"
)

func (a *Agent) collectOnce(ctx context.Context) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)

	metrics := map[string]float64{
		"Alloc":         float64(m.Alloc),
		"BuckHashSys":   float64(m.BuckHashSys),
		"Frees":         float64(m.Frees),
		"GCCPUFraction": m.GCCPUFraction,
		"GCSys":         float64(m.GCSys),
		"HeapAlloc":     float64(m.HeapAlloc),
		"HeapIdle":      float64(m.HeapIdle),
		"HeapInuse":     float64(m.HeapInuse),
		"HeapObjects":   float64(m.HeapObjects),
		"HeapReleased":  float64(m.HeapReleased),
		"HeapSys":       float64(m.HeapSys),
		"LastGC":        float64(m.LastGC),
		"Lookups":       float64(m.Lookups),
		"MCacheInuse":   float64(m.MCacheInuse),
		"MCacheSys":     float64(m.MCacheSys),
		"MSpanInuse":    float64(m.MSpanInuse),
		"MSpanSys":      float64(m.MSpanSys),
		"Mallocs":       float64(m.Mallocs),
		"NextGC":        float64(m.NextGC),
		"NumForcedGC":   float64(m.NumForcedGC),
		"NumGC":         float64(m.NumGC),
		"OtherSys":      float64(m.OtherSys),
		"PauseTotalNs":  float64(m.PauseTotalNs),
		"StackInuse":    float64(m.StackInuse),
		"StackSys":      float64(m.StackSys),
		"Sys":           float64(m.Sys),
		"TotalAlloc":    float64(m.TotalAlloc),
		"RandomValue":   rand.Float64(),
	}

	batch := make([]models.Metrics, 0, len(metrics)+1)
	for name, value := range metrics {
		batch = append(batch, models.Metrics{ID: name, MType: models.Gauge, Value: &value})
	}
	pollInc := int64(1)
	batch = append(batch, models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &pollInc})

	// One UpdateBatch, not an Update per metric: reporting runs in another
	// goroutine, and a snapshot taken between separate updates would mix
	// values of two polls. The store applies the batch under one lock.
	if err := a.store.UpdateBatch(ctx, batch); err != nil {
		a.log.Error("collect metrics error", zap.Int("count", len(batch)), zap.Error(err))
	}
}

// collectSystemOnce reads system memory and per-CPU utilization with gopsutil
// and stores them as gauges: TotalMemory, FreeMemory and CPUutilization1..N,
// one per logical CPU. If one source fails, the other is still stored.
//
// CPU utilization is measured since the previous call (interval 0), so the
// call does not block and, run every pollInterval, each value covers one
// poll interval. gopsutil keeps that previous sample in package state; the
// agent has a single caller, so nothing else moves it.
func (a *Agent) collectSystemOnce(ctx context.Context) {
	var batch []models.Metrics

	if vm, err := mem.VirtualMemoryWithContext(ctx); err != nil {
		a.log.Error("collect memory metrics error", zap.Error(err))
	} else {
		batch = append(batch,
			gaugeMetric("TotalMemory", float64(vm.Total)),
			gaugeMetric("FreeMemory", float64(vm.Free)))
	}

	if percents, err := cpu.PercentWithContext(ctx, 0, true); err != nil {
		a.log.Error("collect cpu metrics error", zap.Error(err))
	} else {
		for i, p := range percents {
			batch = append(batch, gaugeMetric(fmt.Sprintf("CPUutilization%d", i+1), p))
		}
	}

	if len(batch) == 0 {
		return
	}
	// one UpdateBatch, so a snapshot never mixes values of two polls
	if err := a.store.UpdateBatch(ctx, batch); err != nil {
		a.log.Error("collect system metrics error", zap.Int("count", len(batch)), zap.Error(err))
	}
}

func gaugeMetric(name string, value float64) models.Metrics {
	return models.Metrics{ID: name, MType: models.Gauge, Value: &value}
}
