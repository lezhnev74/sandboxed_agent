// Package runs owns the workspace lock that allows one run per workspace,
// and finds the cfg dirs crashed runs left behind.
package runs

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/lezhnev74/sandboxed_agent/internal/paths"
)

// ErrBusy means another run holds the workspace lock.
var ErrBusy = errors.New("another run is using this workspace; wait for it")

const (
	dirMode  = 0o750
	fileMode = 0o600
)

// WorkspaceLock is a held workspace lock.
type WorkspaceLock struct{ f *os.File }

// Lock takes the lock at path without waiting: a second writer fails fast.
func Lock(path string) (*WorkspaceLock, error) {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return nil, fmt.Errorf("workspace lock: %w", err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return nil, fmt.Errorf("workspace lock: %w", err)
	}

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()

		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, ErrBusy
		}

		return nil, fmt.Errorf("workspace lock: %w", err)
	}

	return &WorkspaceLock{f: f}, nil
}

// Release drops the lock.
func (l *WorkspaceLock) Release() error {
	if err := l.f.Close(); err != nil {
		return fmt.Errorf("workspace lock: %w", err)
	}

	return nil
}

// StaleCfgDirs lists the run cfg dirs in a repo's cfg dir that were last
// modified at least older before now. A run removes its own when it ends,
// so these are left by crashed runs; call it under the workspace lock.
func StaleCfgDirs(cfgDir string, now time.Time, older time.Duration) ([]string, error) {
	entries, err := os.ReadDir(cfgDir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, fmt.Errorf("cfg dir: %w", err)
	}

	var out []string

	for _, e := range entries {
		if staleRun(e, now, older) {
			out = append(out, filepath.Join(cfgDir, e.Name()))
		}
	}

	return out, nil
}

// staleRun: e is a run's cfg dir, last modified at least older before now.
func staleRun(e os.DirEntry, now time.Time, older time.Duration) bool {
	if !e.IsDir() || !paths.IsRunID(e.Name()) {
		return false
	}

	fi, err := e.Info()

	return err == nil && now.Sub(fi.ModTime()) >= older
}
