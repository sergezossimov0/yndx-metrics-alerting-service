package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/config"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

func memoryConfig(path string, interval time.Duration, restore bool) *serverConfig {
	return &serverConfig{
		fileStoragePath: path,
		storeInterval:   config.Interval{Duration: interval, AllowZero: true},
		restore:         restore,
	}
}

func updateGauge(t *testing.T, s *storage, name string, value float64) {
	t.Helper()
	if err := s.update.Update(context.Background(), &models.Metrics{ID: name, MType: models.Gauge, Value: &value}); err != nil {
		t.Fatalf("update %s: %v", name, err)
	}
}

// restoredGauge opens a fresh memory store over path and reads name back from it.
func restoredGauge(t *testing.T, path, name string) (float64, bool) {
	t.Helper()
	s := newMemoryStore(memoryConfig(path, 0, true), zap.NewNop())
	defer s.close()
	return s.read.GetGauge(context.Background(), name)
}

func TestNewStore_WithoutDSNUsesMemory(t *testing.T) {
	cfg := memoryConfig(filepath.Join(t.TempDir(), "metrics.json"), 0, false)

	s, err := newStore(context.Background(), cfg, zap.NewNop())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer s.close()

	if s.db != nil {
		t.Fatal("expected no database without a DSN")
	}
}

func TestNewMemoryStore_SyncModeWritesEveryUpdate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	s := newMemoryStore(memoryConfig(path, 0, false), zap.NewNop())
	defer s.close()

	updateGauge(t, s, "Alloc", 1.5)

	if got, ok := restoredGauge(t, path, "Alloc"); !ok || got != 1.5 {
		t.Fatalf("expected Alloc=1.5 on disk right after the update, got %v (present=%v)", got, ok)
	}
}

func TestNewMemoryStore_PeriodicModeSavesOnClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	s := newMemoryStore(memoryConfig(path, time.Hour, false), zap.NewNop())

	updateGauge(t, s, "Alloc", 2.5)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected no snapshot before close, stat err: %v", err)
	}

	s.close()

	if got, ok := restoredGauge(t, path, "Alloc"); !ok || got != 2.5 {
		t.Fatalf("expected close to save Alloc=2.5, got %v (present=%v)", got, ok)
	}
}

func TestNewMemoryStore_RestoreFalseStartsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.json")
	s := newMemoryStore(memoryConfig(path, 0, false), zap.NewNop())
	updateGauge(t, s, "Alloc", 1.5)
	s.close()

	fresh := newMemoryStore(memoryConfig(path, 0, false), zap.NewNop())
	defer fresh.close()
	if _, ok := fresh.read.GetGauge(context.Background(), "Alloc"); ok {
		t.Fatal("expected restore=false to ignore the saved snapshot")
	}
}

func TestRun_ReturnsListenError(t *testing.T) {
	err := run(context.Background(), "127.0.0.1:-1", http.NotFoundHandler(), zap.NewNop())
	if err == nil {
		t.Fatal("expected a listen error for an invalid address, got nil")
	}
}

func TestRun_ShutdownWaitsForInFlightRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	started, release := make(chan struct{}), make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/ready", func(w http.ResponseWriter, _ *http.Request) {})
	mux.HandleFunc("/slow", func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runErr := make(chan error, 1)
	go func() { runErr <- run(ctx, addr, mux, zap.NewNop()) }()

	waitForServer(t, "http://"+addr+"/ready")

	slowStatus := make(chan int, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/slow")
		if err != nil {
			slowStatus <- 0
			return
		}
		_ = resp.Body.Close()
		slowStatus <- resp.StatusCode
	}()

	<-started
	cancel()
	close(release)

	if code := <-slowStatus; code != http.StatusOK {
		t.Fatalf("expected the in-flight request to complete with 200, got %d", code)
	}
	if err := <-runErr; err != nil {
		t.Fatalf("expected graceful shutdown without error, got %v", err)
	}
}

func waitForServer(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if resp, err := http.Get(url); err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("server at %s did not start", url)
}
