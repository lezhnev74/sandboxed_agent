//go:build integration

package image

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// itestTag keeps the test from replacing the user's sandboxed_agent:latest; the
// layers are shared, so a warm cache makes the build cheap. It follows the
// embedded assets, so the test never rebuilds another tag.
var itestTag = "sandboxed_agent-itest:" + AssetsHash()

func sh(t *testing.T, argv ...string) string {
	t.Helper()

	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	require.NoError(t, err, "%s: %s", strings.Join(argv, " "), out)

	return strings.TrimSpace(string(out))
}

func atoi(t *testing.T, s string) int {
	t.Helper()

	var n int
	for _, r := range s {
		require.True(t, r >= '0' && r <= '9', "not a number: %q", s)
		n = n*10 + int(r-'0')
	}

	return n
}

func TestImageIntegration(t *testing.T) {
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker is not available")
	}

	uid, gid := sh(t, "id", "-u"), sh(t, "id", "-g")
	ctx := context.Background()

	var log bytes.Buffer
	v, err := Builder{Runner: Exec{}, Log: &log}.Build(ctx, Options{Tag: itestTag, UID: atoi(t, uid), GID: atoi(t, gid)})
	require.NoError(t, err, log.String())
	assert.NotEmpty(t, v.Claude)
	assert.NotEmpty(t, v.OpenCode)

	t.Run("labels carry the versions", func(t *testing.T) {
		labels := sh(t, "docker", "image", "inspect", "--format",
			"{{index .Config.Labels \""+LabelClaude+"\"}} {{index .Config.Labels \""+LabelOpenCode+"\"}}", itestTag)
		assert.Equal(t, v.Claude+" "+v.OpenCode, labels)
	})

	t.Run("mise is on PATH", func(t *testing.T) {
		assert.Contains(t, sh(t, "docker", "run", "--rm", "-u", uid, itestTag, "mise", "--version"), "linux")
	})

	t.Run("harnesses run as the host uid", func(t *testing.T) {
		assert.Contains(t, sh(t, "docker", "run", "--rm", "-u", uid, itestTag, "claude", "--version"), v.Claude)
		assert.Contains(t, sh(t, "docker", "run", "--rm", "-u", uid, itestTag, "opencode", "--version"), v.OpenCode)
	})

	t.Run("bind-mount files are host-owned", func(t *testing.T) {
		dir := t.TempDir()
		sh(t, "docker", "run", "--rm", "-v", dir+":/w", itestTag, "sh", "-c", "touch /w/f && sudo -n true")

		fi, err := os.Stat(filepath.Join(dir, "f"))
		require.NoError(t, err)
		owner := sh(t, "stat", "-c", "%u:%g", filepath.Join(dir, "f"))
		assert.Equal(t, uid+":"+gid, owner, fi.Name())
	})

	t.Run("sandboxed_agent-exec leads its own group and records it", func(t *testing.T) {
		out := sh(t, "docker", "run", "--rm", itestTag, "sh", "-c",
			`sandboxed_agent-exec /tmp/pgid sh -c 'ps -o pgid= $$' && cat /tmp/pgid`)
		lines := strings.Fields(out)
		require.Len(t, lines, 2, out)
		assert.Equal(t, lines[0], lines[1])
	})
}
