package main

import (
	"context"
	"database/sql"
	"errors"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/repository"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/retry"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/usecase"
	"go.uber.org/zap"
)

// storage is what the server needs from the selected backend.
type storage struct {
	read   usecase.MetricReadStore
	update usecase.MetricUpdateStore
	db     *sql.DB // nil unless PostgreSQL is configured; used by /ping
	close  func()  // releases the backend; call after the HTTP server has stopped
}

// newStore picks the backend: PostgreSQL if a DSN is set, otherwise memory
// with snapshots to a file.
func newStore(ctx context.Context, cfg *serverConfig, log *zap.Logger) (*storage, error) {
	if cfg.dbConnection != "" {
		return newPostgresStore(ctx, cfg, log)
	}
	return newMemoryStore(cfg, log), nil
}

// newPostgresStore opens the database and applies migrations before handing
// it out. ctx bounds the migrations only.
func newPostgresStore(ctx context.Context, cfg *serverConfig, log *zap.Logger) (*storage, error) {
	db, err := sql.Open("pgx", cfg.dbConnection)
	if err != nil {
		return nil, err
	}

	if err := repository.ApplyMigrations(ctx, cfg.dbConnection, dbConnectionTimeout, retry.DefaultDelays,
		log.With(zap.String("component", "migrations"))); err != nil {
		return nil, errors.Join(err, db.Close())
	}

	dbStore := repository.NewPostgresStorage(db, dbConnectionTimeout, retry.DefaultDelays,
		log.With(zap.String("component", "postgres")))

	return &storage{
		read:   dbStore,
		update: dbStore,
		db:     db,
		close: func() {
			if err := db.Close(); err != nil {
				log.Error("failed to close database", zap.Error(err))
			}
		},
	}, nil
}

// newMemoryStore keeps metrics in memory and persists them to the file storage
// path: on every update when storeInterval is 0, otherwise periodically.
//
// The periodic snapshot runs on its own context rather than the server's, so
// the final save happens in close, after Shutdown has drained in-flight
// requests, and does not race with them.
func newMemoryStore(cfg *serverConfig, log *zap.Logger) *storage {
	memStore := repository.NewMemStorage()
	snapshot := repository.NewSnapshot(cfg.fileStoragePath, cfg.storeInterval.Duration, memStore,
		cfg.restore, log.With(zap.String("component", "snapshot")))

	if cfg.storeInterval.Duration == 0 {
		return &storage{
			read:   memStore,
			update: snapshot,
			close: func() {
				// Nothing to release: snapshot saves synchronously on every update.
			},
		}
	}

	snapCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		snapshot.Run(snapCtx)
	}()

	return &storage{
		read:   memStore,
		update: memStore,
		close: func() {
			cancel()
			<-done
		},
	}
}
