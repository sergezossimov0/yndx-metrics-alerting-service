package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/golang-migrate/migrate/v4"
	"github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/retry"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/migrations"
	"go.uber.org/zap"
)

// ApplyMigrations brings the database schema up to the latest migration from
// the migrations directory (embedded into the binary).
//
// It opens its own connection instead of taking the server's *sql.DB: closing
// the golang-migrate driver closes the *sql.DB it was given, which would break
// the shared pool.
//
// golang-migrate connects without a context, so an unreachable database would
// block until the OS gives up on TCP. The connection is therefore established
// first by PingContext, each attempt bounded by connectTimeout; the driver
// then reuses that pooled connection. The database is often still starting
// when the server starts, so a failed connection is retried with retryDelays
// (retry.DefaultDelays in production).
func ApplyMigrations(ctx context.Context, dsn string, connectTimeout time.Duration, retryDelays []time.Duration, log *zap.Logger) (err error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open migrations connection: %w", err)
	}

	connect := retry.Policy{
		Delays:    retryDelays,
		Retriable: isRetriablePgError,
		OnRetry: func(attempt int, delay time.Duration, err error) {
			log.Warn("database is not available, retrying",
				zap.Int("attempt", attempt), zap.Duration("delay", delay), zap.Error(err))
		},
	}
	err = connect.Do(ctx, func(ctx context.Context) error {
		pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
		defer cancel()
		return db.PingContext(pingCtx)
	})
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("connect to database for migrations: %w", err)
	}

	// WithInstance fills in DatabaseName and SchemaName from the connection,
	// so after the call cfg says where migrations are actually applied.
	cfg := &pgx.Config{}
	driver, err := pgx.WithInstance(db, cfg)
	if err != nil {
		_ = db.Close()
		return fmt.Errorf("create migrations driver: %w", err)
	}
	log = log.With(zap.String("database", cfg.DatabaseName), zap.String("schema", cfg.SchemaName))

	source, err := iofs.New(migrations.FS, ".")
	if err != nil {
		_ = driver.Close()
		return fmt.Errorf("open embedded migrations: %w", err)
	}

	m, err := migrate.NewWithInstance("iofs", source, "pgx5", driver)
	if err != nil {
		_ = source.Close()
		_ = driver.Close()
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if closeErr := errors.Join(srcErr, dbErr); closeErr != nil && err == nil {
			err = fmt.Errorf("close migrator: %w", closeErr)
		}
	}()

	m.Log = migrateLogger{log: log}

	before, err := currentVersion(m)
	if err != nil {
		return err
	}

	upErr := m.Up()
	if upErr != nil && !errors.Is(upErr, migrate.ErrNoChange) {
		return fmt.Errorf("apply migrations (schema version before: %d): %w", before, upErr)
	}

	after, err := currentVersion(m)
	if err != nil {
		return err
	}

	if errors.Is(upErr, migrate.ErrNoChange) {
		log.Info("no new migrations, schema already up to date", zap.Uint("version", after))
	} else {
		log.Info("migrations applied", zap.Uint("from_version", before), zap.Uint("to_version", after))
	}

	// golang-migrate trusts schema_migrations and does not check that the
	// tables exist: a table dropped by hand is not recreated. Fail at startup
	// with a clear message instead of on the first query.
	return checkSchemaTables(ctx, db, cfg.SchemaName, after, connectTimeout)
}

// schemaTable is a table the server relies on and the migration version
// that creates it. Keep the list in sync with the migrations directory.
type schemaTable struct {
	name  string
	since uint
}

var requiredTables = []schemaTable{
	{name: "metrics", since: 1},
}

// checkSchemaTables makes sure every table that should exist at version is
// actually there. Each lookup is bounded by timeout.
func checkSchemaTables(ctx context.Context, db *sql.DB, schema string, version uint, timeout time.Duration) error {
	missing, err := missingTables(requiredTables, version, func(name string) (bool, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return tableExists(ctx, db, schema, name)
	})
	if err != nil {
		return fmt.Errorf("check schema tables: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("schema_migrations says version %d, but tables %v are missing in schema %q: "+
			"they were dropped outside migrations; run `migrate down` or drop table schema_migrations "+
			"and restart the server to apply the migrations again", version, missing, schema)
	}
	return nil
}

// missingTables returns the tables that should exist at version (created by
// a migration with since <= version) but, according to exists, do not.
func missingTables(tables []schemaTable, version uint, exists func(name string) (bool, error)) ([]string, error) {
	var missing []string
	for _, t := range tables {
		if t.since > version {
			continue
		}
		ok, err := exists(t.name)
		if err != nil {
			return nil, fmt.Errorf("table %q: %w", t.name, err)
		}
		if !ok {
			missing = append(missing, t.name)
		}
	}
	return missing, nil
}

// tableExists reports whether schema.name exists. to_regclass returns NULL
// for a missing relation instead of failing; format('%I.%I') quotes both
// identifiers safely.
func tableExists(ctx context.Context, db *sql.DB, schema, name string) (bool, error) {
	var exists bool
	err := db.QueryRowContext(ctx,
		`SELECT to_regclass(format('%I.%I', $1::text, $2::text)) IS NOT NULL`, schema, name,
	).Scan(&exists)
	return exists, err
}

// currentVersion returns the applied schema version, 0 if no migration has
// been applied yet. A dirty version means a previous migration failed midway
// and needs manual repair (migrate force), so it is reported as an error.
func currentVersion(m *migrate.Migrate) (uint, error) {
	version, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read migration version: %w", err)
	}
	if dirty {
		return version, fmt.Errorf("schema version %d is dirty: a previous migration failed, fix the schema and run migrate force", version)
	}
	return version, nil
}

// migrateLogger routes golang-migrate's own messages (one line per applied
// migration, e.g. "1/u create_metrics_table (12ms)") to zap.
type migrateLogger struct {
	log *zap.Logger
}

func (l migrateLogger) Printf(format string, v ...any) {
	l.log.Info(strings.TrimSpace(fmt.Sprintf(format, v...)))
}

func (l migrateLogger) Verbose() bool {
	return false
}
