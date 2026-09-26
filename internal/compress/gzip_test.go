package compress

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"sync"
	"testing"
)

func TestCompress_RoundTripsThroughNewReader(t *testing.T) {
	want := []byte(`{"id":"Alloc","type":"gauge","value":1.5}`)

	compressed, err := Compress(want)
	if err != nil {
		t.Fatalf("unexpected error compressing: %v", err)
	}

	zr, err := NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("unexpected error creating reader: %v", err)
	}
	defer zr.Close()

	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("unexpected error decompressing: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestCompress_OutputIsReadableByStandardGzip(t *testing.T) {
	want := []byte("payload")

	compressed, err := Compress(want)
	if err != nil {
		t.Fatalf("unexpected error compressing: %v", err)
	}

	zr, err := gzip.NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("expected valid gzip stream, got error: %v", err)
	}
	defer zr.Close()

	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("unexpected error decompressing: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestCompress_EmptyInputProducesValidStream(t *testing.T) {
	compressed, err := Compress(nil)
	if err != nil {
		t.Fatalf("unexpected error compressing: %v", err)
	}

	zr, err := NewReader(bytes.NewReader(compressed))
	if err != nil {
		t.Fatalf("expected valid gzip stream, got error: %v", err)
	}
	defer zr.Close()

	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("unexpected error decompressing: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty output, got %q", got)
	}
}

func TestNewReader_ReturnsErrorOnNonGzipInput(t *testing.T) {
	if _, err := NewReader(bytes.NewReader([]byte("not gzip"))); err == nil {
		t.Fatal("expected error for non-gzip input, got nil")
	}
}

func TestGetWriter_UsesSharedLevelAfterReuse(t *testing.T) {
	// the gzip header records the compression level: XFL byte 4 means BestSpeed.
	// Several rounds make sure a writer taken back from the pool keeps the level.
	const xflOffset, xflBestSpeed = 8, 4
	for round := 0; round < 3; round++ {
		var buf bytes.Buffer
		zw := GetWriter(&buf)
		if _, err := zw.Write([]byte("payload")); err != nil {
			t.Fatalf("round %d: unexpected error writing: %v", round, err)
		}
		if err := zw.Close(); err != nil {
			t.Fatalf("round %d: unexpected error closing: %v", round, err)
		}
		PutWriter(zw)

		if got := buf.Bytes()[xflOffset]; got != xflBestSpeed {
			t.Fatalf("round %d: expected XFL=%d (BestSpeed), got %d", round, xflBestSpeed, got)
		}
	}
}

func TestGetWriter_ReusedWriterWritesOnlyToNewDestination(t *testing.T) {
	var first bytes.Buffer
	zw := GetWriter(&first)
	if _, err := zw.Write([]byte("first")); err != nil {
		t.Fatalf("unexpected error writing: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("unexpected error closing: %v", err)
	}
	PutWriter(zw)
	firstLen := first.Len()

	var second bytes.Buffer
	zw = GetWriter(&second)
	if _, err := zw.Write([]byte("second")); err != nil {
		t.Fatalf("unexpected error writing: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("unexpected error closing: %v", err)
	}
	PutWriter(zw)

	if first.Len() != firstLen {
		t.Fatalf("expected the previous destination to stay untouched, it grew from %d to %d bytes", firstLen, first.Len())
	}
	zr, err := NewReader(&second)
	if err != nil {
		t.Fatalf("expected valid gzip stream, got error: %v", err)
	}
	got, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("unexpected error decompressing: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("expected %q, got %q", "second", got)
	}
}

func TestCompress_ConcurrentCallsDoNotMixData(t *testing.T) {
	// if a writer were handed to two callers at once, some outputs would be
	// corrupted or contain another caller's data
	const workers, iterations = 8, 200

	var wg sync.WaitGroup
	errs := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				want := []byte(fmt.Sprintf(`{"worker":%d,"iteration":%d}`, w, i))
				compressed, err := Compress(want)
				if err != nil {
					errs <- fmt.Errorf("worker %d: compress: %w", w, err)
					return
				}
				zr, err := NewReader(bytes.NewReader(compressed))
				if err != nil {
					errs <- fmt.Errorf("worker %d: reader: %w", w, err)
					return
				}
				got, err := io.ReadAll(zr)
				if err != nil {
					errs <- fmt.Errorf("worker %d: decompress: %w", w, err)
					return
				}
				if !bytes.Equal(got, want) {
					errs <- fmt.Errorf("worker %d: expected %q, got %q", w, want, got)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		t.Error(err)
	}
}

func BenchmarkCompress(b *testing.B) {
	payload := []byte(`{"id":"HeapAlloc","type":"gauge","value":2539408}`)
	b.ReportAllocs()
	for b.Loop() {
		if _, err := Compress(payload); err != nil {
			b.Fatal(err)
		}
	}
}
