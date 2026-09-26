package handler

import (
	"net/http"
	"time"

	"go.uber.org/zap"
)

// WithLogging returns a middleware that writes an access log entry for every request.
func WithLogging(log *zap.Logger) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()

			responseData := &responseData{
				status: 0,
				size:   0,
			}
			lw := loggingResponseWriter{
				ResponseWriter: w, // встраиваем оригинальный http.ResponseWriter
				responseData:   responseData,
			}
			h.ServeHTTP(&lw, r) // внедряем реализацию http.ResponseWriter

			duration := time.Since(start)

			log.Info("HTTP request",
				zap.String("uri", r.RequestURI),
				zap.String("method", r.Method),
				zap.Duration("duration", duration),
				zap.Int("status", responseData.status), // получаем перехваченный код статуса ответа
				zap.Int("size", responseData.size),     // получаем перехваченный размер ответа
			)
		})
	}
}

type (
	// берём структуру для хранения сведений об ответе
	responseData struct {
		status int
		size   int
	}

	// добавляем реализацию http.ResponseWriter
	loggingResponseWriter struct {
		http.ResponseWriter // встраиваем оригинальный http.ResponseWriter
		responseData        *responseData
	}
)

func (r *loggingResponseWriter) Write(b []byte) (int, error) {
	if r.responseData.status == 0 {
		// Write без предварительного WriteHeader неявно отправляет 200,
		// поэтому фиксируем это здесь, иначе в логах останется статус 0
		r.responseData.status = http.StatusOK
	}
	// записываем ответ, используя оригинальный http.ResponseWriter
	size, err := r.ResponseWriter.Write(b)
	r.responseData.size += size // захватываем размер
	return size, err
}

func (r *loggingResponseWriter) WriteHeader(statusCode int) {
	// записываем код статуса, используя оригинальный http.ResponseWriter
	r.ResponseWriter.WriteHeader(statusCode)
	r.responseData.status = statusCode // захватываем код статуса
}
