// Package compress holds the gzip logic shared by the agent and the server,
// so the compression level and error handling are defined in one place.
package compress

import (
	"bytes"
	"compress/gzip"
	"io"
	"sync"
)

// Level is the gzip compression level shared by the agent and the server.
// BestSpeed is enough for small JSON payloads: the size gain of higher levels
// is negligible there, while the CPU cost is not.
const Level = gzip.BestSpeed

// writerPool reuses gzip writers: a new one allocates ~800 KB of DEFLATE
// tables even for a tiny payload, while Reset only clears its state.
// One pool serves one level, because Reset keeps the writer's level.
var writerPool = sync.Pool{
	New: func() any {
		// Level is a valid constant, so NewWriterLevel cannot fail here
		zw, _ := gzip.NewWriterLevel(io.Discard, Level)
		return zw
	},
}

// GetWriter returns a pooled gzip writer that writes to w.
// The caller must Close it and then return it with PutWriter exactly once:
// a writer put twice would be handed to two callers at the same time.
func GetWriter(w io.Writer) *gzip.Writer {
	zw := writerPool.Get().(*gzip.Writer)
	zw.Reset(w)
	return zw
}

// PutWriter returns zw to the pool; zw must not be used afterwards.
func PutWriter(zw *gzip.Writer) {
	zw.Reset(io.Discard) // drop the reference to the previous destination
	writerPool.Put(zw)
}

// NewReader returns a reader that decompresses gzip data from r.
func NewReader(r io.Reader) (*gzip.Reader, error) {
	return gzip.NewReader(r)
}

// Compress gzips data in one call; convenient for small request bodies.
// The buffer is not pooled: the returned slice aliases it.
func Compress(data []byte) ([]byte, error) {
	var buf bytes.Buffer
	zw := GetWriter(&buf)
	defer PutWriter(zw)

	if _, err := zw.Write(data); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
