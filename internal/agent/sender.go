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
	Update(metric *models.Metrics) error
	ListGauges() map[string]float64
	ListCounters() map[string]int64
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

func (a *Agent) collectOnce() {
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
		if err := a.store.Update(&models.Metrics{ID: name, MType: models.Gauge, Value: &gaugeValue}); err != nil {
			a.log.Error("collect gauge error", zap.String("metric", name), zap.Error(err))
		}
	}

	pollInc := int64(1)
	if err := a.store.Update(&models.Metrics{ID: "PollCount", MType: models.Counter, Delta: &pollInc}); err != nil {
		a.log.Error("collect counter error", zap.String("metric", "PollCount"), zap.Error(err))
	}
}

func (a *Agent) sendMetricJSON(ctx context.Context, body *models.Metrics) error {
	metricURL := strings.TrimRight(a.serverAddr, "/") + "/update/"

	reqBody, err := easyjson.Marshal(body)
	if err != nil {
		return err
	}

	compressedBody, err := compress.Compress(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, metricURL, bytes.NewReader(compressedBody))
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
		return fmt.Errorf("%s %s: unexpected status %d", body.MType, body.ID, resp.StatusCode)
	}
	return nil
}

func (a *Agent) reportOnceJSON(ctx context.Context) {
	gauges := a.store.ListGauges()
	counters := a.store.ListCounters()

	for name, value := range gauges {
		if ctx.Err() != nil {
			a.logReportInterrupted(ctx)
			return
		}
		gm := &models.Metrics{
			ID:    name,
			MType: models.Gauge,
			Value: &value,
		}
		if err := a.sendMetricJSON(ctx, gm); err != nil {
			a.logSendError("send gauge error", name, err)
		}
	}
	for name, value := range counters {
		if ctx.Err() != nil {
			a.logReportInterrupted(ctx)
			return
		}
		cm := &models.Metrics{
			ID:    name,
			MType: models.Counter,
			Delta: &value,
		}
		if err := a.sendMetricJSON(ctx, cm); err != nil {
			a.logSendError("send counter error", name, err)
		}
	}
}

func (a *Agent) logReportInterrupted(ctx context.Context) {
	a.log.Warn("report interrupted", zap.Error(ctx.Err()))
}

// logSendError downgrades errors caused by shutdown: they are expected, not failures.
func (a *Agent) logSendError(msg, name string, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		a.log.Debug(msg, zap.String("metric", name), zap.Error(err))
		return
	}
	a.log.Error(msg, zap.String("metric", name), zap.Error(err))
}

func (a *Agent) Run(ctx context.Context) {
	pollTicker := time.NewTicker(a.pollInterval)
	reportTicker := time.NewTicker(a.reportInterval)
	defer pollTicker.Stop()
	defer reportTicker.Stop()

	// first immediate collection
	a.collectOnce()

	for {
		select {
		case <-ctx.Done():
			flushCtx, cancel := context.WithTimeout(context.Background(), shutdownFlushTimeout)
			a.reportOnceJSON(flushCtx) // final report before exit
			cancel()
			return
		case <-pollTicker.C:
			a.collectOnce()
		case <-reportTicker.C:
			a.reportOnceJSON(ctx)
		}
	}
}
