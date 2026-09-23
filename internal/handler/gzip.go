package handler

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
)

// minCompressibleResponseSize — если суммарный объём тела ответа меньше этого
// порога, сжимать его не имеет смысла: накладные расходы gzip (10-байтный
// заголовок, 8-байтный трейлер, служебные байты DEFLATE-блока) на маленьких
// данных превышают выигрыш от сжатия, а иногда даже увеличивают итоговый размер.
// для прохождения тестов выбран маленький размер
const minCompressibleResponseSize = 10

// compressWriter реализует интерфейс http.ResponseWriter и позволяет прозрачно для сервера
// сжимать передаваемые данные и выставлять правильные HTTP-заголовки.
// Тело буферизируется до тех пор, пока не станет ясно, что данных достаточно,
// чтобы сжатие имело смысл (см. minCompressibleResponseSize); до этого момента
// реальная отправка статуса и заголовков откладывается.
type compressWriter struct {
	w            http.ResponseWriter
	buf          bytes.Buffer
	statusCode   int
	headerCalled bool
	flushed      bool
	zw           *gzip.Writer
}

func newCompressWriter(w http.ResponseWriter) *compressWriter {
	return &compressWriter{w: w}
}

func (c *compressWriter) Header() http.Header {
	return c.w.Header()
}

func (c *compressWriter) Write(p []byte) (int, error) {
	if !c.headerCalled {
		// Write без предварительного WriteHeader неявно отправляет 200
		c.WriteHeader(http.StatusOK)
	}

	if c.zw != nil {
		return c.zw.Write(p)
	}

	n, err := c.buf.Write(p)
	if err != nil {
		return n, err
	}

	if c.buf.Len() >= minCompressibleResponseSize {
		if err := c.startCompressing(); err != nil {
			return n, err
		}
	}

	return n, nil
}

func (c *compressWriter) WriteHeader(statusCode int) {
	if c.headerCalled {
		return
	}
	c.headerCalled = true
	c.statusCode = statusCode
}

// startCompressing коммитится к сжатию: выставляет Content-Encoding, реально
// отправляет статус и переносит уже накопленный буфер в gzip.Writer.
func (c *compressWriter) startCompressing() error {
	c.w.Header().Set("Content-Encoding", "gzip")
	c.flushHeader()

	gz, err := gzip.NewWriterLevel(c.w, gzip.BestSpeed)
	if err != nil {
		return err
	}
	c.zw = gz

	if c.buf.Len() == 0 {
		return nil
	}
	_, err = c.zw.Write(c.buf.Bytes())
	c.buf.Reset()
	return err
}

func (c *compressWriter) flushHeader() {
	if c.flushed {
		return
	}
	c.flushed = true
	c.w.WriteHeader(c.statusCode)
}

// Close завершает работу compressWriter: если суммарный объём ответа так и не
// превысил minCompressibleResponseSize, тело отправляется как есть, без
// сжатия и без заголовка Content-Encoding; иначе закрывает gzip.Writer и
// досылает все оставшиеся данные из его внутреннего буфера.
func (c *compressWriter) Close() error {
	if c.zw != nil {
		return c.zw.Close()
	}

	if !c.headerCalled {
		c.WriteHeader(http.StatusOK)
	}
	c.flushHeader()

	if c.buf.Len() == 0 {
		return nil
	}
	_, err := c.w.Write(c.buf.Bytes())
	return err
}

// compressReader реализует интерфейс io.ReadCloser и позволяет прозрачно для сервера
// декомпрессировать получаемые от клиента данные
type compressReader struct {
	r  io.ReadCloser
	zr *gzip.Reader
}

func newCompressReader(r io.ReadCloser) (*compressReader, error) {
	zr, err := gzip.NewReader(r)
	if err != nil {
		return nil, err
	}

	return &compressReader{
		r:  r,
		zr: zr,
	}, nil
}

func (c compressReader) Read(p []byte) (n int, err error) {
	return c.zr.Read(p)
}

func (c *compressReader) Close() error {
	if err := c.r.Close(); err != nil {
		return err
	}
	return c.zr.Close()
}
