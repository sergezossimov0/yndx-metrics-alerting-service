package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/signature"
	"go.uber.org/zap"
)

// postWithBadSignature sends a valid metric with a signature that matches no
// key through the router built by newRouter.
func postWithBadSignature(t *testing.T, signKey []byte) *httptest.ResponseRecorder {
	t.Helper()
	s := newMemoryStore(memoryConfig(filepath.Join(t.TempDir(), "metrics.json"), 0, false), zap.NewNop())
	t.Cleanup(s.close)

	req := httptest.NewRequest(http.MethodPost, "/update/", strings.NewReader(`{"id":"Alloc","type":"gauge","value":1.5}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(signature.Header, "not-a-signature")
	res := httptest.NewRecorder()

	newRouter(s, signKey, zap.NewNop()).ServeHTTP(res, req)
	return res
}

// Without a key WithSignature is not in the chain: the signature is not
// checked and the response is not signed. start passes []byte(cfg.key),
// which is empty but not nil, so both forms are covered.
func TestNewRouter_WithoutKeySkipsSignature(t *testing.T) {
	for _, key := range [][]byte{nil, []byte("")} {
		res := postWithBadSignature(t, key)

		if res.Code != http.StatusOK {
			t.Fatalf("key %q: expected 200 without a key, got %d: %s", key, res.Code, res.Body)
		}
		if sig := res.Header().Get(signature.Header); sig != "" {
			t.Fatalf("key %q: expected no %s header without a key, got %q", key, signature.Header, sig)
		}
	}
}

// With a key WithSignature is in the chain and rejects a wrong signature.
func TestNewRouter_WithKeyChecksSignature(t *testing.T) {
	res := postWithBadSignature(t, []byte("secret"))

	if res.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a wrong signature, got %d: %s", res.Code, res.Body)
	}
}
