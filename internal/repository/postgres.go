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
	"go.uber.org/zap"
)

const connectionTimeout = 2 * time.Second

// Gauges and counters live in one table keyed by (id, mtype), so the same
// name may exist as both a gauge and a counter, as in MemStorage. The CHECK
// constraint guarantees exactly one of delta/value is set for the row's type.
const createMetricsTable = `
CREATE TABLE IF NOT EXISTS metrics (
    id    TEXT             NOT NULL,
    mtype TEXT             NOT NULL CHECK (mtype IN ('gauge', 'counter')),
    delta BIGINT,
    value DOUBLE PRECISION,
    PRIMARY KEY (id, mtype),
    CHECK (
        (mtype = 'gauge'   AND value IS NOT NULL AND delta IS NULL) OR
        (mtype = 'counter' AND delta IS NOT NULL AND value IS NULL)
    )
)`

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
// interfaces as MemStorage. Every query runs under the caller's context,
// bounded by its own timeout. The read methods return no error, so read
// errors are logged and reported as "not found" / empty.
type PostgresStorage struct {
	db      *sql.DB
	timeout time.Duration
	log     *zap.Logger
}

// NewPostgresStorage creates the metrics table if it does not exist yet.
func NewPostgresStorage(ctx context.Context, db *sql.DB, timeout time.Duration, log *zap.Logger) (*PostgresStorage, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if _, err := db.ExecContext(ctx, createMetricsTable); err != nil {
		return nil, fmt.Errorf("create metrics table: %w", err)
	}

	return &PostgresStorage{db: db, timeout: timeout, log: log}, nil
}

func (s *PostgresStorage) Update(ctx context.Context, metric *models.Metrics) error {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	s.log.Info("updating metrics", zap.Any("metrics", metric))

	var err error
	if metric.MType == models.Gauge && metric.Value != nil {
		_, err = s.db.ExecContext(ctx, upsertGauge, metric.ID, *metric.Value)
	} else if metric.MType == models.Counter && metric.Delta != nil {
		_, err = s.db.ExecContext(ctx, upsertCounter, metric.ID, *metric.Delta)
	}
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
func (s *PostgresStorage) UpdateBatch(ctx context.Context, metrics []models.Metrics) (err error) {
	if len(metrics) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	sorted := slices.Clone(metrics)
	slices.SortStableFunc(sorted, func(a, b models.Metrics) int {
		return cmp.Or(cmp.Compare(a.ID, b.ID), cmp.Compare(a.MType, b.MType))
	})

	s.log.Info("updating metrics batch", zap.Int("count", len(sorted)))

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
	gauges := make(map[string]float64)
	s.list(ctx, selectGauges, func(rows *sql.Rows) error {
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
	counters := make(map[string]int64)
	s.list(ctx, selectCounters, func(rows *sql.Rows) error {
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
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	err := s.db.QueryRowContext(ctx, query, name).Scan(dest)
	if errors.Is(err, sql.ErrNoRows) {
		return false
	}
	if err != nil {
		s.log.Error("Failed to get metric", zap.String("id", name), zap.Error(err))
		return false
	}

	return true
}

// list runs query and calls scan for every row. Errors are logged, and the
// caller gets whatever rows were scanned before the failure.
func (s *PostgresStorage) list(ctx context.Context, query string, scan func(*sql.Rows) error) {
	ctx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		s.log.Error("Failed to list metrics", zap.Error(err))
		return
	}
	defer rows.Close()

	for rows.Next() {
		if err := scan(rows); err != nil {
			s.log.Error("Failed to scan metric", zap.Error(err))
			return
		}
	}
	if err := rows.Err(); err != nil {
		s.log.Error("Failed to iterate metrics", zap.Error(err))
	}
}
