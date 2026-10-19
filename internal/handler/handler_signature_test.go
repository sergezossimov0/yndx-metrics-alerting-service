package handler

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/signature"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

const signedBody = `[{"id":"Alloc","type":"gauge","value":1.5}]`

var signKey = []byte("secret")

// recordingHandler answers 200 and records whether it was reached and the body it read.
type recordingHandler struct {
	reached bool
	body    string
}

func (h *recordingHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.reached = true
	b, _ := io.ReadAll(r.Body)
	h.body = string(b)
	w.WriteHeader(http.StatusOK)
}

func signedRequest(body, sig string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/updates/", strings.NewReader(body))
	req.Header.Set(headerContentType, contentTypeJSON)
	if sig != "" {
		req.Header.Set(signature.Header, sig)
	}
	return req
}

func TestWithSignature_PassesValidSignatureWithBodyIntact(t *testing.T) {
	inner := &recordingHandler{}
	req := signedRequest(signedBody, signature.Sum([]byte(signedBody), signKey))
	res := httptest.NewRecorder()

	WithSignature(signKey, zap.NewNop())(inner).ServeHTTP(res, req)

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
	if !inner.reached {
		t.Fatal("expected a correctly signed request to reach the handler")
	}
	if inner.body != signedBody {
		t.Fatalf("expected the handler to read the whole body %q, got %q", signedBody, inner.body)
	}
}

func TestWithSignature_RejectsInvalidSignature(t *testing.T) {
	tests := []struct {
		name string
		sig  string
	}{
		{name: "other key", sig: signature.Sum([]byte(signedBody), []byte("other"))},
		{name: "other body", sig: signature.Sum([]byte(`[]`), signKey)},
		{name: "not hex", sig: "not-a-signature"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &recordingHandler{}
			core, logs := observer.New(zapcore.DebugLevel)
			res := httptest.NewRecorder()

			WithSignature(signKey, zap.New(core))(inner).ServeHTTP(res, signedRequest(signedBody, tt.sig))

			if res.Code != http.StatusBadRequest {
				t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
			}
			if inner.reached {
				t.Fatal("expected a request with a wrong signature not to reach the handler")
			}
			requireOneLogEntryWithout(t, logs, "request signature mismatch", tt.sig)
		})
	}
}

// requireOneLogEntryWithout fails unless logs hold exactly one entry with msg
// and none of its fields carries value.
func requireOneLogEntryWithout(t *testing.T, logs *observer.ObservedLogs, msg, value string) {
	t.Helper()
	entries := logs.FilterMessage(msg).All()
	if len(entries) != 1 {
		t.Fatalf("expected one %q log entry, got %d: %v", msg, len(entries), logs.All())
	}
	for _, f := range entries[0].Context {
		if f.String == value {
			t.Fatalf("expected %q not to be logged, found it in field %q", value, f.Key)
		}
	}
}

func TestWithSignature_PassesRequestWithoutSignatureUnchecked(t *testing.T) {
	inner := &recordingHandler{}
	res := httptest.NewRecorder()

	WithSignature(signKey, zap.NewNop())(inner).ServeHTTP(res, signedRequest(signedBody, ""))

	if !inner.reached {
		t.Fatal("expected a request without a signature to reach the handler")
	}
	if inner.body != signedBody {
		t.Fatalf("expected the handler to read the whole body %q, got %q", signedBody, inner.body)
	}
}

func TestWithSignature_EmptyKeyDisablesCheck(t *testing.T) {
	for _, key := range [][]byte{nil, []byte("")} {
		inner := &recordingHandler{}
		res := httptest.NewRecorder()

		WithSignature(key, zap.NewNop())(inner).ServeHTTP(res, signedRequest(signedBody, "not-a-signature"))

		if !inner.reached {
			t.Fatalf("expected key %q to disable the check and pass the request through", key)
		}
	}
}

func TestWithSignature_EmptyKeyWarnsOnceAboutSignedRequests(t *testing.T) {
	inner := &recordingHandler{}
	core, logs := observer.New(zapcore.DebugLevel)
	mw := WithSignature(nil, zap.New(core))(inner)

	// an unsigned request is the normal case without a key: no warning
	mw.ServeHTTP(httptest.NewRecorder(), signedRequest(signedBody, ""))
	if n := logs.Len(); n != 0 {
		t.Fatalf("expected no log entries for an unsigned request, got %d: %v", n, logs.All())
	}

	for range 3 {
		inner.reached = false
		mw.ServeHTTP(httptest.NewRecorder(), signedRequest(signedBody, signature.Sum([]byte(signedBody), signKey)))
		if !inner.reached {
			t.Fatal("expected a signed request to reach the handler when the server has no key")
		}
	}

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("expected exactly one warning for three signed requests, got %d: %v", len(entries), entries)
	}
	if entries[0].Level != zapcore.WarnLevel {
		t.Fatalf("expected a warning, got level %s", entries[0].Level)
	}
}

// The agent signs the uncompressed JSON, so behind CompressionHandler the
// signature must match the decompressed body, not the gzip stream.
func TestWithSignature_ChecksDecompressedBodyBehindCompressionHandler(t *testing.T) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write([]byte(signedBody)); err != nil {
		t.Fatalf("unexpected error compressing: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("unexpected error compressing: %v", err)
	}
	compressed := gz.Bytes()

	tests := []struct {
		name       string
		sig        string
		wantStatus int
	}{
		{name: "signature of uncompressed body", sig: signature.Sum([]byte(signedBody), signKey), wantStatus: http.StatusOK},
		{name: "signature of gzip stream", sig: signature.Sum(compressed, signKey), wantStatus: http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			inner := &recordingHandler{}
			req := signedRequest(string(compressed), tt.sig)
			req.Header.Set("Content-Encoding", "gzip")
			res := httptest.NewRecorder()

			chain := CompressionHandler(zap.NewNop())(WithSignature(signKey, zap.NewNop())(inner))
			chain.ServeHTTP(res, req)

			if res.Code != tt.wantStatus {
				t.Fatalf("expected status %d, got %d", tt.wantStatus, res.Code)
			}
			if tt.wantStatus == http.StatusOK && inner.body != signedBody {
				t.Fatalf("expected the handler to read the decompressed body %q, got %q", signedBody, inner.body)
			}
		})
	}
}

// requireSignedResponse fails unless res carries a HashSHA256 header that signs its body.
func requireSignedResponse(t *testing.T, res *httptest.ResponseRecorder) {
	t.Helper()
	sig := res.Header().Get(signature.Header)
	if sig == "" {
		t.Fatalf("expected the response to carry the %s header", signature.Header)
	}
	if !signature.Verify(res.Body.Bytes(), signKey, sig) {
		t.Fatalf("expected %s to sign the response body %q, got %s", signature.Header, res.Body.String(), sig)
	}
}

func TestWithSignature_SignsResponseKeepingStatusHeadersAndBody(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":`))
		_, _ = w.Write([]byte(`"Alloc"}`))
	})
	res := httptest.NewRecorder()

	WithSignature(signKey, zap.NewNop())(inner).ServeHTTP(res, signedRequest(signedBody, ""))

	if res.Code != http.StatusCreated {
		t.Fatalf("expected the handler's status %d, got %d", http.StatusCreated, res.Code)
	}
	if got := res.Header().Get(headerContentType); got != contentTypeJSON {
		t.Fatalf("expected the handler's Content-Type %q, got %q", contentTypeJSON, got)
	}
	if got := res.Body.String(); got != `{"id":"Alloc"}` {
		t.Fatalf("expected both writes in the body, got %q", got)
	}
	requireSignedResponse(t, res)
}

func TestWithSignature_SignsResponseWrittenWithoutWriteHeader(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.5"))
	})
	res := httptest.NewRecorder()

	WithSignature(signKey, zap.NewNop())(inner).ServeHTTP(res, signedRequest(signedBody, ""))

	if res.Code != http.StatusOK {
		t.Fatalf("expected an implicit %d, got %d", http.StatusOK, res.Code)
	}
	requireSignedResponse(t, res)
}

func TestWithSignature_SignsEmptyResponse(t *testing.T) {
	// recordingHandler answers 200 without a body, like the update handlers
	res := httptest.NewRecorder()

	WithSignature(signKey, zap.NewNop())(&recordingHandler{}).ServeHTTP(res, signedRequest(signedBody, ""))

	if res.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d", http.StatusOK, res.Code)
	}
	if res.Body.Len() != 0 {
		t.Fatalf("expected an empty body, got %q", res.Body.String())
	}
	requireSignedResponse(t, res)
}

func TestWithSignature_SignsRejection(t *testing.T) {
	inner := &recordingHandler{}
	res := httptest.NewRecorder()

	WithSignature(signKey, zap.NewNop())(inner).ServeHTTP(res, signedRequest(signedBody, "not-a-signature"))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d", http.StatusBadRequest, res.Code)
	}
	requireSignedResponse(t, res)
}

func TestWithSignature_EmptyKeyLeavesResponseUnsigned(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("1.5"))
	})
	res := httptest.NewRecorder()

	WithSignature(nil, zap.NewNop())(inner).ServeHTTP(res, signedRequest(signedBody, ""))

	if sig := res.Header().Get(signature.Header); sig != "" {
		t.Fatalf("expected no %s header without a key, got %q", signature.Header, sig)
	}
}

// Behind CompressionHandler the response is signed before it is compressed,
// so the signature matches the body the client gets after decompressing.
func TestWithSignature_SignsUncompressedResponseBehindCompressionHandler(t *testing.T) {
	const respBody = `{"id":"Alloc","type":"gauge","value":1.5}`
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(headerContentType, contentTypeJSON)
		_, _ = w.Write([]byte(respBody))
	})
	req := signedRequest(signedBody, "")
	req.Header.Set("Accept-Encoding", "gzip")
	res := httptest.NewRecorder()

	CompressionHandler(zap.NewNop())(WithSignature(signKey, zap.NewNop())(inner)).ServeHTTP(res, req)

	if got := res.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("expected a gzip-compressed response, got Content-Encoding %q", got)
	}
	zr, err := gzip.NewReader(res.Body)
	if err != nil {
		t.Fatalf("expected a valid gzip stream, got error: %v", err)
	}
	defer zr.Close()
	body, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("unexpected error decompressing: %v", err)
	}

	if string(body) != respBody {
		t.Fatalf("expected body %q, got %q", respBody, body)
	}
	if !signature.Verify(body, signKey, res.Header().Get(signature.Header)) {
		t.Fatalf("expected %s to sign the decompressed body", signature.Header)
	}
}
