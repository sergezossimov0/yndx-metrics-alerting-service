package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWithLogging_DelegatesToInnerHandlerAndPreservesBody(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/value/gauge/missing", nil)

	WithLogging(inner).ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected recorder status %d, got %d", http.StatusNotFound, rec.Code)
	}
	if rec.Body.String() != "not found" {
		t.Fatalf("expected body %q, got %q", "not found", rec.Body.String())
	}
}

func TestLoggingResponseWriter_Write_DefaultsStatusToOKWhenUnset(t *testing.T) {
	rec := httptest.NewRecorder()
	rd := &responseData{}
	lw := loggingResponseWriter{ResponseWriter: rec, responseData: rd}

	if _, err := lw.Write([]byte("hello")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rd.status != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, rd.status)
	}
	if rd.size != len("hello") {
		t.Fatalf("expected size %d, got %d", len("hello"), rd.size)
	}
}

func TestLoggingResponseWriter_Write_DoesNotOverrideExplicitStatus(t *testing.T) {
	rec := httptest.NewRecorder()
	rd := &responseData{}
	lw := loggingResponseWriter{ResponseWriter: rec, responseData: rd}

	lw.WriteHeader(http.StatusBadRequest)
	if _, err := lw.Write([]byte("bad")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rd.status != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rd.status)
	}
}
