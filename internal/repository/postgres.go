package repository

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/retry"
	"go.uber.org/zap"
)

// The metrics table is created by the migrations in the migrations directory
// (see ApplyMigrations); the queries below rely on its schema.
const (
	upsertGauge = `
INSERT INTO metrics (id, mtype, value) VALUES ($1, 'gauge', $2)
ON CONFLICT (id, mtype) DO UPDATE SET value = EXCLUDED.value`

	// The increment happens inside the database, so concurrent updates of the
	// same counter are not lost and no Go-side lock is needed.
	upsertCounter = `
INSERT INTO metrics (id, mtype, delta) VALUES ($1, 'counter', $2)
ON CONFLICT (id, mtype) DO UPDATE SET delta = metrics.delta + EXCLUDED.delta`

	selectGauge    = `SELECT value FROM metrics WHERE id = $1 AND mtype = 'gauge'`
	selectCounter  = `SELECT delta FROM metrics WHERE id = $1 AND mtype = 'counter'`
	selectGauges   = `SELECT id, value FROM metrics WHERE mtype = 'gauge'`
	selectCounters = `SELECT id, delta FROM metrics WHERE mtype = 'counter'`
)

// PostgresStorage keeps metrics in PostgreSQL. It satisfies the same storage
// interfaces as MemStorage. Every operation is retried on temporary transport
// errors (see isRetriablePgError) with the configured delays; each attempt is
// bounded by its own timeout, so the pauses do not eat into it. The read
// methods return no error, so read errors are logged after the last attempt
// and reported as "not found" / empty.
type PostgresStorage struct {
	db      *sql.DB
	timeout time.Duration
	retry   retry.Policy
	log     *zap.Logger
}

// NewPostgresStorage expects the schema to be in place: run ApplyMigrations
// before using the storage. timeout bounds a single attempt; retryDelays are
// the pauses before each retry (retry.DefaultDelays in production).
func NewPostgresStorage(db *sql.DB, timeout time.Duration, retryDelays []time.Duration, log *zap.Logger) *PostgresStorage {
	return &PostgresStorage{
		db:      db,
		timeout: timeout,
		retry: retry.Policy{
			Delays:    retryDelays,
			Retriable: isRetriablePgError,
			OnRetry: func(attempt int, delay time.Duration, err error) {
				log.Warn("database operation failed, retrying",
					zap.Int("attempt", attempt), zap.Duration("delay", delay), zap.Error(err))
			},
		},
		log: log,
	}
}

// withRetry runs fn under the retry policy, giving every attempt its own
// timeout.
func (s *PostgresStorage) withRetry(ctx context.Context, fn func(ctx context.Context) error) error {
	return s.retry.Do(ctx, func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, s.timeout)
		defer cancel()
		return fn(ctx)
	})
}

func (s *PostgresStorage) Update(ctx context.Context, metric *models.Metrics) error {
	s.log.Info("updating metrics", zap.Any("metrics", metric))

	err := s.withRetry(ctx, func(ctx context.Context) error {
		var err error
		if metric.MType == models.Gauge && metric.Value != nil {
			_, err = s.db.ExecContext(ctx, upsertGauge, metric.ID, *metric.Value)
		} else if metric.MType == models.Counter && metric.Delta != nil {
			_, err = s.db.ExecContext(ctx, upsertCounter, metric.ID, *metric.Delta)
		}
		return err
	})
	if err != nil {
		return fmt.Errorf("update %s %q: %w", metric.MType, metric.ID, err)
	}

	return nil
}

// UpdateBatch applies all metrics in one transaction: either the whole batch
// is stored or nothing is. Each metric goes through the same upsert as Update,
// so duplicates within a batch are handled like separate updates (counters
// add up, the last gauge wins). Rows are written in (id, mtype) order, so
// concurrent batches lock them in the same order and cannot deadlock.
//
// A retry repeats the whole transaction: after a connection error the
// transaction is gone, and repeating a single statement would be meaningless.
func (s *PostgresStorage) UpdateBatch(ctx context.Context, metrics []models.Metrics) error {
	if len(metrics) == 0 {
		return nil
	}

	sorted := slices.Clone(metrics)
	slices.SortStableFunc(sorted, func(a, b models.Metrics) int {
		return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(a.MType, b.MType))
	})

	s.log.Info("updating metrics batch", zap.Int("count", len(sorted)))

	return s.withRetry(ctx, func(ctx context.Context) error {
		return s.updateBatchTx(ctx, sorted)
	})
}

// updateBatchTx makes one attempt to store the sorted batch in a transaction.
func (s *PostgresStorage) updateBatchTx(ctx context.Context, sorted []models.Metrics) (err error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil && !errors.Is(rbErr, sql.ErrTxDone) {
				s.log.Error("Failed to rollback metrics batch", zap.Error(rbErr))
			}
		}
	}()

	gaugeStmt, err := tx.PrepareContext(ctx, upsertGauge)
	if err != nil {
		return fmt.Errorf("prepare gauge upsert: %w", err)
	}
	defer gaugeStmt.Close()

	counterStmt, err := tx.PrepareContext(ctx, upsertCounter)
	if err != nil {
		return fmt.Errorf("prepare counter upsert: %w", err)
	}
	defer counterStmt.Close()

	for _, metric := range sorted {
		if metric.MType == models.Gauge && metric.Value != nil {
			_, err = gaugeStmt.ExecContext(ctx, metric.ID, *metric.Value)
		} else if metric.MType == models.Counter && metric.Delta != nil {
			_, err = counterStmt.ExecContext(ctx, metric.ID, *metric.Delta)
		}
		if err != nil {
			return fmt.Errorf("update %s %q: %w", metric.MType, metric.ID, err)
		}
	}

	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit metrics batch: %w", err)
	}
	return nil
}

func (s *PostgresStorage) GetGauge(ctx context.Context, name string) (float64, bool) {
	var v float64
	return v, s.getOne(ctx, selectGauge, name, &v)
}

func (s *PostgresStorage) GetCounter(ctx context.Context, name string) (int64, bool) {
	var v int64
	return v, s.getOne(ctx, selectCounter, name, &v)
}

func (s *PostgresStorage) ListGauges(ctx context.Context) map[string]float64 {
	var gauges map[string]float64
	s.list(ctx, selectGauges, func() {
		gauges = make(map[string]float64)
	}, func(rows *sql.Rows) error {
		var (
			id string
			v  float64
		)
		if err := rows.Scan(&id, &v); err != nil {
			return err
		}
		gauges[id] = v
		return nil
	})

	return gauges
}

func (s *PostgresStorage) ListCounters(ctx context.Context) map[string]int64 {
	var counters map[string]int64
	s.list(ctx, selectCounters, func() {
		counters = make(map[string]int64)
	}, func(rows *sql.Rows) error {
		var (
			id string
			v  int64
		)
		if err := rows.Scan(&id, &v); err != nil {
			return err
		}
		counters[id] = v
		return nil
	})

	return counters
}

// getOne scans a single-column row into dest and reports whether it was found.
func (s *PostgresStorage) getOne(ctx context.Context, query, name string, dest any) bool {
	found := false
	err := s.withRetry(ctx, func(ctx context.Context) error {
		err := s.db.QueryRowContext(ctx, query, name).Scan(dest)
		if errors.Is(err, sql.ErrNoRows) {
			found = false
			return nil
		}
		found = err == nil
		return err
	})
	if err != nil {
		s.log.Error("Failed to get metric", zap.String("id", name), zap.Error(err))
		return false
	}

	return found
}

// list runs query and calls scan for every row. reset is called before every
// attempt, so rows from a failed attempt are not mixed with a retry. Errors
// are logged after the last attempt, and the caller gets whatever rows that
// attempt scanned before the failure.
func (s *PostgresStorage) list(ctx context.Context, query string, reset func(), scan func(*sql.Rows) error) {
	err := s.withRetry(ctx, func(ctx context.Context) error {
		reset()

		rows, err := s.db.QueryContext(ctx, query)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			if err := scan(rows); err != nil {
				return err
			}
		}
		return rows.Err()
	})
	if err != nil {
		s.log.Error("Failed to list metrics", zap.Error(err))
	}
}
