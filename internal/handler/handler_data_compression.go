package handler

import (
	"net/http"
	"strings"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	"go.uber.org/zap"
)

// isCompressibleContentType сообщает, разрешено ли сжимать ответ для запроса
// с таким Content-Type. Пустой Content-Type (например, у GET-запросов без тела,
// как GET /) тоже считается допустимым — иначе такие запросы никогда бы не
// сжимались, просто потому что у них нет заголовка Content-Type вовсе.
func isCompressibleContentType(contentType string) bool {
	return contentType == "" ||
		strings.HasPrefix(contentType, "application/json") ||
		strings.HasPrefix(contentType, "text/html")
}

func CompressionHandler(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// по умолчанию устанавливаем оригинальный http.ResponseWriter как тот,
		// который будем передавать следующей функции
		ow := w

		// проверяем, что клиент умеет получать от сервера сжатые данные в формате gzip
		// и что тип контента запроса поддерживает сжатие ответа
		acceptEncoding := r.Header.Get("Accept-Encoding")
		supportsGzip := strings.Contains(acceptEncoding, "gzip") && isCompressibleContentType(r.Header.Get(headerContentType))
		if supportsGzip {
			// оборачиваем оригинальный http.ResponseWriter новым с поддержкой сжатия
			cw := newCompressWriter(w)
			// меняем оригинальный http.ResponseWriter на новый
			ow = cw
			// не забываем отправить клиенту все данные после завершения middleware
			defer func() {
				if err := cw.Close(); err != nil {
					logger.Log.Error("close compress writer error", zap.Error(err))
				}
			}()
		}

		// проверяем, что клиент отправил серверу сжатые данные в формате gzip
		contentEncoding := r.Header.Get("Content-Encoding")
		sendsGzip := strings.Contains(contentEncoding, "gzip")
		if sendsGzip {
			// оборачиваем тело запроса в io.Reader с поддержкой декомпрессии
			cr, err := newCompressReader(r.Body)
			if err != nil {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			// меняем тело запроса на новое
			r.Body = cr
			defer cr.Close()
		}

		// передаём управление хендлеру
		h.ServeHTTP(ow, r)
	})
}
