// Package retry repeats an operation that failed with a temporary error,
// waiting longer before each next attempt.
package retry

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// DefaultDelays are the pauses before the 1st, 2nd and 3rd retry: one attempt
// plus at most three retries, waiting 1s, 3s and 5s between them.
var DefaultDelays = []time.Duration{1 * time.Second, 3 * time.Second, 5 * time.Second}

// Policy describes when and how often to retry.
type Policy struct {
	// Delays holds the pause before each retry; its length is the maximum
	// number of retries. Empty means a single attempt.
	Delays []time.Duration
	// Retriable reports whether err is temporary and worth another attempt.
	// Nil means no error is retried.
	Retriable func(err error) bool
	// OnRetry, if set, is called before each pause, e.g. to log the failure.
	// attempt is the number of the attempt that has just failed, from 1.
	OnRetry func(attempt int, delay time.Duration, err error)
}

// Do calls fn until it succeeds, fails with an error that is not retriable,
// the retries run out, or ctx is done. The pauses between attempts are
// interrupted by ctx, so a shutdown does not wait for them.
//
// An error from the first attempt is returned as is. After retries the error
// says how many attempts were made and wraps the last error; if ctx ended
// the retries, ctx.Err() is wrapped too, so errors.Is(err, context.Canceled)
// works for the caller.
func (p Policy) Do(ctx context.Context, fn func(ctx context.Context) error) error {
	for attempt := 1; ; attempt++ {
		err := fn(ctx)
		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			return wrap(attempt, errors.Join(err, ctx.Err()))
		}
		if attempt > len(p.Delays) || p.Retriable == nil || !p.Retriable(err) {
			return wrap(attempt, err)
		}

		delay := p.Delays[attempt-1]
		if p.OnRetry != nil {
			p.OnRetry(attempt, delay, err)
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return wrap(attempt, errors.Join(err, ctx.Err()))
		case <-timer.C:
		}
	}
}

func wrap(attempts int, err error) error {
	if attempts == 1 {
		return err
	}
	return fmt.Errorf("after %d attempts: %w", attempts, err)
}
