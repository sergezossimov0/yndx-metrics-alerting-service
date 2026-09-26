package handler

import (
	"net/http"
	"strings"

	"go.uber.org/zap"
)

// isCompressibleContentType сообщает, разрешено ли сжимать ответ для запроса
// с таким Content-Type. Пустой Content-Type (например, у GET-запросов без тела,
// как GET /) тоже считается допустимым — иначе такие запросы никогда бы не
// сжимались, просто потому что у них нет заголовка Content-Type вовсе.
func isCompressibleContentType(contentType string) bool {
	return contentType == "" ||
		strings.HasPrefix(contentType, contentTypeJSON) ||
		strings.HasPrefix(contentType, contentTypeHTML)
}

// CompressionHandler returns a middleware that gzips responses and ungzips requests.
func CompressionHandler(log *zap.Logger) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// проверяем, что клиент отправил серверу сжатые данные в формате gzip.
			// Тело запроса проверяется до подготовки сжатия ответа: если оно не
			// является валидным gzip, 400 уходит клиенту раньше, чем появится
			// compressWriter. Иначе его отложенный Close повторно вызвал бы
			// WriteHeader со статусом 200, и в журнал запросов попал бы неверный статус.
			contentEncoding := r.Header.Get("Content-Encoding")
			sendsGzip := strings.Contains(contentEncoding, "gzip")
			if sendsGzip {
				// оборачиваем тело запроса в io.Reader с поддержкой декомпрессии
				cr, err := newCompressReader(r.Body)
				if err != nil {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				// меняем тело запроса на новое
				r.Body = cr
				defer cr.Close()
			}

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
						log.Error("close compress writer error", zap.Error(err))
					}
				}()
			}

			// передаём управление хендлеру
			h.ServeHTTP(ow, r)
		})
	}
}
