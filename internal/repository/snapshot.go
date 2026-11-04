package repository

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/mailru/easyjson"
	models "github.com/sergezossimov0/yndx-metrics-alerting-service.git/internal/model"
	"go.uber.org/zap"
)

type Snapshot struct {
	path          string
	storeInterval time.Duration
	store         *MemStorage
	log           *zap.Logger
	mu            sync.Mutex
}

// NewSnapshot creates a file-backed snapshot of store. When restore is true,
// it first loads the previously saved metrics from path, so the caller only
// wires components and does not need to know about restoring state.
func NewSnapshot(path string, storeInterval time.Duration, store *MemStorage, restore bool, log *zap.Logger) *Snapshot {
	s := &Snapshot{
		path:          path,
		storeInterval: storeInterval,
		store:         store,
		log:           log,
	}
	if restore {
		s.restore()
	}
	return s
}

// restore loads the snapshot file into the store. A missing file is not an
// error: it simply means nothing has been saved yet. Any other load failure is
// logged and leaves the store untouched, so the server starts with empty
// metrics instead of failing.
func (s *Snapshot) restore() {
	metrics, err := loadSnapshotFromFile(s.path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		s.log.Error("failed to restore metrics snapshot", zap.String("path", s.path), zap.Error(err))
		return
	}
	s.store.Restore(metrics)
}

// Update stores the metric and synchronously writes a snapshot to disk. A
// failed disk write is only logged: the metric is already in memory, so the
// update itself succeeded and the next successful write will persist it.
func (s *Snapshot) Update(metric *models.Metrics) error {
	if err := s.store.Update(metric); err != nil {
		return err
	}

	if err := s.persist(); err != nil {
		s.log.Error("failed to persist metrics snapshot", zap.String("path", s.path), zap.Error(err))
	}
	return nil
}

// Run saves the store every storeInterval until ctx is cancelled, then
// performs one final save so nothing written since the last tick is lost.
// It blocks, so start it in its own goroutine. A non-positive interval means
// synchronous mode (see Update), so Run returns immediately.
func (s *Snapshot) Run(ctx context.Context) {
	if s.storeInterval <= 0 {
		s.log.Warn("periodic snapshot disabled: non-positive store interval", zap.Duration("interval", s.storeInterval))
		return
	}

	storeTicker := time.NewTicker(s.storeInterval)
	defer storeTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			if err := s.persist(); err != nil {
				s.log.Error("failed to persist final metrics snapshot", zap.String("path", s.path), zap.Error(err))
			}
			return
		case <-storeTicker.C:
			if err := s.persist(); err != nil {
				s.log.Error("failed to persist metrics snapshot", zap.String("path", s.path), zap.Error(err))
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

// snapshotFileMode keeps the permissions the snapshot had with os.WriteFile.
const snapshotFileMode = 0o644

// saveSnapshotToFile writes metrics atomically: the data goes to a temporary
// file next to path, which then replaces path with a single rename. A crash
// at any moment leaves either the old or the new file, never a partial one.
func saveSnapshotToFile(path string, metrics []models.Metrics) (err error) {
	data, err := easyjson.Marshal(models.MetricsList(metrics))
	if err != nil {
		return err
	}

	// the temp file must be in the same directory: rename is atomic
	// only within one file system
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = os.Remove(tmp.Name()) // do not leave garbage on failure
		}
	}()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	// flush to disk before rename, otherwise after a power loss the rename
	// may survive while the data does not
	if err = tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	// CreateTemp creates the file with 0600; keep the previous permissions
	if err = os.Chmod(tmp.Name(), snapshotFileMode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
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
