package driver

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/box"
)

func TestToolchainPlan(t *testing.T) {
	tests := []struct {
		name       string
		repo       []string
		bundle     bool
		install    bool
		fromBundle bool
	}{
		{"nothing", nil, false, false, false},
		{"go.mod", []string{"go.mod"}, false, true, false},
		{"mise.toml", []string{"mise.toml"}, false, true, false},
		{".mise.toml", []string{".mise.toml"}, false, true, false},
		{".tool-versions", []string{".tool-versions"}, false, true, false},
		{"bundle only", nil, true, true, true},
		{"the repo's file wins", []string{"go.mod"}, true, true, false},
		{"other files", []string{"package.json"}, false, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			have := map[string]bool{"/b/mise.toml": tc.bundle}
			for _, f := range tc.repo {
				have[filepath.Join("/ws", f)] = true
			}

			bundle := "/b/mise.toml"
			install, fromBundle := toolchainPlan("/ws", bundle, func(p string) bool { return have[p] })

			assert.Equal(t, tc.install, install)
			assert.Equal(t, tc.fromBundle, fromBundle)
		})
	}

	install, _ := toolchainPlan("/ws", "", func(p string) bool { return p == "" })
	assert.False(t, install, "no bundle file named")
}

func newToolchains(t *testing.T, b *fakeBox) (Toolchains, *bytes.Buffer) {
	t.Helper()

	var log bytes.Buffer

	return Toolchains{
		Box: b, BoxName: "box", Workspace: t.TempDir(), RunCfg: filepath.Join(t.TempDir(), "run1"), Log: &log,
	}, &log
}

func TestToolchainsInstallFromRepo(t *testing.T) {
	b := &fakeBox{}
	tc, log := newToolchains(t, b)
	require.NoError(t, os.WriteFile(filepath.Join(tc.Workspace, "go.mod"), []byte("module x\n"), 0o600))

	env, err := tc.Install(context.Background())

	require.NoError(t, err)
	assert.Empty(t, env)
	assert.Equal(t, "box", b.name)
	assert.Equal(t, []string{"mise", "install"}, b.argv)
	assert.Equal(t, tc.Workspace, b.opts.Dir)
	assert.Empty(t, b.opts.Env)
	assert.Same(t, log, b.opts.Stdout)
	assert.Same(t, log, b.opts.Stderr)
}

func TestToolchainsInstallFromBundle(t *testing.T) {
	b := &fakeBox{}
	tc, _ := newToolchains(t, b)
	tc.BundleMise = filepath.Join(t.TempDir(), "mise.toml")
	require.NoError(t, os.WriteFile(tc.BundleMise, []byte("[tools]\ngo = \"1.26\"\n"), 0o600))

	env, err := tc.Install(context.Background())

	require.NoError(t, err)

	copied := filepath.Join(tc.RunCfg, "mise.toml")
	want := []string{"MISE_GLOBAL_CONFIG_FILE=" + copied}
	assert.Equal(t, want, env)
	assert.Equal(t, want, b.opts.Env)
	assert.FileExists(t, copied)

	body, err := os.ReadFile(copied)
	require.NoError(t, err)
	assert.Contains(t, string(body), "go = ")
}

func TestToolchainsNothingToInstall(t *testing.T) {
	b := &fakeBox{}
	tc, _ := newToolchains(t, b)

	env, err := tc.Install(context.Background())

	require.NoError(t, err)
	assert.Empty(t, env)
	assert.Nil(t, b.argv, "no exec")
}

func TestToolchainsInstallErrors(t *testing.T) {
	b := &fakeBox{err: &box.ExitError{Code: 1}}
	tc, _ := newToolchains(t, b)
	require.NoError(t, os.WriteFile(filepath.Join(tc.Workspace, "mise.toml"), nil, 0o600))

	_, err := tc.Install(context.Background())

	require.ErrorContains(t, err, "mise install")

	require.NoError(t, os.Remove(filepath.Join(tc.Workspace, "mise.toml")))

	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	tc.BundleMise, tc.RunCfg = file, filepath.Join(file, "sub")

	_, err = tc.Install(context.Background())

	require.ErrorContains(t, err, "toolchains")
}
