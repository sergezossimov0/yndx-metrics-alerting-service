package repository

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/migrations"
	"go.uber.org/zap"
)

// silentListener accepts TCP connections and never answers, like a database
// host that is reachable but hung. Without a timeout the PostgreSQL client
// would wait for the startup reply forever.
func silentListener(t *testing.T) (string, func() int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})

	return ln.Addr().String(), func() int {
		mu.Lock()
		defer mu.Unlock()
		return len(conns)
	}
}

// testRetryDelays keep the shape of retry.DefaultDelays (three retries) but
// in milliseconds, so tests that hit failures stay fast.
var testRetryDelays = []time.Duration{time.Millisecond, 2 * time.Millisecond, 3 * time.Millisecond}

func TestApplyMigrations_FailsWithinConnectTimeoutWhenDatabaseHangs(t *testing.T) {
	addr, _ := silentListener(t)
	dsn := "postgres://user@" + addr + "/metrics?sslmode=disable"

	const connectTimeout = 200 * time.Millisecond
	start := time.Now()
	err := ApplyMigrations(context.Background(), dsn, connectTimeout, nil, zap.NewNop())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error for a database that never answers")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected ApplyMigrations to give up after about %v, it took %v", connectTimeout, elapsed)
	}
}

func TestApplyMigrations_StopsWhenContextIsCancelled(t *testing.T) {
	addr, _ := silentListener(t)
	dsn := "postgres://user@" + addr + "/metrics?sslmode=disable"

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()
	err := ApplyMigrations(ctx, dsn, time.Minute, testRetryDelays, zap.NewNop())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error after the context was cancelled")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected ApplyMigrations to stop on cancellation, it took %v", elapsed)
	}
}

func TestApplyMigrations_RetriesConnectionThreeTimes(t *testing.T) {
	addr, accepted := silentListener(t)
	dsn := "postgres://user@" + addr + "/metrics?sslmode=disable"

	err := ApplyMigrations(context.Background(), dsn, 100*time.Millisecond, testRetryDelays, zap.NewNop())

	if err == nil {
		t.Fatal("expected an error for a database that never answers")
	}
	if n := accepted(); n != 4 {
		t.Fatalf("expected 1 attempt + 3 retries = 4 connections, got %d", n)
	}
	if !strings.Contains(err.Error(), "after 4 attempts") {
		t.Fatalf("expected the error to report the attempts, got %v", err)
	}
}

func TestApplyMigrations_RetriesRefusedConnection(t *testing.T) {
	// a listener that is closed right away gives "connection refused",
	// as when PostgreSQL is not running yet
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	dsn := "postgres://user@" + addr + "/metrics?sslmode=disable"

	err = ApplyMigrations(context.Background(), dsn, time.Second, testRetryDelays, zap.NewNop())

	if err == nil || !strings.Contains(err.Error(), "after 4 attempts") {
		t.Fatalf("expected a refused connection to be retried 3 times, got %v", err)
	}
}

func TestMissingTables(t *testing.T) {
	tables := []schemaTable{
		{name: "metrics", since: 1},
		{name: "later", since: 2},
	}
	existing := func(names ...string) func(string) (bool, error) {
		return func(name string) (bool, error) {
			for _, n := range names {
				if n == name {
					return true, nil
				}
			}
			return false, nil
		}
	}

	tests := []struct {
		name    string
		version uint
		exists  func(string) (bool, error)
		want    []string
	}{
		{name: "no migrations applied: nothing required", version: 0, exists: existing(), want: nil},
		{name: "all tables present", version: 2, exists: existing("metrics", "later"), want: nil},
		{name: "table dropped by hand", version: 1, exists: existing(), want: []string{"metrics"}},
		{name: "table of a later version not required yet", version: 1, exists: existing("metrics"), want: nil},
		{name: "several missing", version: 2, exists: existing(), want: []string{"metrics", "later"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := missingTables(tables, tt.version, tt.exists)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("expected missing %v, got %v", tt.want, got)
			}
		})
	}
}

func TestMissingTables_ReturnsLookupError(t *testing.T) {
	lookupErr := errors.New("connection lost")

	_, err := missingTables([]schemaTable{{name: "metrics", since: 1}}, 1, func(string) (bool, error) {
		return false, lookupErr
	})

	if !errors.Is(err, lookupErr) || !strings.Contains(err.Error(), `"metrics"`) {
		t.Fatalf("expected the lookup error for table metrics, got %v", err)
	}
}

// requiredTables must match the migrations: every listed table has to be
// created by the up migration of its version.
func TestRequiredTables_AreCreatedByTheirMigration(t *testing.T) {
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}

	for _, table := range requiredTables {
		prefix := fmt.Sprintf("%06d_", table.since)
		found := false
		for _, e := range entries {
			if !strings.HasPrefix(e.Name(), prefix) || !strings.HasSuffix(e.Name(), ".up.sql") {
				continue
			}
			body, err := fs.ReadFile(migrations.FS, e.Name())
			if err != nil {
				t.Fatalf("read %s: %v", e.Name(), err)
			}
			if strings.Contains(strings.ToLower(string(body)), "create table if not exists "+table.name) ||
				strings.Contains(strings.ToLower(string(body)), "create table "+table.name) {
				found = true
			}
		}
		if !found {
			t.Fatalf("table %q is expected since version %d, but no %s*.up.sql creates it", table.name, table.since, prefix)
		}
	}
}
