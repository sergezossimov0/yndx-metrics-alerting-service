package repository

import (
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
)

// BenchmarkMemStorage measures a mix of writes (Update) and reads (GetGauge)
// on 30 gauges, roughly one agent report, at different write ratios.
//
// It backs the choice of sync.Mutex over sync.RWMutex in MemStorage:
//
//	go test -run '^$' -bench BenchmarkMemStorage ./internal/repository/          # with contention
//	go test -run '^$' -bench BenchmarkMemStorage -count=10 -cpu 1 ./internal/repository/   # without contention
//
// To compare with RWMutex, change the mutex type and the read methods to
// RLock/RUnlock and run the same commands.
func BenchmarkMemStorage(b *testing.B) {
	const metrics = 30

	keys := make([]string, metrics)
	for i := range keys {
		keys[i] = "gauge" + strconv.Itoa(i)
	}

	for _, writePercent := range []int{100, 50, 10, 1} {
		b.Run(fmt.Sprintf("writes=%d%%", writePercent), func(b *testing.B) {
			s := NewMemStorage()
			for i, k := range keys {
				v := float64(i)
				_ = s.Update(&models.Metrics{ID: k, MType: models.Gauge, Value: &v})
			}

			var op atomic.Uint64
			b.ReportAllocs()
			b.ResetTimer()
			b.RunParallel(func(pb *testing.PB) {
				for pb.Next() {
					i := op.Add(1)
					k := keys[i%metrics]
					if int(i%100) < writePercent {
						v := float64(i)
						_ = s.Update(&models.Metrics{ID: k, MType: models.Gauge, Value: &v})
					} else {
						_, _ = s.GetGauge(k)
					}
				}
			})
		})
	}
}
