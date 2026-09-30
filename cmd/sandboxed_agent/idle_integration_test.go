//go:build integration

package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/box"
	"github.com/lezhnev74/sandboxed_agent/internal/clock"
	"github.com/lezhnev74/sandboxed_agent/internal/image"
)

func containerStatus(name string) string {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Status}}", name).Output()
	if err != nil {
		return "absent"
	}

	return strings.TrimSpace(string(out))
}

// TestIdleWatchdogIntegration: with a 6 s idle timeout, a box running a
// long exec keeps running; once it ends, the box stops by itself and its
// sidecar stops once the box's heartbeat goes stale; the next Up starts
// both again.
func TestIdleWatchdogIntegration(t *testing.T) {
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker is not available")
	}

	ctx := context.Background()
	uid, gid := os.Getuid(), os.Getgid()
	tag := "sandboxed_agent-itest:" + image.AssetsHash()
	log := &lockedBuffer{}

	require.NoError(t, image.Builder{Runner: image.Exec{}, Log: log}.
		Ensure(ctx, image.Options{Tag: tag, UID: uid, GID: gid}), log.String())

	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	require.NoError(t, os.MkdirAll(ws, 0o750))

	// Everything this test creates has its own name and is removed by name.
	name := fmt.Sprintf("sandboxed_agent-itest-idle-%d", os.Getpid())
	d := box.DefaultDocker()
	c := box.Config{
		Name: name, Image: tag, Workspace: ws, UID: uid, GID: gid,
		CfgDir: filepath.Join(root, "cfg"), Home: filepath.Join(root, "home"), OpenCodeDir: filepath.Join(root, "oc"),
		Docker: &d, Idle: 6 * time.Second,
	}

	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", c.Name, box.SidecarName(c.Name)).Run()
		_ = exec.Command("docker", "volume", "rm", box.SockVolume(c.Name), box.DindVolume(c.Name)).Run()
	})

	m := box.Manager{Runner: box.Exec{}, Log: log}

	up := func() {
		_, err := m.Up(ctx, c, box.SameMounts)
		require.NoError(t, err, log.String())
	}
	up()

	held := make(chan error, 1)

	go func() { held <- m.Exec(ctx, c.Name, box.ExecOpts{Stderr: log}, []string{"sleep", "20"}) }()

	require.NoError(t, clock.Real{}.Sleep(ctx, 15*time.Second))
	assert.Equal(t, "running", containerStatus(c.Name), "a running exec holds the box")
	assert.Equal(t, "running", containerStatus(box.SidecarName(c.Name)))

	require.NoError(t, <-held, log.String())

	waitFor(t, "the idle box stops", func() bool { return containerStatus(c.Name) == "exited" })
	waitFor(t, "its sidecar stops", func() bool { return containerStatus(box.SidecarName(c.Name)) == "exited" })

	up()
	assert.Equal(t, "running", containerStatus(c.Name), "restarted transparently")
	assert.Equal(t, "running", containerStatus(box.SidecarName(c.Name)))
	require.NoError(t, m.Exec(ctx, c.Name, box.ExecOpts{Stderr: log}, []string{"docker", "info"}), log.String())
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.String()
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()

	end := clock.Real{}.Now().Add(60 * time.Second)
	for !cond() {
		require.True(t, clock.Real{}.Now().Before(end), "timed out waiting: %s", what)
		require.NoError(t, clock.Real{}.Sleep(context.Background(), 100*time.Millisecond))
	}
}
