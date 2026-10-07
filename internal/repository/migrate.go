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
// first by PingContext, bounded by connectTimeout; the driver then reuses that
// pooled connection.
func ApplyMigrations(ctx context.Context, dsn string, connectTimeout time.Duration, log *zap.Logger) (err error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open migrations connection: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
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
		// golang-migrate trusts schema_migrations and does not check that the
		// tables exist: a table dropped by hand is not recreated.
		log.Info("no new migrations, schema already up to date", zap.Uint("version", after))
		return nil
	}
	log.Info("migrations applied", zap.Uint("from_version", before), zap.Uint("to_version", after))

	return nil
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
