package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"go.uber.org/zap/zapcore"
)

func TestWithLogging_DelegatesToInnerHandlerAndPreservesBody(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/value/gauge/missing", nil)

	log, _ := newObservedLogger()
	WithLogging(log)(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected recorder status %d, got %d", http.StatusNotFound, rec.Code)
	}
	if rec.Body.String() != "not found" {
		t.Fatalf("expected body %q, got %q", "not found", rec.Body.String())
	}
}

func TestWithLogging_WritesAccessLogEntryToInjectedLogger(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/value/gauge/missing", nil)

	log, logs := newObservedLogger()
	WithLogging(log)(inner).ServeHTTP(rec, req)

	entries := logs.FilterMessage("HTTP request").All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one access log entry, got %d: %v", len(entries), logs.All())
	}
	entry := entries[0]
	if entry.Level != zapcore.InfoLevel {
		t.Fatalf("expected info level, got %v", entry.Level)
	}
	fields := entry.ContextMap()
	if fields["uri"] != "/value/gauge/missing" || fields["method"] != http.MethodGet {
		t.Fatalf("expected uri and method of the request, got %v", fields)
	}
	if fields["status"] != int64(http.StatusNotFound) || fields["size"] != int64(len("not found")) {
		t.Fatalf("expected status=%d size=%d, got %v", http.StatusNotFound, len("not found"), fields)
	}
}
