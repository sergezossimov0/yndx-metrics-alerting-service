package handler

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"go.uber.org/zap"
)

func PingHandler(db *sql.DB, timeout time.Duration, log *zap.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		if err := db.PingContext(ctx); err != nil {
			log.Error("Ping failed", zap.Error(err))
			http.Error(w, "Ping failed", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}
}
