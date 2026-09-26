package handler

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// newObservedLogger returns a logger that keeps entries in memory, so a test
// can inspect what a component logged without touching any global state.
func newObservedLogger() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

type clientErrorCase struct {
	name        string
	method      string
	target      string
	contentType string
	body        string
	wantStatus  int
}

// Client errors (4xx) are the client's fault, not the server's: handlers must
// not log them. The access log (WithLogging) still records every request with
// its status, so nothing is lost.
func TestHandlers_ClientErrorsAreNotLogged(t *testing.T) {
	tests := []clientErrorCase{
		{"update JSON: malformed body", http.MethodPost, "/update/", "application/json", "not-json", http.StatusBadRequest},
		{"update JSON: invalid content type", http.MethodPost, "/update/", "text/plain", `{"id":"Alloc"}`, http.StatusBadRequest},
		{"update JSON: invalid metric", http.MethodPost, "/update/", "application/json", `{"id":"Alloc","type":"unknown"}`, http.StatusBadRequest},
		{"value JSON: malformed body", http.MethodPost, "/value/", "application/json", "not-json", http.StatusBadRequest},
		{"value JSON: invalid content type", http.MethodPost, "/value/", "text/plain", `{"id":"Alloc"}`, http.StatusBadRequest},
		{"value JSON: unknown metric", http.MethodPost, "/value/", "application/json", `{"id":"Missing","type":"gauge"}`, http.StatusNotFound},
		{"update URL: invalid value", http.MethodPost, "/update/gauge/Alloc/abc", "text/plain", "", http.StatusBadRequest},
		{"value URL: unknown metric", http.MethodGet, "/value/gauge/Missing", "", "", http.StatusNotFound},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertClientErrorNotLogged(t, tt)
		})
	}
}

// assertClientErrorNotLogged sends the request of tc and checks the status
// and that the handlers logged nothing.
func assertClientErrorNotLogged(t *testing.T, tc clientErrorCase) {
	t.Helper()

	log, logs := newObservedLogger()
	h, _ := newTestHandlerWithLogger(log)
	req := httptest.NewRequest(tc.method, tc.target, strings.NewReader(tc.body))
	if tc.contentType != "" {
		req.Header.Set("Content-Type", tc.contentType)
	}
	res := httptest.NewRecorder()

	h.ServeHTTP(res, req)

	if res.Code != tc.wantStatus {
		t.Fatalf("expected status %d, got %d", tc.wantStatus, res.Code)
	}
	if logs.Len() != 0 {
		t.Fatalf("expected client error not to be logged, got %v", logs.All())
	}
}

type serverErrorCase struct {
	name        string
	route       string
	handler     func(MetricUpdater, *zap.Logger) http.HandlerFunc
	target      string
	contentType string
	body        string
}

// serverErrorCause is the internal error the failing updater returns.
const serverErrorCause = "store unavailable"

// Server errors (5xx) are the opposite case: the client only gets a generic
// message, so the cause must be logged, or nobody will ever see it.
func TestUpdateHandlers_ServerErrorIsLoggedAndNotLeakedToClient(t *testing.T) {
	tests := []serverErrorCase{
		{"update JSON", "/update/", UpdateMetricsJSONHandler, "/update/", "application/json", `{"id":"Alloc","type":"gauge","value":1.5}`},
		{"update URL", "/update/{type}/{name}/{value}", UpdateMetricsHandler, "/update/gauge/Alloc/1.5", "text/plain", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertServerErrorLoggedAndNotLeaked(t, tt)
		})
	}
}

// assertServerErrorLoggedAndNotLeaked sends the request of tc to a handler
// whose updater fails, and checks the status, the log entry and the response
// body.
func assertServerErrorLoggedAndNotLeaked(t *testing.T, tc serverErrorCase) {
	t.Helper()

	log, logs := newObservedLogger()
	r := chi.NewRouter()
	r.Post(tc.route, tc.handler(&metricUpdaterMock{err: errors.New(serverErrorCause)}, log))

	req := httptest.NewRequest(http.MethodPost, tc.target, strings.NewReader(tc.body))
	req.Header.Set("Content-Type", tc.contentType)
	res := httptest.NewRecorder()

	r.ServeHTTP(res, req)

	if res.Code != http.StatusInternalServerError {
		t.Fatalf("expected status %d, got %d", http.StatusInternalServerError, res.Code)
	}
	assertSingleUpdateErrorEntry(t, logs)
	if strings.Contains(res.Body.String(), serverErrorCause) {
		t.Fatalf("expected the internal cause not to leak to the client, got body %q", res.Body.String())
	}
}

// assertSingleUpdateErrorEntry checks that exactly one error-level entry was
// logged, with the metric, its type and the internal cause.
func assertSingleUpdateErrorEntry(t *testing.T, logs *observer.ObservedLogs) {
	t.Helper()

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one log entry, got %d: %v", len(entries), entries)
	}
	entry := entries[0]
	if entry.Level != zapcore.ErrorLevel || entry.Message != msgFailedUpdateMetric {
		t.Fatalf("expected error-level %q entry, got level=%v msg=%q", msgFailedUpdateMetric, entry.Level, entry.Message)
	}
	fields := entry.ContextMap()
	if fields["metric"] != "Alloc" || fields["type"] != "gauge" || fields["error"] != serverErrorCause {
		t.Fatalf("expected metric=Alloc type=gauge error=%q, got %v", serverErrorCause, fields)
	}
}

func TestJSONHandlers_MalformedBodyReturnsMessageToClient(t *testing.T) {
	// the decode error is no longer logged, so the client gets the reason instead
	for _, target := range []string{"/update/", "/value/"} {
		t.Run(target, func(t *testing.T) {
			h, _ := newTestHandler()

			res := postJSONRequest(h, target, "application/json", []byte("not-json"))

			if res.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
			}
			if got := strings.TrimSpace(res.Body.String()); got != msgInvalidJSONBody {
				t.Fatalf("expected body %q, got %q", msgInvalidJSONBody, got)
			}
		})
	}
}
