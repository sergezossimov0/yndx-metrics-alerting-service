package agent

import (
	"context"
	"net/http"
	"sync"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/retry"
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
