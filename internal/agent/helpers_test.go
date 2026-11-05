package agent

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// decodeGzipJSONBody decompresses a gzip-encoded request body and decodes it as JSON.
func decodeGzipJSONBody(r *http.Request, out interface{}) error {
	zr, err := gzip.NewReader(r.Body)
	if err != nil {
		return err
	}
	defer zr.Close()
	return json.NewDecoder(zr).Decode(out)
}

// metricStoreMock lets tests force UpdateBatch to fail, which the real
// repository.MemStorage never does — needed to exercise the error-logging
// branch in collectOnce. It also records the batches collectOnce writes.
type metricStoreMock struct {
	updateErr  error
	metrics    []models.Metrics
	subtracted []models.Metrics
	updates    [][]models.Metrics
}

func (m *metricStoreMock) UpdateBatch(_ context.Context, metrics []models.Metrics) error {
	m.updates = append(m.updates, metrics)
	return m.updateErr
}

func (m *metricStoreMock) Snapshot() []models.Metrics {
	return m.metrics
}

func (m *metricStoreMock) SubtractCounters(_ context.Context, sent []models.Metrics) {
	m.subtracted = sent
}

// newObservedLogger returns a logger that keeps entries in memory, so a test
// can inspect what a component logged without touching any global state.
func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

func counterMetric(id string, d int64) models.Metrics {
	return models.Metrics{ID: id, MType: models.Counter, Delta: &d}
}

// countingServer counts the requests it receives and answers with status.
func countingServer(t *testing.T, status int) (*httptest.Server, func() int) {
	t.Helper()
	var mu sync.Mutex
	count := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count++
		mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(ts.Close)
	return ts, func() int {
		mu.Lock()
		defer mu.Unlock()
		return count
	}
}
