package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/mailru/easyjson"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/compress"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/retry"
	"go.uber.org/zap"
)

// shutdownFlushTimeout bounds the whole final report, not a single request.
const shutdownFlushTimeout = 5 * time.Second

// metricStore is the agent's local buffer of collected metrics.
//
// Invariant: at most one report is in flight at a time. reportOnceJSON takes a
// Snapshot, sends it and then calls SubtractCounters with the same batch.
// SubtractCounters is not idempotent: if two reports overlapped, both would
// snapshot and send the same counter deltas, the server would add them twice,
// and both subtractions would drive the local counters negative.
//
// Today this holds because Run starts a single reportLoop, which reports
// sequentially, and the final flush runs only after that loop has stopped.
// Adding a second sender or a parallel flush requires versioning the batch
// (e.g. subtract only deltas of a snapshot that was not yet acknowledged).
type metricStore interface {
	UpdateBatch(ctx context.Context, metrics []models.Metrics) error
	Snapshot() []models.Metrics
	SubtractCounters(ctx context.Context, sent []models.Metrics)
}

type Agent struct {
	store          metricStore
	serverAddr     string
	pollInterval   time.Duration
	reportInterval time.Duration
	client         *http.Client
	retry          retry.Policy
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
		retry: retry.Policy{
			Delays:    retry.DefaultDelays,
			Retriable: isRetriableSendError,
			OnRetry: func(attempt int, delay time.Duration, err error) {
				log.Warn("send failed, retrying",
					zap.Int("attempt", attempt), zap.Duration("delay", delay), zap.Error(err))
			},
		},
		log: log,
	}
}

// statusError is a response with a status other than 200 OK.
type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("unexpected status %d", e.code)
}

// isRetriableSendError reports whether a failed request is worth repeating:
// a transport error (the server is unreachable, the connection was reset or
// timed out) or a status saying the server is temporarily unavailable.
// 400 is not retried: the same body would be rejected again. Neither is 500:
// the server has already retried its own storage before answering.
//
// Context errors are deliberately not filtered out here. Since Go 1.23 a
// http.Client timeout matches context.DeadlineExceeded too, so such a filter
// would also stop retries of a hung server. A cancelled agent (shutdown) is
// handled by retry.Policy.Do, which checks its own ctx before retrying.
func isRetriableSendError(err error) bool {
	if statusErr, ok := errors.AsType[*statusError](err); ok {
		switch statusErr.code {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		return false
	}

	// http.Client.Do reports every transport failure as *url.Error.
	_, ok := errors.AsType[*url.Error](err)
	return ok
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
// the server. Any status other than 200 OK is an error. Temporary failures
// are retried according to a.retry; the body is compressed once and a new
// request is built for every attempt, since a sent request body is consumed.
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

	return a.retry.Do(ctx, func(ctx context.Context) error {
		return a.post(ctx, reqURL, compressedBody)
	})
}

// post makes a single attempt to send an already compressed JSON body.
func (a *Agent) post(ctx context.Context, reqURL string, compressedBody []byte) error {
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
		return &statusError{code: resp.StatusCode}
	}
	return nil
}

// reportOnceJSON sends all collected metrics as one batch. Counters hold the
// delta accumulated since the last successful report: once the server has
// accepted the batch, the sent deltas are subtracted. If sending fails even
// after the retries, the deltas stay and go out with the next batch. An empty
// batch is not sent.
//
// Polls keep running in their own goroutine while the batch is in flight
// (see Run). Subtracting the sent deltas, rather than resetting counters to
// zero, keeps the increments made meanwhile.
//
// Must not run concurrently with itself: see the invariant on metricStore.
func (a *Agent) reportOnceJSON(ctx context.Context) {
	if ctx.Err() != nil {
		a.logReportInterrupted(ctx)
		return
	}

	batch := a.store.Snapshot()
	if len(batch) == 0 {
		return
	}

	start := time.Now()
	if err := a.sendBatchJSON(ctx, batch); err != nil {
		a.logSendError("send metrics batch error", len(batch), err)
		return
	}
	a.store.SubtractCounters(ctx, batch)

	// duration includes retries, so a slow or flaky server shows up here
	a.log.Info("metrics batch sent",
		zap.String("server", a.serverAddr),
		zap.Int("count", len(batch)),
		zap.Duration("duration", time.Since(start)))
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

// Run polls and reports until ctx is cancelled, then makes one final report.
//
// Polling and reporting run in separate goroutines, so a report that waits
// between retries (up to ~9s, or ~21s if the server hangs) does not hold up
// polling: PollCount stays exact and the next batch has fresh values. There
// is still a single reporting goroutine, so batches never overlap. The store
// is safe for concurrent use, and SubtractCounters keeps polls that happen
// while a batch is in flight.
func (a *Agent) Run(ctx context.Context) {
	// first immediate collection
	a.collectOnce(ctx)

	var wg sync.WaitGroup
	wg.Go(func() { a.pollLoop(ctx) })
	wg.Go(func() { a.reportLoop(ctx) })
	wg.Wait()

	// Both loops have stopped, so the final report runs alone and includes the
	// last poll. ctx is already done, hence a fresh context for it.
	flushCtx, cancel := context.WithTimeout(context.Background(), shutdownFlushTimeout)
	defer cancel()
	a.reportOnceJSON(flushCtx)
}

// pollLoop collects metrics every pollInterval until ctx is cancelled.
func (a *Agent) pollLoop(ctx context.Context) {
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.collectOnce(ctx)
		}
	}
}

// reportLoop sends a batch every reportInterval until ctx is cancelled. If a
// report with retries outlasts the interval, the next one starts right after
// it: ticks that arrive meanwhile are dropped by the ticker, not queued.
func (a *Agent) reportLoop(ctx context.Context) {
	ticker := time.NewTicker(a.reportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.reportOnceJSON(ctx)
		}
	}
}
