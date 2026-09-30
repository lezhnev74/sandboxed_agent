package paths_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/paths"
)

func TestNewUsesXDGDirsWhenSet(t *testing.T) {
	d := paths.New(paths.Env{Home: "/h", DataHome: "/data", ConfigHome: "/conf"})

	assert.Equal(t, "/data/sandboxed_agent", d.Data)
	assert.Equal(t, "/conf/sandboxed_agent", d.Config)
}

func TestNewFallsBackToHomeDefaults(t *testing.T) {
	d := paths.New(paths.Env{Home: "/h"})

	assert.Equal(t, "/h/.local/share/sandboxed_agent", d.Data)
	assert.Equal(t, "/h/.config/sandboxed_agent", d.Config)
}

func TestNewIgnoresRelativeXDGDirs(t *testing.T) {
	// The XDG spec says relative values are invalid and must be ignored.
	d := paths.New(paths.Env{Home: "/h", DataHome: "rel", ConfigHome: "rel"})

	assert.Equal(t, "/h/.local/share/sandboxed_agent", d.Data)
	assert.Equal(t, "/h/.config/sandboxed_agent", d.Config)
}

func TestDirsLayout(t *testing.T) {
	d := paths.New(paths.Env{Home: "/h"})
	const key = "foo-12345678"

	tests := []struct {
		name, got, want string
	}{
		{"lock", d.Lock(key), "/h/.local/share/sandboxed_agent/locks/foo-12345678.lock"},
		{"cfg root", d.CfgRoot(), "/h/.local/share/sandboxed_agent/cfg"},
		{"cfg", d.Cfg(key), "/h/.local/share/sandboxed_agent/cfg/foo-12345678"},
		{"run cfg", d.RunCfg(key, "r1"), "/h/.local/share/sandboxed_agent/cfg/foo-12345678/r1"},
		{"box home", d.BoxHome(), "/h/.local/share/sandboxed_agent/home"},
		{"claude auth", d.ClaudeAuth(), "/h/.local/share/sandboxed_agent/claude-auth"},
		{"opencode", d.OpenCode(), "/h/.local/share/sandboxed_agent/opencode"},
		{"env file", d.EnvFile(), "/h/.config/sandboxed_agent/env"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, tc.got)
		})
	}
}

func TestRepoKeyIsStableAndPathDependent(t *testing.T) {
	a := paths.RepoKey("/home/u/Code/foo")

	assert.Equal(t, a, paths.RepoKey("/home/u/Code/foo"))
	assert.Equal(t, a, paths.RepoKey("/home/u/Code/foo/"), "trailing slash is the same dir")
	assert.NotEqual(t, a, paths.RepoKey("/home/u/Other/foo"))
	assert.Regexp(t, `^foo-[0-9a-f]{8}$`, a)
}

func TestRepoKeyPinnedValue(t *testing.T) {
	// Pinned: changing the key orphans every existing box and run dir.
	assert.Equal(t, "foo-eb4bfded", paths.RepoKey("/home/u/Code/foo"))
}

func TestRepoKeySanitizesBasename(t *testing.T) {
	tests := []struct {
		path, prefix string
	}{
		{"/home/u/my repo", "my-repo-"},
		{"/home/u/ü.x_y", "-.x_y-"},
		{"/", "root-"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			assert.Regexp(t, `^`+regexp.QuoteMeta(tc.prefix)+`[0-9a-f]{8}$`, paths.RepoKey(tc.path))
		})
	}
}

func TestBoxNames(t *testing.T) {
	assert.Equal(t, "sandboxed_agent-foo-12345678", paths.BoxName("foo-12345678"))
	assert.Equal(t, "sandboxed_agent-foo-12345678-dind", paths.SidecarName("foo-12345678"))
	assert.Equal(t, "sandboxed_agent-foo-12345678-20260929-120000-abcd", paths.FreshBoxName("foo-12345678", "20260929-120000-abcd"))
}

func TestNewRunID(t *testing.T) {
	at := time.Date(2026, 9, 29, 7, 5, 3, 0, time.UTC)

	id, err := paths.NewRunID(at, bytes.NewReader([]byte{0xab, 0x0c, 0xff}))

	require.NoError(t, err)
	assert.Equal(t, "20260929-070503-ab0c", id)
}

func TestNewRunIDUsesUTC(t *testing.T) {
	at := time.Date(2026, 9, 29, 9, 5, 3, 0, time.FixedZone("x", 2*3600))

	id, err := paths.NewRunID(at, bytes.NewReader([]byte{0, 1}))

	require.NoError(t, err)
	assert.Equal(t, "20260929-070503-0001", id)
}

func TestNewRunIDFailsOnShortRandom(t *testing.T) {
	_, err := paths.NewRunID(time.Time{}, bytes.NewReader([]byte{1}))

	require.Error(t, err)
}

func TestEnsureDirCreatesNestedDirs(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "a", "b")

	require.NoError(t, paths.EnsureDir(dir))
	require.NoError(t, paths.EnsureDir(dir), "idempotent")

	info, err := os.Stat(dir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())
}

func TestEnsureDirFailsOnFile(t *testing.T) {
	f := filepath.Join(t.TempDir(), "f")
	require.NoError(t, os.WriteFile(f, nil, 0o600))

	require.Error(t, paths.EnsureDir(f))
}

func TestIsRunID(t *testing.T) {
	assert.True(t, paths.IsRunID("20260929-070503-ab0c"))

	for _, s := range []string{"", "login", "tui-20260929-070503-ab0c", "20260929-070503-ab0", "20260929-070503+ab0c",
		"20261329-070503-ab0c", "20260929-070503-zz0c", "20260929-070503-ab0c1"} {
		assert.False(t, paths.IsRunID(s), s)
	}
}
