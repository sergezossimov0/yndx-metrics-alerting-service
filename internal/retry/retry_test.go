package retry

import (
	"context"
	"errors"
	"testing"
	"time"
)

var (
	errTemporary = errors.New("temporary")
	errPermanent = errors.New("permanent")
)

func isTemporary(err error) bool { return errors.Is(err, errTemporary) }

// fastPolicy keeps the shape of DefaultDelays (three retries) but in
// milliseconds, so the tests stay fast.
func fastPolicy() Policy {
	return Policy{
		Delays:    []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond},
		Retriable: isTemporary,
	}
}

// failing returns fn that fails with errs in order and then succeeds, and a
// counter of its calls.
func failing(errs ...error) (func(context.Context) error, *int) {
	calls := 0
	return func(context.Context) error {
		calls++
		if calls <= len(errs) {
			return errs[calls-1]
		}
		return nil
	}, &calls
}

func TestDefaultDelays_MatchTheSpec(t *testing.T) {
	want := []time.Duration{time.Second, 3 * time.Second, 5 * time.Second}
	if len(DefaultDelays) != len(want) {
		t.Fatalf("expected %v, got %v", want, DefaultDelays)
	}
	for i := range want {
		if DefaultDelays[i] != want[i] {
			t.Fatalf("expected %v, got %v", want, DefaultDelays)
		}
	}
}

func TestDo_SucceedsOnFirstAttempt(t *testing.T) {
	fn, calls := failing()

	if err := fastPolicy().Do(context.Background(), fn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected 1 call, got %d", *calls)
	}
}

func TestDo_SucceedsAfterTemporaryErrors(t *testing.T) {
	fn, calls := failing(errTemporary, errTemporary)

	if err := fastPolicy().Do(context.Background(), fn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *calls != 3 {
		t.Fatalf("expected 3 calls, got %d", *calls)
	}
}

func TestDo_GivesUpAfterThreeRetries(t *testing.T) {
	fn, calls := failing(errTemporary, errTemporary, errTemporary, errTemporary, errTemporary)

	err := fastPolicy().Do(context.Background(), fn)

	if !errors.Is(err, errTemporary) {
		t.Fatalf("expected the last error to be wrapped, got %v", err)
	}
	if *calls != 4 {
		t.Fatalf("expected 1 attempt + 3 retries = 4 calls, got %d", *calls)
	}
	if got := err.Error(); got != "after 4 attempts: temporary" {
		t.Fatalf("expected the error to report the attempts, got %q", got)
	}
}

func TestDo_DoesNotRetryPermanentError(t *testing.T) {
	fn, calls := failing(errPermanent)

	err := fastPolicy().Do(context.Background(), fn)

	if err != errPermanent {
		t.Fatalf("expected the first-attempt error as is, got %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected 1 call, got %d", *calls)
	}
}

func TestDo_NilRetriableMeansSingleAttempt(t *testing.T) {
	fn, calls := failing(errTemporary)
	p := fastPolicy()
	p.Retriable = nil

	if err := p.Do(context.Background(), fn); err == nil {
		t.Fatal("expected an error")
	}
	if *calls != 1 {
		t.Fatalf("expected 1 call, got %d", *calls)
	}
}

func TestDo_CallsOnRetryBeforeEachPause(t *testing.T) {
	fn, _ := failing(errTemporary, errTemporary)
	p := fastPolicy()
	var attempts []int
	var delays []time.Duration
	p.OnRetry = func(attempt int, delay time.Duration, err error) {
		attempts = append(attempts, attempt)
		delays = append(delays, delay)
	}

	if err := p.Do(context.Background(), fn); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(attempts) != 2 || attempts[0] != 1 || attempts[1] != 2 {
		t.Fatalf("expected OnRetry after attempts 1 and 2, got %v", attempts)
	}
	if delays[0] != p.Delays[0] || delays[1] != p.Delays[1] {
		t.Fatalf("expected the configured delays, got %v", delays)
	}
}

func TestDo_StopsWaitingWhenContextIsCancelled(t *testing.T) {
	p := Policy{Delays: []time.Duration{time.Minute}, Retriable: isTemporary}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(20*time.Millisecond, cancel)

	start := time.Now()
	err := p.Do(ctx, func(context.Context) error { return errTemporary })

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected Do to stop on cancellation, it took %v", elapsed)
	}
	if !errors.Is(err, context.Canceled) || !errors.Is(err, errTemporary) {
		t.Fatalf("expected both the cancellation and the last error, got %v", err)
	}
}

func TestDo_DoesNotRetryWhenContextIsAlreadyDone(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fn, calls := failing(errTemporary, errTemporary)

	err := fastPolicy().Do(ctx, fn)

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if *calls != 1 {
		t.Fatalf("expected 1 call, got %d", *calls)
	}
}
