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
	"sync/atomic"
	"time"

	"github.com/mailru/easyjson"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/compress"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/retry"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/signature"
	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"
	"go.uber.org/zap"
)

// shutdownFlushTimeout bounds the whole final report, not a single request.
const shutdownFlushTimeout = 5 * time.Second

// defaultRateLimit is the number of send workers when WithRateLimit is not given.
const defaultRateLimit = 1

// metricStore is the agent's local buffer of collected metrics.
//
// Invariant: at most one report is in flight at a time. A report takes a
// Snapshot, sends it and then calls SubtractCounters with what was sent.
// SubtractCounters is not idempotent: if two reports overlapped, both would
// snapshot and send the same counter deltas, the server would add them twice,
// and both subtractions would drive the local counters negative.
//
// Today this holds because Run starts a single reportLoop, and reportViaPool
// waits until the workers have handled every metric of its snapshot before
// the loop can take the next one. The final flush runs only after that loop
// has stopped. The workers send metrics of one snapshot in parallel, but each
// metric once, so each counter delta is subtracted once.
// Adding a second reporter or a parallel flush requires versioning the batch
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
	signKey        []byte // empty disables signing
	rateLimit      int    // number of send workers, i.e. max concurrent requests
	log            *zap.Logger
}

// Option configures optional Agent settings.
type Option func(*Agent)

// WithSignKey makes the agent sign request bodies with key and send the
// signature in the HashSHA256 header. An empty key disables signing.
func WithSignKey(key []byte) Option {
	return func(a *Agent) { a.signKey = key }
}

// WithRateLimit sets how many requests the agent may have in flight at once:
// Run starts that many send workers. A value below 1 keeps the default of 1;
// the agent config rejects such values before they get here.
func WithRateLimit(n int) Option {
	return func(a *Agent) {
		if n >= 1 {
			a.rateLimit = n
		}
	}
}

func NewAgent(serverAddr string, pollInterval, reportInterval time.Duration, storage metricStore, log *zap.Logger, opts ...Option) *Agent {
	a := &Agent{
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
		rateLimit: defaultRateLimit,
		log:       log,
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
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
//
// With a sign key the uncompressed JSON is signed, not the gzip stream: the
// server checks the signature after decompressing, so it does not depend on
// how the body was compressed.
func (a *Agent) postGzipJSON(ctx context.Context, path string, body easyjson.Marshaler) error {
	reqURL := strings.TrimRight(a.serverAddr, "/") + path

	reqBody, err := easyjson.Marshal(body)
	if err != nil {
		return err
	}

	var sig string
	if len(a.signKey) > 0 {
		sig = signature.Sum(reqBody, a.signKey)
	}

	compressedBody, err := compress.Compress(reqBody)
	if err != nil {
		return err
	}

	return a.retry.Do(ctx, func(ctx context.Context) error {
		return a.post(ctx, reqURL, compressedBody, sig)
	})
}

// post makes a single attempt to send an already compressed JSON body.
// An empty sig sends the request without the HashSHA256 header.
func (a *Agent) post(ctx context.Context, reqURL string, compressedBody []byte, sig string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(compressedBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	if sig != "" {
		req.Header.Set(signature.Header, sig)
	}
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

// sendJob is one metric for the send workers. done is called exactly once,
// with the send error or nil, after the metric has been handled.
type sendJob struct {
	metric models.Metrics
	done   func(err error)
}

// sendWorker sends metrics from jobs until the channel is closed. It does not
// stop on ctx: after cancellation the sends fail at once, and the worker keeps
// draining jobs so that reportViaPool, waiting for them, is not left hanging.
func (a *Agent) sendWorker(ctx context.Context, jobs <-chan sendJob) {
	for job := range jobs {
		job.done(a.sendOne(ctx, job.metric))
	}
}

// sendOne posts a single metric to POST /update/. Once the server has
// accepted it, a counter's sent delta is subtracted; on failure the delta
// stays and goes out with the next report.
func (a *Agent) sendOne(ctx context.Context, metric models.Metrics) error {
	if err := a.sendMetricJSON(ctx, &metric); err != nil {
		a.logSendError("send metric error", 1, err)
		return err
	}
	a.store.SubtractCounters(ctx, []models.Metrics{metric})
	return nil
}

// reportViaPool hands every collected metric to the send workers and waits
// until all of them are handled. The workers are the only goroutines that
// send while Run is reporting, so at most rateLimit requests are in flight.
// Waiting keeps reports from overlapping (see the invariant on metricStore).
// An empty snapshot is not sent.
func (a *Agent) reportViaPool(ctx context.Context, jobs chan<- sendJob) {
	if ctx.Err() != nil {
		a.logReportInterrupted(ctx)
		return
	}

	batch := a.store.Snapshot()
	if len(batch) == 0 {
		return
	}

	start := time.Now()
	var (
		pending sync.WaitGroup
		failed  atomic.Int64
	)
	done := func(err error) {
		if err != nil {
			failed.Add(1)
		}
		pending.Done()
	}

	pending.Add(len(batch))
	for _, m := range batch {
		jobs <- sendJob{metric: m, done: done}
	}
	pending.Wait()

	// duration includes retries, so a slow or flaky server shows up here
	a.log.Info("metrics sent",
		zap.String("server", a.serverAddr),
		zap.Int("count", len(batch)),
		zap.Int64("failed", failed.Load()),
		zap.Duration("duration", time.Since(start)))
}

// Run polls and reports until ctx is cancelled, then makes one final report.
//
// Polling and reporting run in separate goroutines, so a report that waits
// between retries (up to ~9s, or ~21s if the server hangs) does not hold up
// polling: PollCount stays exact and the next report has fresh values. There
// is still a single reporting goroutine, so reports never overlap. The store
// is safe for concurrent use, and SubtractCounters keeps polls that happen
// while a report is in flight.
//
// Reports are sent by a pool of rateLimit workers, started once for the whole
// run, one metric per request.
//
// System metrics (gopsutil) are polled in a goroutine of their own, also every
// pollInterval. They are not collected right away: the first CPU sample would
// cover only the few microseconds since the agent started.
func (a *Agent) Run(ctx context.Context) {
	// first immediate collection
	a.collectOnce(ctx)

	jobs := make(chan sendJob)
	var workers sync.WaitGroup
	for range a.rateLimit {
		workers.Go(func() { a.sendWorker(ctx, jobs) })
	}

	var wg sync.WaitGroup
	wg.Go(func() { a.pollLoop(ctx, a.collectOnce) })
	wg.Go(func() { a.pollLoop(ctx, a.collectSystemOnce) })
	wg.Go(func() { a.reportLoop(ctx, jobs) })
	wg.Wait()

	// reportLoop has waited for every job it queued, so none is pending.
	close(jobs)
	workers.Wait()

	// Everything else has stopped, so the final report runs alone and includes
	// the last poll. ctx is already done, hence a fresh context for it. It is a
	// single batch request, so it stays within any rate limit.
	flushCtx, cancel := context.WithTimeout(context.Background(), shutdownFlushTimeout)
	defer cancel()
	a.reportOnceJSON(flushCtx)
}

// pollLoop runs collect every pollInterval until ctx is cancelled.
func (a *Agent) pollLoop(ctx context.Context, collect func(context.Context)) {
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			collect(ctx)
		}
	}
}

// reportLoop hands collected metrics to the send workers every reportInterval
// until ctx is cancelled. If a report with retries outlasts the interval, the
// next one starts right after it: ticks that arrive meanwhile are dropped by
// the ticker, not queued.
func (a *Agent) reportLoop(ctx context.Context, jobs chan<- sendJob) {
	ticker := time.NewTicker(a.reportInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			a.reportViaPool(ctx, jobs)
		}
	}
}
