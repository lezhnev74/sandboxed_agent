package runs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLockIsExclusiveAndFailsFast(t *testing.T) {
	path := filepath.Join(t.TempDir(), "locks", "repo-1.lock")

	first, err := Lock(path)
	require.NoError(t, err)

	_, err = Lock(path)
	require.ErrorIs(t, err, ErrBusy)

	require.NoError(t, first.Release())

	again, err := Lock(path)
	require.NoError(t, err, "released locks can be taken again")
	require.NoError(t, again.Release())
}

func TestLockBadPath(t *testing.T) {
	file := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(file, nil, 0o600))

	_, err := Lock(filepath.Join(file, "x.lock"))
	require.Error(t, err)
}

func TestStaleCfgDirs(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)

	mk := func(name string, age time.Duration) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.Mkdir(p, 0o750))
		require.NoError(t, os.Chtimes(p, now.Add(-age), now.Add(-age)))

		return p
	}

	old := mk("20260901-000000-ab0c", 48*time.Hour)
	mk("20260929-110000-0001", time.Hour)
	mk("login", 48*time.Hour)
	mk("tui-20260901-000000-ab0c", 48*time.Hour)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "20260901-000000-ffff"), nil, 0o600))

	got, err := StaleCfgDirs(dir, now, 24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, []string{old}, got, "only run dirs, only old ones")

	got, err = StaleCfgDirs(dir, now, 0)
	require.NoError(t, err)
	assert.Len(t, got, 2, "0 is every run dir")

	got, err = StaleCfgDirs(filepath.Join(dir, "missing"), now, 0)
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = StaleCfgDirs(filepath.Join(dir, "20260901-000000-ffff"), now, 0)
	require.Error(t, err)
}
