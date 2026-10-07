package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/mailru/easyjson"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/compress"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

// shutdownFlushTimeout bounds the whole final report, not a single request.
const shutdownFlushTimeout = 5 * time.Second

type metricStore interface {
	Update(ctx context.Context, metric *models.Metrics) error
	Snapshot() []models.Metrics
	SubtractCounters(ctx context.Context, sent []models.Metrics)
}

type Agent struct {
	store          metricStore
	serverAddr     string
	pollInterval   time.Duration
	reportInterval time.Duration
	client         *http.Client
	log            *zap.Logger
}

func NewAgent(serverAddr string, pollInterval, reportInterval time.Duration, storage metricStore, log *zap.Logger) *Agent {
	return &Agent{
		store:          storage,
		serverAddr:     serverAddr,
		pollInterval:   pollInterval,
		reportInterval: reportInterval,
		client: &http.Client{
			Timeout: 3 * time.Second,
		},
		log: log,
	}
}

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

	for name, value := range metrics {
		gaugeValue := value
		if err := a.store.Update(ctx, &models.Metrics{ID: name, MType: models.Gauge, Value: &gaugeValue}); err != nil {
			a.log.Error("collect gauge error", zap.String("metric", name), zap.Error(err))
		}
	}

	pollInc := int64(1)
	if err := a.store.Update(ctx, &models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &pollInc}); err != nil {
		a.log.Error("collect counter error", zap.String("metric", "PollCount"), zap.Error(err))
	}
}

// sendMetricJSON posts a single metric to POST /update/.
func (a *Agent) sendMetricJSON(ctx context.Context, body *models.Metrics) error {
	if err := a.postGzipJSON(ctx, "/update/", body); err != nil {
		return fmt.Errorf("%s %s: %w", body.MType, body.ID, err)
	}
	return nil
}

// sendBatchJSON posts all metrics in a single request to POST /updates/.
func (a *Agent) sendBatchJSON(ctx context.Context, batch []models.Metrics) error {
	if err := a.postGzipJSON(ctx, "/updates/", models.MetricsList(batch)); err != nil {
		return fmt.Errorf("batch of %d metrics: %w", len(batch), err)
	}
	return nil
}

// postGzipJSON marshals body, compresses it with gzip and posts it to path on
// the server. Any status other than 200 OK is an error.
func (a *Agent) postGzipJSON(ctx context.Context, path string, body easyjson.Marshaler) error {
	reqURL := strings.TrimRight(a.serverAddr, "/") + path

	reqBody, err := easyjson.Marshal(body)
	if err != nil {
		return err
	}

	compressedBody, err := compress.Compress(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(compressedBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			a.log.Error("close response body error", zap.Error(closeErr))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

// reportOnceJSON sends all collected metrics as one batch. Counters hold the
// delta accumulated since the last successful report: once the server has
// accepted the batch, the sent deltas are subtracted. If sending fails, the
// deltas stay and go out with the next batch. An empty batch is not sent.
func (a *Agent) reportOnceJSON(ctx context.Context) {
	if ctx.Err() != nil {
		a.logReportInterrupted(ctx)
		return
	}

	batch := a.store.Snapshot()
	if len(batch) == 0 {
		return
	}

	if err := a.sendBatchJSON(ctx, batch); err != nil {
		a.logSendError("send metrics batch error", len(batch), err)
		return
	}
	a.store.SubtractCounters(ctx, batch)
}

func (a *Agent) logReportInterrupted(ctx context.Context) {
	a.log.Warn("report interrupted", zap.Error(ctx.Err()))
}

// logSendError downgrades errors caused by shutdown: they are expected, not failures.
func (a *Agent) logSendError(msg string, count int, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		a.log.Debug(msg, zap.Int("count", count), zap.Error(err))
		return
	}
	a.log.Error(msg, zap.Int("count", count), zap.Error(err))
}

func (a *Agent) Run(ctx context.Context) {
	pollTicker := time.NewTicker(a.pollInterval)
	reportTicker := time.NewTicker(a.reportInterval)
	defer pollTicker.Stop()
	defer reportTicker.Stop()

	// first immediate collection
	a.collectOnce(ctx)

	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), shutdownFlushTimeout)
			a.reportOnceJSON(flushCtx) // final report before exit
			cancel()
			return
		case <-pollTicker.C:
			a.collectOnce(ctx)
		case <-reportTicker.C:
			a.reportOnceJSON(ctx)
		}
	}
}
