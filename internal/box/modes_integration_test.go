//go:build integration

package box

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/image"
)

// TestRunModesIntegration: the tool's own volume chown works; a --dir
// change is refused; Discard removes a --fresh box.
func TestRunModesIntegration(t *testing.T) {
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker is not available")
	}

	ctx := context.Background()
	uid, gid := os.Getuid(), os.Getgid()

	var log bytes.Buffer
	require.NoError(t, image.Builder{Runner: image.Exec{}, Log: &log}.
		Ensure(ctx, image.Options{Tag: itestTag, UID: uid, GID: gid}), log.String())

	root := t.TempDir()
	c := Config{
		Name: fmt.Sprintf("sandboxed_agent-itest-modes-%d", os.Getpid()), Image: itestTag,
		Workspace: filepath.Join(root, "ws"), UID: uid, GID: gid,
		CfgDir: filepath.Join(root, "cfg"), Home: filepath.Join(root, "home"), OpenCodeDir: filepath.Join(root, "oc"),
	}
	vol := c.Name + "-cache"
	c.Volumes = []Volume{{Name: vol, Target: HomeDir + "/.cache"}}

	m := Manager{Runner: Exec{}, Log: &log}
	t.Cleanup(func() {
		_ = m.Remove(context.Background(), c.Name)
		_ = exec.Command("docker", "volume", "rm", vol).Run()
	})

	id, err := m.Up(ctx, c, SameMounts)
	require.NoError(t, err, log.String())

	t.Run("the volumes are the user's", func(t *testing.T) {
		out, err := inBox(t, m, c.Name, "touch ~/.cache/ok && sudo -n true && echo ok")
		require.NoError(t, err, out)
		assert.Equal(t, "ok", out)
	})

	t.Run("a dir change is refused", func(t *testing.T) {
		d := c
		d.Dirs = []string{filepath.Join(root, "extra")}

		_, err := m.Up(ctx, d, SameMounts)
		require.ErrorIs(t, err, ErrMountsChanged)
		assert.Contains(t, err.Error(), "sandboxed_agent down")

		again, err := m.Up(ctx, c, SameMounts)
		require.NoError(t, err, log.String())
		assert.Equal(t, id, again, "the box stays")
	})

	t.Run("discard", func(t *testing.T) {
		require.NoError(t, m.Discard(ctx, c.Name), log.String())
		_, found, err := m.find(ctx, c.Name)
		require.NoError(t, err)
		assert.False(t, found)
	})
}
