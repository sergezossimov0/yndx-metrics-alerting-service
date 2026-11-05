package handler

import (
	"bytes"
	"io"
	"net/http"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/signature"
	"go.uber.org/zap"
)

// WithSignature returns a middleware that checks the HashSHA256 signature of
// request bodies and signs every response body. key must not be empty: the
// caller decides whether signing is on and adds the middleware only when a
// key is set (see newRouter in cmd/server).
//
// It must run after CompressionHandler: the agent signs the uncompressed body,
// so the signature is checked against the decompressed one, and the response
// is signed before it is compressed.
//
// A request without the header is passed through unchecked: there is nothing
// to compare, and GET / or GET /ping from a browser carry no signature.
// A request with a wrong signature is rejected with 400 before it reaches the
// handler, so its data is never stored.
func WithSignature(key []byte, log *zap.Logger) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &signingWriter{ResponseWriter: w}
			serveVerified(h, key, log, sw, r)
			if err := sw.send(key); err != nil {
				log.Warn("write response error", zap.String("uri", r.RequestURI), zap.Error(err))
			}
		})
	}
}

// serveVerified passes r to h if its signature is valid or absent,
// and answers 400 otherwise.
func serveVerified(h http.Handler, key []byte, log *zap.Logger, w http.ResponseWriter, r *http.Request) {
	sig := r.Header.Get(signature.Header)
	if sig == "" {
		h.ServeHTTP(w, r)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Warn("read request body error", zap.String("uri", r.RequestURI), zap.Error(err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	if !signature.Verify(body, key, sig) {
		// the signature itself is not logged: it is of no use for debugging
		// without the key, and the log is not the place for it
		log.Warn("request signature mismatch", zap.String("uri", r.RequestURI))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// the body has been read; give the handler a fresh reader over the same bytes
	r.Body = io.NopCloser(bytes.NewReader(body))
	h.ServeHTTP(w, r)
}

// signingWriter holds the response back until the handler has finished:
// the signature header has to go out before the body, but it is known only
// once the whole body has been written. Responses here are small (a metric
// in JSON or the HTML list), so buffering them costs nothing.
type signingWriter struct {
	http.ResponseWriter
	status int
	body   bytes.Buffer
}

// WriteHeader records the status; as with net/http, only the first call counts.
func (s *signingWriter) WriteHeader(status int) {
	if s.status == 0 {
		s.status = status
	}
}

func (s *signingWriter) Write(p []byte) (int, error) {
	if s.status == 0 {
		// Write без предварительного WriteHeader неявно отправляет 200
		s.status = http.StatusOK
	}
	return s.body.Write(p)
}

// send signs the buffered body and writes the status, the headers and the
// body to the wrapped writer.
func (s *signingWriter) send(key []byte) error {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	s.Header().Set(signature.Header, signature.Sum(s.body.Bytes(), key))
	s.ResponseWriter.WriteHeader(s.status)

	if s.body.Len() == 0 {
		return nil
	}
	_, err := s.ResponseWriter.Write(s.body.Bytes())
	return err
}
