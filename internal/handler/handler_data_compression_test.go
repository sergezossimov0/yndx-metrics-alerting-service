package handler

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
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

	CompressionHandler(zap.NewNop())(inner).ServeHTTP(res, req)

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

	CompressionHandler(zap.NewNop())(inner).ServeHTTP(res, req)

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

	CompressionHandler(zap.NewNop())(inner).ServeHTTP(res, req)

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

	CompressionHandler(zap.NewNop())(inner).ServeHTTP(res, req)

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

	CompressionHandler(zap.NewNop())(inner).ServeHTTP(res, req)

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

	CompressionHandler(zap.NewNop())(inner).ServeHTTP(res, req)

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

	CompressionHandler(zap.NewNop())(inner).ServeHTTP(res, req)

	if string(gotBody) != `{"id":"Alloc"}` {
		t.Fatalf("expected decompressed body %q, got %q", `{"id":"Alloc"}`, string(gotBody))
	}
}

// headerCountingRecorder counts WriteHeader calls: httptest.ResponseRecorder
// silently ignores a second call, while a real server logs
// "http: superfluous response.WriteHeader call".
type headerCountingRecorder struct {
	*httptest.ResponseRecorder
	writeHeaderCalls int
}

func (r *headerCountingRecorder) WriteHeader(code int) {
	r.writeHeaderCalls++
	r.ResponseRecorder.WriteHeader(code)
}

// A body declared as gzip but not valid gzip is the client's fault: the
// middleware must answer 400, not call the handler and not log anything.
func TestCompressionHandler_InvalidGzipRequestBodyReturnsBadRequest(t *testing.T) {
	tests := []struct {
		name           string
		acceptEncoding string
	}{
		{"plain response", ""},
		{"client also accepts gzip response", "gzip"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertInvalidGzipRejected(t, tt.acceptEncoding)
		})
	}
}

// assertInvalidGzipRejected sends a body declared as gzip but not valid gzip,
// with the given Accept-Encoding, and checks that the middleware answers 400
// exactly once, without calling the handler or logging.
func assertInvalidGzipRejected(t *testing.T, acceptEncoding string) {
	t.Helper()

	var reached bool
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodPost, "/update/", strings.NewReader("definitely not gzip"))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	res := &headerCountingRecorder{ResponseRecorder: httptest.NewRecorder()}
	log, logs := newObservedLogger()

	CompressionHandler(log)(inner).ServeHTTP(res, req)

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}
	if res.writeHeaderCalls != 1 {
		t.Fatalf("expected exactly one WriteHeader call, got %d", res.writeHeaderCalls)
	}
	if reached {
		t.Fatal("expected the handler not to be called for an invalid gzip body")
	}
	if logs.Len() != 0 {
		t.Fatalf("expected the client error not to be logged, got %v", logs.All())
	}
}
