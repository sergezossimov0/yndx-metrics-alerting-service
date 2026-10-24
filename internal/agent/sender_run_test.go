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

// decodeSentMetrics reads the metrics of one agent request: a single metric
// posted to /update/ by the send workers, or a batch posted to /updates/ by
// the final report.
func decodeSentMetrics(r *http.Request) []models.Metrics {
	if r.URL.Path == "/updates/" {
		var batch []models.Metrics
		_ = decodeGzipJSONBody(r, &batch)
		return batch
	}
	var m models.Metrics
	_ = decodeGzipJSONBody(r, &m)
	return []models.Metrics{m}
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

func TestAgent_Run_LimitsConcurrentRequests(t *testing.T) {
	const limit = 3
	var (
		inFlight    atomic.Int64
		maxInFlight atomic.Int64
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := inFlight.Add(1)
		defer inFlight.Add(-1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		// hold the request so that the workers pile up on the server
		time.Sleep(20 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	a := NewAgent(ts.URL, 5*time.Millisecond, 10*time.Millisecond, repository.NewMemStorage(), zap.NewNop(),
		WithRateLimit(limit))

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	a.Run(ctx)

	// ~29 metrics per report, so all workers are busy at once: exactly limit,
	// not fewer (the pool works in parallel) and not more (the limit holds)
	if got := maxInFlight.Load(); got != limit {
		t.Fatalf("expected at most and at least %d concurrent requests, got %d", limit, got)
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
