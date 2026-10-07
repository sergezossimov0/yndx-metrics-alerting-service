package repository

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"go.uber.org/zap"
)

// silentListener accepts TCP connections and never answers, like a database
// host that is reachable but hung. Without a timeout the PostgreSQL client
// would wait for the startup reply forever.
func silentListener(t *testing.T) string {
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

	return ln.Addr().String()
}

func TestApplyMigrations_FailsWithinConnectTimeoutWhenDatabaseHangs(t *testing.T) {
	addr := silentListener(t)
	dsn := "postgres://user@" + addr + "/metrics?sslmode=disable"

	const connectTimeout = 200 * time.Millisecond
	start := time.Now()
	err := ApplyMigrations(context.Background(), dsn, connectTimeout, zap.NewNop())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error for a database that never answers")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected ApplyMigrations to give up after about %v, it took %v", connectTimeout, elapsed)
	}
}

func TestApplyMigrations_StopsWhenContextIsCancelled(t *testing.T) {
	addr := silentListener(t)
	dsn := "postgres://user@" + addr + "/metrics?sslmode=disable"

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(100*time.Millisecond, cancel)

	start := time.Now()
	err := ApplyMigrations(ctx, dsn, time.Minute, zap.NewNop())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected an error after the context was cancelled")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("expected ApplyMigrations to stop on cancellation, it took %v", elapsed)
	}
}
