package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"go.uber.org/zap"
)

// pollCountingStore wraps MemStorage and records when every PollCount
// increment happened, so a test can tell whether polling went on while a
// report was in flight.
type pollCountingStore struct {
	*repository.MemStorage

	mu    sync.Mutex
	polls []time.Time
}

func (s *pollCountingStore) UpdateBatch(ctx context.Context, metrics []models.Metrics) error {
	for _, m := range metrics {
		if m.ID == "PollCount" {
			s.mu.Lock()
			s.polls = append(s.polls, time.Now())
			s.mu.Unlock()
		}
	}
	return s.MemStorage.UpdateBatch(ctx, metrics)
}

func (s *pollCountingStore) pollsBetween(from, to time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, p := range s.polls {
		if p.After(from) && p.Before(to) {
			n++
		}
	}
	return n
}

func (s *pollCountingStore) totalPolls() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.polls)
}

// decodeSentMetrics reads the batch of one agent request to /updates/.
func decodeSentMetrics(r *http.Request) []models.Metrics {
	var batch []models.Metrics
	_ = decodeGzipJSONBody(r, &batch)
	return batch
}

// Run with -race: polls and reports now run concurrently on the same store.
func TestAgent_Run_KeepsPollingWhileReportRetries(t *testing.T) {
	var (
		mu             sync.Mutex
		pollCountTimes []time.Time // requests carrying PollCount
		received       int64       // PollCount deltas the server accepted
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics := decodeSentMetrics(r)

		mu.Lock()
		defer mu.Unlock()
		for _, m := range metrics {
			if m.ID != "PollCount" || m.Delta == nil {
				continue
			}
			pollCountTimes = append(pollCountTimes, time.Now())
			if len(pollCountTimes) == 1 {
				// the first PollCount fails and has to wait for its retry
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			received += *m.Delta
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	store := &pollCountingStore{MemStorage: repository.NewMemStorage()}
	a := NewAgent(ts.URL, 5*time.Millisecond, 30*time.Millisecond, store, zap.NewNop())
	const retryPause = 200 * time.Millisecond
	a.retry.Delays = []time.Duration{retryPause}

	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(pollCountTimes) < 2 {
		t.Fatalf("expected the failed PollCount to be retried, got %d requests", len(pollCountTimes))
	}

	// With polling and reporting in one goroutine at most one poll could
	// happen during the retry pause; with separate goroutines it is ~40.
	if n := store.pollsBetween(pollCountTimes[0], pollCountTimes[1]); n < 10 {
		t.Fatalf("expected polling to continue during the %v retry pause, got %d polls", retryPause, n)
	}

	// Every poll is delivered exactly once: nothing lost while the batch was
	// in flight, nothing sent twice.
	if want := int64(store.totalPolls()); received != want {
		t.Fatalf("expected the server to receive all %d polls, got %d", want, received)
	}
	if left, _ := store.GetCounter(context.Background(), "PollCount"); left != 0 {
		t.Fatalf("expected no PollCount delta left after the final report, got %d", left)
	}
}

func TestAgent_Run_FinalReportIncludesLastPolls(t *testing.T) {
	var (
		mu       sync.Mutex
		received int64
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		metrics := decodeSentMetrics(r)
		mu.Lock()
		for _, m := range metrics {
			if m.ID == "PollCount" && m.Delta != nil {
				received += *m.Delta
			}
		}
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	store := &pollCountingStore{MemStorage: repository.NewMemStorage()}
	// the report interval is longer than the run: only the final report sends
	a := NewAgent(ts.URL, 5*time.Millisecond, time.Hour, store, zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if want := int64(store.totalPolls()); want < 2 || received != want {
		t.Fatalf("expected the final report to carry all %d polls, got %d", want, received)
	}
}

// Every report is one batch, and the next snapshot is taken only after the
// server has answered: however many workers there are, requests never
// overlap. That is both within RATE_LIMIT and what keeps counter deltas from
// being sent twice (see the invariant on metricStore).
func TestAgent_Run_SendsOneBatchAtATimeWithinRateLimit(t *testing.T) {
	const limit = 3
	var (
		inFlight    atomic.Int64
		maxInFlight atomic.Int64
		requests    atomic.Int64
	)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Only the periodic reports count. The final report goes after the
		// pool has stopped, but it may still meet handlers of requests the
		// client abandoned on cancel: time.Sleep below does not notice that.
		//
		// ctx.Err() is set before ctx.Done() is closed, and Run starts the
		// final report only after its loops have seen Done, so the final
		// request always finds ctx.Err() != nil here. (A flag cleared from
		// context.AfterFunc would race: AfterFunc runs in its own goroutine.)
		// A periodic request that reaches the server after cancel is skipped
		// too, which can only lower the counts, never fail the test falsely.
		if ctx.Err() != nil {
			w.WriteHeader(http.StatusOK)
			return
		}
		requests.Add(1)
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		// hold the request longer than reportInterval, so that a report which
		// did not wait for the previous one would overlap it
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL, 5*time.Millisecond, 10*time.Millisecond, repository.NewMemStorage(), zap.NewNop(),
		WithRateLimit(limit))

	a.Run(ctx)

	if requests.Load() < 2 {
		t.Fatalf("expected several periodic reports, got %d", requests.Load())
	}
	if got := maxInFlight.Load(); got != 1 {
		t.Fatalf("expected reports to go one at a time with %d workers, got %d concurrent requests", limit, got)
	}
}

// Every report carries the whole snapshot in one request to /updates/:
// the agent never posts single metrics to /update/.
func TestAgent_Run_SendsWholeSnapshotAsOneBatch(t *testing.T) {
	var (
		mu      sync.Mutex
		paths   []string
		batches [][]models.Metrics
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		batch := decodeSentMetrics(r)
		mu.Lock()
		paths = append(paths, r.URL.Path)
		batches = append(batches, batch)
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL, 5*time.Millisecond, 20*time.Millisecond, repository.NewMemStorage(), zap.NewNop(),
		WithRateLimit(3))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	mu.Lock()
	defer mu.Unlock()
	if len(paths) < 2 {
		t.Fatalf("expected several reports, got %d requests", len(paths))
	}
	for i, p := range paths {
		if p != "/updates/" {
			t.Fatalf("request %d: expected /updates/, got %s", i, p)
		}
		// runtime metrics alone are ~29, so a request with a handful of
		// metrics would mean the snapshot was split
		if n := len(batches[i]); n < 20 {
			t.Fatalf("request %d: expected the whole snapshot in one batch, got %d metrics", i, n)
		}
	}
}

func TestWithRateLimit_IgnoresValuesBelowOne(t *testing.T) {
	for _, n := range []int{0, -1} {
		a := NewAgent("http://localhost:8080", time.Second, time.Second, repository.NewMemStorage(), zap.NewNop(),
			WithRateLimit(n))
		if a.rateLimit != defaultRateLimit {
			t.Fatalf("WithRateLimit(%d): expected the default %d, got %d", n, defaultRateLimit, a.rateLimit)
		}
	}
}

func TestAgent_Run_StopsWhenContextIsCancelled(t *testing.T) {
	a := NewAgent("http://localhost:8080", time.Second, time.Second, &metricStoreMock{}, zap.NewNop())

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected Run to return after context cancellation")
	}
}

func TestAgent_Run_CollectsAndReportsBeforeStopping(t *testing.T) {
	var mu sync.Mutex
	requestCount := 0
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestCount++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	// sub-second intervals keep the test fast and deterministic
	a := NewAgent(ts.URL, 15*time.Millisecond, 25*time.Millisecond, repository.NewMemStorage(), zap.NewNop())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		a.Run(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("expected Run to return after context deadline")
	}

	mu.Lock()
	defer mu.Unlock()
	if requestCount == 0 {
		t.Fatal("expected at least one metrics batch to reach the server")
	}
}
