package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
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

func (s *pollCountingStore) Update(ctx context.Context, metric *models.Metrics) error {
	if metric.ID == "PollCount" {
		s.mu.Lock()
		s.polls = append(s.polls, time.Now())
		s.mu.Unlock()
	}
	return s.MemStorage.Update(ctx, metric)
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

// Run with -race: polls and reports now run concurrently on the same store.
func TestAgent_Run_KeepsPollingWhileReportRetries(t *testing.T) {
	var (
		mu           sync.Mutex
		requestTimes []time.Time
		received     int64 // PollCount deltas the server accepted
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []models.Metrics
		_ = decodeGzipJSONBody(r, &batch)

		mu.Lock()
		defer mu.Unlock()
		requestTimes = append(requestTimes, time.Now())
		if len(requestTimes) == 1 {
			// the first report fails and has to wait for its retry
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		for _, m := range batch {
			if m.ID == "PollCount" && m.Delta != nil {
				received += *m.Delta
			}
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
	if len(requestTimes) < 2 {
		t.Fatalf("expected the failed report to be retried, got %d requests", len(requestTimes))
	}

	// With polling and reporting in one goroutine at most one poll could
	// happen during the retry pause; with separate goroutines it is ~40.
	if n := store.pollsBetween(requestTimes[0], requestTimes[1]); n < 10 {
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
		var batch []models.Metrics
		_ = decodeGzipJSONBody(r, &batch)
		mu.Lock()
		for _, m := range batch {
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
