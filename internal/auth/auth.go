// Package auth moves the stored Claude login in and out of a run's config
// dir. OAuth refresh tokens rotate, so copy-back keeps only the newest token
// and both directions hold an flock on the store's .lock.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// CredentialsName is the credential file, in the store and in a config dir.
const CredentialsName = ".credentials.json"

// ErrNoLogin means there is no stored login yet.
var ErrNoLogin = errors.New("no stored Claude login: run `sandboxed_agent login claude`")

// ErrLoginFailed means a login left no usable credential behind.
var ErrLoginFailed = errors.New("the login left no Claude credential")

// warnWithin is how close to expiry a token gets a warning.
const warnWithin = time.Hour

const (
	dirMode  = 0o700
	fileMode = 0o600
)

// Store is the host dir holding the Claude login (claude-auth/).
type Store struct{ Dir string }

// File is the stored credential.
func (s Store) File() string { return filepath.Join(s.Dir, CredentialsName) }

func (s Store) lockFile() string { return filepath.Join(s.Dir, ".lock") }

// CopyIn copies the stored credential into cfgDir and returns it.
func (s Store) CopyIn(cfgDir string) ([]byte, error) {
	var data []byte

	err := s.locked(func() error {
		var err error
		if data, err = os.ReadFile(s.File()); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrNoLogin
			}

			return fmt.Errorf("auth: %w", err)
		}

		return writeAtomic(filepath.Join(cfgDir, CredentialsName), data)
	})

	return data, err
}

// CopyBack stores cfgDir's credential if it expires later than the stored
// one. It reports whether it did.
func (s Store) CopyBack(cfgDir string) (bool, error) {
	run, err := os.ReadFile(filepath.Join(cfgDir, CredentialsName))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}

	if err != nil {
		return false, fmt.Errorf("auth: %w", err)
	}

	copied := false
	err = s.locked(func() error {
		stored, rerr := os.ReadFile(s.File())
		if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			return fmt.Errorf("auth: %w", rerr)
		}

		if copied = Newer(run, stored); !copied {
			return nil
		}

		return writeAtomic(s.File(), run)
	})

	return copied, err
}

// Put stores the credential a fresh login wrote to file, replacing the
// stored one whatever its expiry.
func (s Store) Put(file string) error {
	data, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w (%s is missing)", ErrLoginFailed, file)
	}

	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	if _, ok := ExpiresAt(data); !ok {
		return fmt.Errorf("%w (%s has no OAuth token)", ErrLoginFailed, file)
	}

	return s.locked(func() error { return writeAtomic(s.File(), data) })
}

// locked runs fn under an exclusive flock on the store's .lock.
func (s Store) locked(fn func() error) error {
	if err := os.MkdirAll(s.Dir, dirMode); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	f, err := os.OpenFile(s.lockFile(), os.O_CREATE|os.O_RDWR, fileMode)
	if err != nil {
		return fmt.Errorf("auth lock: %w", err)
	}
	defer f.Close()

	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return fmt.Errorf("auth lock: %w", err)
	}

	return fn()
}

// writeAtomic writes data via a temp file and rename, so a reader never
// sees a half-written credential.
func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	tmp, err := os.CreateTemp(dir, ".credentials-*")
	if err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	_, werr := tmp.Write(data)
	cerr := tmp.Close()

	if err := errors.Join(werr, cerr); err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("auth: %w", err)
	}

	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("auth: %w", err)
	}

	return nil
}

// ExpiresAt reads claudeAiOauth.expiresAt (unix ms).
func ExpiresAt(data []byte) (time.Time, bool) {
	var c struct {
		OAuth struct {
			ExpiresAt int64 `json:"expiresAt"`
		} `json:"claudeAiOauth"`
	}

	if json.Unmarshal(data, &c) != nil || c.OAuth.ExpiresAt == 0 {
		return time.Time{}, false
	}

	return time.UnixMilli(c.OAuth.ExpiresAt), true
}

// Newer reports whether run should replace stored: run is readable and
// expires later (an unreadable or missing stored credential always loses).
func Newer(run, stored []byte) bool {
	r, ok := ExpiresAt(run)
	if !ok {
		return false
	}

	s, ok := ExpiresAt(stored)

	return !ok || r.After(s)
}

// Warning is a line to show when the credential is expired or expires
// within the hour, else "".
func Warning(data []byte, now time.Time) string {
	at, ok := ExpiresAt(data)
	if !ok {
		return ""
	}

	left := at.Sub(now)
	if left <= 0 {
		return "warning: the stored Claude access token has expired; the run refreshes it if the refresh token still works"
	}

	if left < warnWithin {
		return fmt.Sprintf("warning: the stored Claude access token expires in %d min", int(left.Minutes()))
	}

	return ""
}
