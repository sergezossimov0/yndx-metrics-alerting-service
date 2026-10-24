package agent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/mailru/easyjson"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/compress"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/signature"
	"go.uber.org/zap"
)

// statusError is a response with a status other than 200 OK.
type statusError struct {
	code int
}

func (e *statusError) Error() string {
	return fmt.Sprintf("unexpected status %d", e.code)
}

// isRetriableSendError reports whether a failed request is worth repeating:
// a transport error (the server is unreachable, the connection was reset or
// timed out) or a status saying the server is temporarily unavailable.
// 400 is not retried: the same body would be rejected again. Neither is 500:
// the server has already retried its own storage before answering.
//
// Context errors are deliberately not filtered out here. Since Go 1.23 a
// http.Client timeout matches context.DeadlineExceeded too, so such a filter
// would also stop retries of a hung server. A cancelled agent (shutdown) is
// handled by retry.Policy.Do, which checks its own ctx before retrying.
func isRetriableSendError(err error) bool {
	if statusErr, ok := errors.AsType[*statusError](err); ok {
		switch statusErr.code {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return true
		}
		return false
	}

	// http.Client.Do reports every transport failure as *url.Error.
	_, ok := errors.AsType[*url.Error](err)
	return ok
}

// sendMetricJSON posts a single metric to POST /update/.
func (a *Agent) sendMetricJSON(ctx context.Context, body *models.Metrics) error {
	if err := a.postGzipJSON(ctx, "/update/", body); err != nil {
		return fmt.Errorf("%s %s: %w", body.MType, body.ID, err)
	}
	return nil
}

// sendBatchJSON posts all metrics in a single request to POST /updates/.
func (a *Agent) sendBatchJSON(ctx context.Context, batch []models.Metrics) error {
	if err := a.postGzipJSON(ctx, "/updates/", models.MetricsList(batch)); err != nil {
		return fmt.Errorf("batch of %d metrics: %w", len(batch), err)
	}
	return nil
}

// postGzipJSON marshals body, compresses it with gzip and posts it to path on
// the server. Any status other than 200 OK is an error. Temporary failures
// are retried according to a.retry; the body is compressed once and a new
// request is built for every attempt, since a sent request body is consumed.
//
// With a sign key the uncompressed JSON is signed, not the gzip stream: the
// server checks the signature after decompressing, so it does not depend on
// how the body was compressed.
func (a *Agent) postGzipJSON(ctx context.Context, path string, body easyjson.Marshaler) error {
	reqURL := strings.TrimRight(a.serverAddr, "/") + path

	reqBody, err := easyjson.Marshal(body)
	if err != nil {
		return err
	}

	var sig string
	if len(a.signKey) > 0 {
		sig = signature.Sum(reqBody, a.signKey)
	}

	compressedBody, err := compress.Compress(reqBody)
	if err != nil {
		return err
	}

	return a.retry.Do(ctx, func(ctx context.Context) error {
		return a.post(ctx, reqURL, compressedBody, sig)
	})
}

// post makes a single attempt to send an already compressed JSON body.
// An empty sig sends the request without the HashSHA256 header.
func (a *Agent) post(ctx context.Context, reqURL string, compressedBody []byte, sig string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, bytes.NewReader(compressedBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	if sig != "" {
		req.Header.Set(signature.Header, sig)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := resp.Body.Close(); closeErr != nil {
			a.log.Error("close response body error", zap.Error(closeErr))
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return &statusError{code: resp.StatusCode}
	}
	return nil
}
