package handler

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCompressionHandler_PassesThroughRequestsRegardlessOfContentType(t *testing.T) {
	var reached bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusBadRequest)
	})

	req := httptest.NewRequest(http.MethodPost, "/update/", strings.NewReader(`{"id":"Alloc"}`))
	req.Header.Set("Content-Type", "text/plain")
	res := httptest.NewRecorder()

	CompressionHandler(inner).ServeHTTP(res, req)

	if !reached {
		t.Fatal("expected the request to reach the wrapped handler regardless of its Content-Type")
	}
	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected the wrapped handler's own status %d to be preserved, got %d", http.StatusBadRequest, res.Code)
	}
}

func TestCompressionHandler_PassesThroughRequestsWithoutContentType(t *testing.T) {
	var reached bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/update/", strings.NewReader(`{"id":"Alloc"}`))
	res := httptest.NewRecorder()

	CompressionHandler(inner).ServeHTTP(res, req)

	if !reached {
		t.Fatal("expected the request to reach the wrapped handler even without a Content-Type header")
	}
}

func TestIsCompressibleContentType(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		want        bool
	}{
		{name: "empty content type is allowed", contentType: "", want: true},
		{name: "application/json is allowed", contentType: "application/json", want: true},
		{name: "application/json with charset is allowed", contentType: "application/json; charset=utf-8", want: true},
		{name: "text/html is allowed", contentType: "text/html", want: true},
		{name: "text/plain is not allowed", contentType: "text/plain", want: false},
		{name: "application/x-www-form-urlencoded is not allowed", contentType: "application/x-www-form-urlencoded", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isCompressibleContentType(tt.contentType); got != tt.want {
				t.Fatalf("isCompressibleContentType(%q) = %v, want %v", tt.contentType, got, tt.want)
			}
		})
	}
}

func TestCompressionHandler_SkipsCompressionForUnsupportedContentTypeButStillCallsHandler(t *testing.T) {
	var reached bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	})

	req := httptest.NewRequest(http.MethodPost, "/update/", strings.NewReader(`{"id":"Alloc"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()

	CompressionHandler(inner).ServeHTTP(res, req)

	if !reached {
		t.Fatal("expected the request to still reach the wrapped handler")
	}
	if res.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected no Content-Encoding for an unsupported request Content-Type, got %q", res.Header().Get("Content-Encoding"))
	}
	if res.Body.String() != "hello" {
		t.Fatalf("expected plain, uncompressed body %q, got %q", "hello", res.Body.String())
	}
}

func TestCompressionHandler_CompressesResponseWhenAcceptEncodingGzip(t *testing.T) {
	body := strings.Repeat("a", minCompressibleResponseSize)
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()

	CompressionHandler(inner).ServeHTTP(res, req)

	if res.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding: gzip, got %q", res.Header().Get("Content-Encoding"))
	}
	if decodeGzipBody(t, res.Body.Bytes()) != body {
		t.Fatalf("decoded body does not match original")
	}
}

func TestCompressionHandler_DoesNotCompressResponseBelowSizeThreshold(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()

	CompressionHandler(inner).ServeHTTP(res, req)

	if res.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected no Content-Encoding for a body below the size threshold, got %q", res.Header().Get("Content-Encoding"))
	}
	if res.Body.String() != "hello" {
		t.Fatalf("expected plain, uncompressed body %q, got %q", "hello", res.Body.String())
	}
}

func TestCompressionHandler_DoesNotCompressResponseWithoutAcceptEncoding(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("hello"))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	res := httptest.NewRecorder()

	CompressionHandler(inner).ServeHTTP(res, req)

	if res.Header().Get("Content-Encoding") != "" {
		t.Fatalf("expected no Content-Encoding, got %q", res.Header().Get("Content-Encoding"))
	}
	if res.Body.String() != "hello" {
		t.Fatalf("expected plain body %q, got %q", "hello", res.Body.String())
	}
}

func TestCompressionHandler_DecompressesGzipRequestBody(t *testing.T) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if _, err := gz.Write([]byte(`{"id":"Alloc"}`)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var gotBody []byte
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var err error
		gotBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("unexpected error reading decompressed body: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/update/", &buf)
	req.Header.Set("Content-Encoding", "gzip")
	res := httptest.NewRecorder()

	CompressionHandler(inner).ServeHTTP(res, req)

	if string(gotBody) != `{"id":"Alloc"}` {
		t.Fatalf("expected decompressed body %q, got %q", `{"id":"Alloc"}`, string(gotBody))
	}
}
