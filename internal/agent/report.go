package agent

import (
	"context"
	"errors"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

// sendFunc delivers one snapshot to the server and subtracts the sent counter
// deltas once the server has accepted it (see sendSnapshot).
type sendFunc func(ctx context.Context, batch []models.Metrics) error

// report takes a snapshot of the collected metrics and delivers it with send
// as one batch. An empty snapshot is not sent.
//
// Must not run concurrently with itself: see the invariant on metricStore.
func (a *Agent) report(ctx context.Context, send sendFunc) {
	if ctx.Err() != nil {
		a.log.Warn("report interrupted", zap.Error(ctx.Err()))
		return
	}

	batch := a.store.Snapshot()
	if len(batch) == 0 {
		return
	}

	start := time.Now()
	if err := send(ctx, batch); err != nil {
		a.logSendError("send metrics batch error", len(batch), err)
		return
	}

	// duration includes retries, so a slow or flaky server shows up here
	a.log.Info("metrics batch sent",
		zap.String("server", a.serverAddr),
		zap.Int("count", len(batch)),
		zap.Duration("duration", time.Since(start)))
}

// reportOnceJSON sends the snapshot from the calling goroutine. Run uses it
// for the final report, after the send workers have stopped.
func (a *Agent) reportOnceJSON(ctx context.Context) {
	a.report(ctx, a.sendSnapshot)
}

// reportViaPool hands the snapshot to the send workers as a single job and
// waits for its result. Waiting keeps reports from overlapping (see the
// invariant on metricStore).
func (a *Agent) reportViaPool(ctx context.Context, jobs chan<- sendJob) {
	a.report(ctx, func(ctx context.Context, batch []models.Metrics) error {
		// buffered, so the worker never blocks on handing back the result
		done := make(chan error, 1)
		jobs <- sendJob{batch: batch, done: done}
		return <-done
	})
}

// sendSnapshot posts batch to POST /updates/ in one request. Counters hold the
// delta accumulated since the last successful report: once the server has
// accepted the batch, the sent deltas are subtracted. If sending fails even
// after the retries, the deltas stay and go out with the next batch.
//
// Polls keep running in their own goroutines while the batch is in flight
// (see Run). Subtracting the sent deltas, rather than resetting counters to
// zero, keeps the increments made meanwhile.
func (a *Agent) sendSnapshot(ctx context.Context, batch []models.Metrics) error {
	if err := a.sendBatchJSON(ctx, batch); err != nil {
		return err
	}
	a.store.SubtractCounters(ctx, batch)
	return nil
}

// logSendError downgrades errors caused by shutdown: they are expected, not failures.
func (a *Agent) logSendError(msg string, count int, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		a.log.Debug(msg, zap.Int("count", count), zap.Error(err))
		return
	}
	a.log.Error(msg, zap.Int("count", count), zap.Error(err))
}

// sendJob is one snapshot for the send workers. The worker sends the result
// of sendSnapshot to done exactly once.
type sendJob struct {
	batch []models.Metrics
	done  chan<- error
}

// sendWorker sends snapshots from jobs until the channel is closed. It does
// not stop on ctx: after cancellation the sends fail at once, and the worker
// keeps draining jobs so that reportViaPool, waiting for a result, is not
// left hanging.
func (a *Agent) sendWorker(ctx context.Context, jobs <-chan sendJob) {
	for job := range jobs {
		job.done <- a.sendSnapshot(ctx, job.batch)
	}
}
