package handler

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/compress"
)

func TestCompressWriter_SmallBodyIsNotCompressed(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)

	if _, err := cw.Write([]byte("hello")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected implicit status %d, got %d", http.StatusOK, rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected no Content-Encoding for a body below the threshold, got %q", rec.Header().Get("Content-Encoding"))
	}
	if rec.Body.String() != "hello" {
		t.Fatalf("expected plain, uncompressed body %q, got %q", "hello", rec.Body.String())
	}
}

func TestCompressWriter_LargeBodyIsCompressed(t *testing.T) {
	body := strings.Repeat("a", minCompressibleResponseSize)

	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)

	if _, err := cw.Write([]byte(body)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding: gzip for a body at/above the threshold, got %q", rec.Header().Get("Content-Encoding"))
	}
	if decodeGzipBody(t, rec.Body.Bytes()) != body {
		t.Fatalf("decoded body does not match original")
	}
}

func TestCompressWriter_LargeBodyWrittenInSmallChunksIsStillCompressed(t *testing.T) {
	chunk := strings.Repeat("b", 100)
	var want strings.Builder

	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)

	// 11 chunks of 100 bytes = 1100 bytes, crossing the 1024-byte threshold partway through.
	for i := 0; i < 11; i++ {
		if _, err := cw.Write([]byte(chunk)); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want.WriteString(chunk)
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding: gzip once the cumulative size crosses the threshold, got %q", rec.Header().Get("Content-Encoding"))
	}
	if decodeGzipBody(t, rec.Body.Bytes()) != want.String() {
		t.Fatalf("decoded body does not match the concatenation of all written chunks")
	}
}

func TestCompressWriter_LargeErrorBodyIsCompressedWithCorrectStatus(t *testing.T) {
	body := strings.Repeat("e", minCompressibleResponseSize)

	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)

	cw.WriteHeader(http.StatusBadRequest)
	if _, err := cw.Write([]byte(body)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding: gzip even for an error status, got %q", rec.Header().Get("Content-Encoding"))
	}
	if decodeGzipBody(t, rec.Body.Bytes()) != body {
		t.Fatalf("decoded body does not match original")
	}
}

func TestCompressWriter_SmallErrorBodyIsNotCompressed(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)

	cw.WriteHeader(http.StatusNotFound)
	if _, err := cw.Write([]byte("not found")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected no Content-Encoding for a body below the threshold, got %q", rec.Header().Get("Content-Encoding"))
	}
	if rec.Body.String() != "not found" {
		t.Fatalf("expected plain, uncompressed body %q, got %q", "not found", rec.Body.String())
	}
}

func TestCompressWriter_WriteHeader_CalledOnceEvenWithSubsequentWrite(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)

	cw.WriteHeader(http.StatusNotFound)
	cw.WriteHeader(http.StatusInternalServerError) // must be ignored
	if _, err := cw.Write([]byte("not found")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	_ = cw.Close()

	if rec.Code != http.StatusNotFound {
		t.Fatalf("expected the first WriteHeader status %d to stick, got %d", http.StatusNotFound, rec.Code)
	}
}

func TestCompressWriter_EmptyBodyClosesCleanly(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)

	if err := cw.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("expected implicit status %d, got %d", http.StatusOK, rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected no Content-Encoding for an empty body, got %q", rec.Header().Get("Content-Encoding"))
	}
}

func TestCompressWriter_SecondCloseOfCompressedBodyIsNoop(t *testing.T) {
	body := strings.Repeat("a", minCompressibleResponseSize)

	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)

	if _, err := cw.Write([]byte(body)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("unexpected error on first Close: %v", err)
	}
	sizeAfterFirstClose := rec.Body.Len()

	if err := cw.Close(); err != nil {
		t.Fatalf("expected second Close to be a no-op, got error: %v", err)
	}

	if cw.zw != nil {
		t.Fatal("expected the gzip writer to be released after Close")
	}
	if rec.Body.Len() != sizeAfterFirstClose {
		t.Fatalf("expected second Close to write nothing, body grew from %d to %d bytes", sizeAfterFirstClose, rec.Body.Len())
	}
	if decodeGzipBody(t, rec.Body.Bytes()) != body {
		t.Fatalf("decoded body does not match original")
	}

	// A writer put twice would be returned by two consecutive Gets. This check
	// cannot catch every double Put (sync.Pool gives no ordering guarantees),
	// but it never fails when the writer was put exactly once.
	a := compress.GetWriter(io.Discard)
	b := compress.GetWriter(io.Discard)
	defer compress.PutWriter(a)
	defer compress.PutWriter(b)
	if a == b {
		t.Fatal("the same gzip writer was handed out twice: it was put into the pool twice")
	}
}

func TestCompressWriter_SecondCloseOfPlainBodyDoesNotRepeatIt(t *testing.T) {
	rec := httptest.NewRecorder()
	cw := newCompressWriter(rec)

	if _, err := cw.Write([]byte("hello")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("unexpected error on first Close: %v", err)
	}
	if err := cw.Close(); err != nil {
		t.Fatalf("expected second Close to be a no-op, got error: %v", err)
	}

	if rec.Body.String() != "hello" {
		t.Fatalf("expected body %q to be sent once, got %q", "hello", rec.Body.String())
	}
}

func decodeGzipBody(t *testing.T, raw []byte) string {
	t.Helper()

	gz, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	decoded, err := io.ReadAll(gz)
	if err != nil {
		t.Fatalf("failed to decode gzip body: %v", err)
	}
	return string(decoded)
}
