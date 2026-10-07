package agent

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// testRetryDelays keep the shape of retry.DefaultDelays (three retries) but
// in milliseconds, so tests that hit failures stay fast.
var testRetryDelays = []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}

// flakyServer fails the first `failures` requests with fail and then answers
// 200. It records the size of every request body it receives.
func flakyServer(t *testing.T, failures int, fail func(w http.ResponseWriter)) (*httptest.Server, func() []int) {
	t.Helper()
	var (
		mu     sync.Mutex
		bodies []int
	)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var batch []models.Metrics
		_ = decodeGzipJSONBody(r, &batch)

		mu.Lock()
		bodies = append(bodies, len(batch))
		n := len(bodies)
		mu.Unlock()

		if n <= failures {
			fail(w)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)
	return ts, func() []int {
		mu.Lock()
		defer mu.Unlock()
		return append([]int(nil), bodies...)
	}
}

// dropConnection closes the connection without a response, so the client
// sees a transport error, as when the server goes away mid-request.
func dropConnection(w http.ResponseWriter) {
	conn, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		_ = conn.Close()
	}
}

func respondWith(code int) func(w http.ResponseWriter) {
	return func(w http.ResponseWriter) { w.WriteHeader(code) }
}

func newRetryTestAgent(serverURL string, store metricStore) *Agent {
	a := NewAgent(serverURL, 2*time.Second, 10*time.Second, store, zap.NewNop())
	a.retry.Delays = testRetryDelays
	return a
}

func TestNewAgent_UsesDefaultRetryDelays(t *testing.T) {
	a := NewAgent("http://localhost:8080", time.Second, time.Second, repository.NewMemStorage(), zap.NewNop())

	want := []time.Duration{time.Second, 3 * time.Second, 5 * time.Second}
	if len(a.retry.Delays) != len(want) {
		t.Fatalf("expected delays %v, got %v", want, a.retry.Delays)
	}
	for i := range want {
		if a.retry.Delays[i] != want[i] {
			t.Fatalf("expected delays %v, got %v", want, a.retry.Delays)
		}
	}
}

func TestSendBatchJSON_RetriesAfterDroppedConnectionWithFullBody(t *testing.T) {
	ts, bodies := flakyServer(t, 2, dropConnection)
	a := newRetryTestAgent(ts.URL, repository.NewMemStorage())

	batch := []models.Metrics{gaugeMetric("Alloc", 1), counterMetric("PollCount", 2)}
	if err := a.sendBatchJSON(context.Background(), batch); err != nil {
		t.Fatalf("expected the third attempt to succeed, got %v", err)
	}

	got := bodies()
	if len(got) != 3 {
		t.Fatalf("expected 3 attempts, got %d", len(got))
	}
	for i, n := range got {
		if n != 2 {
			t.Fatalf("expected every attempt to carry the whole batch, attempt %d had %d metrics", i+1, n)
		}
	}
}

func TestSendBatchJSON_RetriesOnServiceUnavailable(t *testing.T) {
	for _, code := range []int{http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			ts, bodies := flakyServer(t, 1, respondWith(code))
			a := newRetryTestAgent(ts.URL, repository.NewMemStorage())

			if err := a.sendBatchJSON(context.Background(), []models.Metrics{gaugeMetric("Alloc", 1)}); err != nil {
				t.Fatalf("expected the retry to succeed, got %v", err)
			}
			if n := len(bodies()); n != 2 {
				t.Fatalf("expected 2 attempts, got %d", n)
			}
		})
	}
}

func TestSendBatchJSON_DoesNotRetryClientOrServerErrors(t *testing.T) {
	for _, code := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusInternalServerError} {
		t.Run(http.StatusText(code), func(t *testing.T) {
			ts, bodies := flakyServer(t, 10, respondWith(code))
			a := newRetryTestAgent(ts.URL, repository.NewMemStorage())

			if err := a.sendBatchJSON(context.Background(), []models.Metrics{gaugeMetric("Alloc", 1)}); err == nil {
				t.Fatal("expected an error")
			}
			if n := len(bodies()); n != 1 {
				t.Fatalf("expected a single attempt, got %d", n)
			}
		})
	}
}

func TestSendBatchJSON_GivesUpAfterThreeRetries(t *testing.T) {
	ts, bodies := flakyServer(t, 10, respondWith(http.StatusServiceUnavailable))
	a := newRetryTestAgent(ts.URL, repository.NewMemStorage())

	err := a.sendBatchJSON(context.Background(), []models.Metrics{gaugeMetric("Alloc", 1)})

	if statusErr, ok := errors.AsType[*statusError](err); !ok || statusErr.code != http.StatusServiceUnavailable {
		t.Fatalf("expected the last 503 to be reported, got %v", err)
	}
	if n := len(bodies()); n != 4 {
		t.Fatalf("expected 1 attempt + 3 retries = 4 requests, got %d", n)
	}
}

func TestReportOnceJSON_SubtractsCountersOnceAfterRetriedSuccess(t *testing.T) {
	ts, _ := flakyServer(t, 2, dropConnection)
	store := repository.NewMemStorage()
	a := newRetryTestAgent(ts.URL, store)

	a.collectOnce(context.Background())
	a.collectOnce(context.Background())
	a.reportOnceJSON(context.Background())

	if got, _ := store.GetCounter(context.Background(), "PollCount"); got != 0 {
		t.Fatalf("expected PollCount delta to be subtracted exactly once, got %d", got)
	}
}

func TestReportOnceJSON_ShutdownDuringRetryPauseIsNotAnError(t *testing.T) {
	ts, _ := flakyServer(t, 10, respondWith(http.StatusServiceUnavailable))
	core, logs := newObservedLogger()
	a := NewAgent(ts.URL, 2*time.Second, 10*time.Second, &metricStoreMock{
		metrics: []models.Metrics{gaugeMetric("Alloc", 1)},
	}, core)
	a.retry.Delays = []time.Duration{time.Minute}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	a.reportOnceJSON(ctx)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("expected the retry pause to be cut short by shutdown, it took %v", elapsed)
	}
	if n := logs.FilterLevelExact(zapcore.ErrorLevel).Len(); n != 0 {
		t.Fatalf("expected no error-level logs on shutdown, got %d: %v", n, logs.All())
	}
}

func TestIsRetriableSendError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "transport error", err: &url.Error{Op: "Post", URL: "http://x", Err: errors.New("connection refused")}, want: true},
		{name: "502", err: &statusError{code: http.StatusBadGateway}, want: true},
		{name: "503", err: &statusError{code: http.StatusServiceUnavailable}, want: true},
		{name: "504", err: &statusError{code: http.StatusGatewayTimeout}, want: true},
		{name: "400", err: &statusError{code: http.StatusBadRequest}, want: false},
		{name: "500", err: &statusError{code: http.StatusInternalServerError}, want: false},
		{name: "cancelled", err: &url.Error{Op: "Post", URL: "http://x", Err: context.Canceled}, want: false},
		{name: "other", err: errors.New("marshal failed"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRetriableSendError(tt.err); got != tt.want {
				t.Fatalf("isRetriableSendError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
