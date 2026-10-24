package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

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
