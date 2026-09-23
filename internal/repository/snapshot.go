package repository

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"github.com/mailru/easyjson"
	"github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/logger"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

type Snapshot struct {
	path          string
	storeInterval time.Duration
	store         *MemStorage
	mu            sync.Mutex
}

func NewSnapshot(path string, storeInterval int, store *MemStorage) *Snapshot {
	return &Snapshot{
		path:          path,
		storeInterval: time.Duration(storeInterval) * time.Second,
		store:         store,
	}
}

// Restore loads the snapshot file into the store. A missing file is not an
// error: it simply means nothing has been saved yet.
func (s *Snapshot) Restore() error {
	metrics, err := loadSnapshotFromFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	s.store.Restore(metrics)
	return nil
}

// Update stores the metric and synchronously writes a snapshot to disk. A
// failed disk write is only logged: the metric is already in memory, so the
// update itself succeeded and the next successful write will persist it.
func (s *Snapshot) Update(metric *models.Metrics) error {
	if err := s.store.Update(metric); err != nil {
		return err
	}

	if err := s.persist(); err != nil {
		logger.Log.Error("failed to persist metrics snapshot", zap.String("path", s.path), zap.Error(err))
	}
	return nil
}

// Run saves the store every storeInterval until ctx is cancelled, then
// performs one final save so nothing written since the last tick is lost.
// It blocks, so start it in its own goroutine. A non-positive interval means
// synchronous mode (see Update), so Run returns immediately.
func (s *Snapshot) Run(ctx context.Context) {
	if s.storeInterval <= 0 {
		logger.Log.Warn("periodic snapshot disabled: non-positive store interval", zap.Duration("interval", s.storeInterval))
		return
	}

	storeTicker := time.NewTicker(s.storeInterval)
	defer storeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			if err := s.persist(); err != nil {
				logger.Log.Error("failed to persist final metrics snapshot", zap.String("path", s.path), zap.Error(err))
			}
			return
		case <-storeTicker.C:
			if err := s.persist(); err != nil {
				logger.Log.Error("failed to persist metrics snapshot", zap.String("path", s.path), zap.Error(err))
			}
		}
	}
}

// persist takes the store snapshot and writes it under the same lock, so the
// file always ends up with the most recent state.
func (s *Snapshot) persist() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return saveSnapshotToFile(s.path, s.store.Snapshot())
}

func saveSnapshotToFile(path string, metrics []models.Metrics) error {
	data, err := easyjson.Marshal(models.MetricsList(metrics))
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func loadSnapshotFromFile(path string) ([]models.Metrics, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	var metrics models.MetricsList
	if err := easyjson.Unmarshal(data, &metrics); err != nil {
		return nil, err
	}
	return metrics, nil
}
