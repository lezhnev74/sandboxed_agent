//go:build integration

package box

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/clock"
	"github.com/lezhnev74/sandboxed_agent/internal/gitx"
	"github.com/lezhnev74/sandboxed_agent/internal/image"
)

// itestTag is shared with the image itest, so the layers are warm. It
// follows the embedded assets, so the test never rebuilds another tag.
var itestTag = "sandboxed_agent-itest:" + image.AssetsHash()

func sh(t *testing.T, dir string, argv ...string) string {
	t.Helper()

	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s: %s", strings.Join(argv, " "), out)

	return strings.TrimSpace(string(out))
}

func inBox(t *testing.T, m Manager, name, script string) (string, error) {
	t.Helper()

	var out bytes.Buffer
	err := m.Exec(context.Background(), name, ExecOpts{Stdout: &out, Stderr: &out}, []string{"sh", "-c", script})

	return strings.TrimSpace(out.String()), err
}

func TestBoxIntegration(t *testing.T) {
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker is not available")
	}

	ctx := context.Background()
	uid, gid := os.Getuid(), os.Getgid()

	var log bytes.Buffer
	require.NoError(t, image.Builder{Runner: image.Exec{}, Log: &log}.
		Ensure(ctx, image.Options{Tag: itestTag, UID: uid, GID: gid}), log.String())

	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	require.NoError(t, os.MkdirAll(filepath.Join(ws, ".claude"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(ws, ".claude", "settings.json"), []byte("{}"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "CLAUDE.md"), []byte("secret\n"), 0o600))
	sh(t, ws, "git", "init", "-q")

	c := Config{
		Name: fmt.Sprintf("sandboxed_agent-itest-%d", os.Getpid()), Image: itestTag, Workspace: ws, UID: uid, GID: gid,
		CfgDir: filepath.Join(root, "cfg"), Home: filepath.Join(root, "home"), OpenCodeDir: filepath.Join(root, "oc"),
		GitConfig: filepath.Join(root, "cfg", "gitconfig"),
	}
	// Volumes of this test only, removed by name after the box.
	for _, v := range Volumes() {
		c.Volumes = append(c.Volumes, Volume{Name: c.Name + strings.TrimPrefix(v.Name, "sandboxed_agent"), Target: v.Target})
	}
	t.Cleanup(func() {
		for _, v := range c.Volumes {
			_ = exec.Command("docker", "volume", "rm", v.Name).Run()
		}
	})

	m := Manager{Runner: Exec{}, Log: &log}
	t.Cleanup(func() { _ = m.Remove(context.Background(), c.Name) })

	// What cmd/sandboxed_agent does before Up: the git config.
	require.NoError(t, os.MkdirAll(c.CfgDir, 0o750))
	require.NoError(t, gitx.WriteConfig(c.GitConfig, gitx.Identity{Name: "Host User", Email: "host@example.com"}))

	id, err := m.Up(ctx, c, SameMounts)
	require.NoError(t, err, log.String())

	t.Run("reuse keeps the container", func(t *testing.T) {
		again, err := m.Up(ctx, c, SameMounts)
		require.NoError(t, err)
		assert.Equal(t, id, again)
	})

	t.Run("repo agent files are visible, as on the host", func(t *testing.T) {
		out, err := inBox(t, m, c.Name, "cat CLAUDE.md; ls -A .claude")
		require.NoError(t, err, out)
		assert.Equal(t, "secret\nsettings.json", out)
	})

	t.Run("commits carry the host identity and are host-owned", func(t *testing.T) {
		out, err := inBox(t, m, c.Name, "touch f && git add -A && git commit -qm m && git status --porcelain --untracked-files=all")
		require.NoError(t, err, out)
		assert.Empty(t, out)

		assert.Equal(t, "Host User <host@example.com>", sh(t, ws, "git", "log", "-1", "--format=%an <%ae>"))
		assert.Equal(t, ".claude/settings.json\nCLAUDE.md\nf", sh(t, ws, "git", "ls-files"))
		assert.Equal(t, strconv.Itoa(uid)+":"+strconv.Itoa(gid), sh(t, ws, "stat", "-c", "%u:%g", "f"))
	})

	t.Run("other mounts are refused unless any will do", func(t *testing.T) {
		other := c
		other.Dirs = []string{filepath.Join(root, "extra")}

		_, err := m.Up(ctx, other, SameMounts)
		require.ErrorIs(t, err, ErrMountsChanged)

		got, err := m.Up(ctx, other, AnyMounts)
		require.NoError(t, err)
		assert.Equal(t, id, got)
	})

	t.Run("a stopped box is started", func(t *testing.T) {
		sh(t, root, "docker", "stop", "-t", "1", c.Name)

		got, err := m.Up(ctx, c, SameMounts)
		require.NoError(t, err)
		assert.Equal(t, id, got)

		out, err := inBox(t, m, c.Name, "echo up")
		require.NoError(t, err)
		assert.Equal(t, "up", out)
	})

	t.Run("exit codes come through", func(t *testing.T) {
		_, err := inBox(t, m, c.Name, "exit 3")
		var ee *ExitError
		require.ErrorAs(t, err, &ee)
		assert.Equal(t, 3, ee.Code)
	})

	t.Run("KillGroup leaves no harness process", func(t *testing.T) {
		for name, script := range map[string]string{
			"obeys TERM":   "sleep 1000 & sleep 1000",
			"ignores TERM": "trap '' TERM; sleep 1000 & sleep 1000",
		} {
			t.Run(name, func(t *testing.T) {
				pgid := filepath.Join(c.CfgDir, "iter.pgid")
				_ = os.Remove(pgid)

				done := make(chan error, 1)
				go func() {
					done <- m.Exec(ctx, c.Name, ExecOpts{}, []string{"sandboxed_agent-exec", pgid, "sh", "-c", script})
				}()

				require.Eventually(t, func() bool { _, ok, _ := ReadPGID(pgid); return ok }, 10*time.Second, 50*time.Millisecond)
				id, _, err := ReadPGID(pgid)
				require.NoError(t, err)

				require.NoError(t, m.KillGroup(ctx, c.Name, pgid, clock.Real{}, 2*time.Second))
				<-done

				out, err := inBox(t, m, c.Name, "ps -eo pgid=,args=")
				require.NoError(t, err)
				for _, line := range strings.Split(out, "\n") {
					assert.NotEqual(t, strconv.Itoa(id), strings.Fields(line)[0], "left over: %s", line)
				}
				assert.NotContains(t, out, "sleep 1000")
			})
		}
	})

	t.Run("volumes belong to the box user", func(t *testing.T) {
		for _, v := range c.Volumes {
			out, err := inBox(t, m, c.Name, "stat -c %u:%g "+v.Target+" && touch "+v.Target+"/w")
			require.NoError(t, err, out)
			assert.Equal(t, strconv.Itoa(uid)+":"+strconv.Itoa(gid), out, v.Name)
		}
	})

	t.Run("a go.mod toolchain installs cold, then warm in a new box", func(t *testing.T) {
		require.NoError(t, os.WriteFile(filepath.Join(ws, "go.mod"), []byte("module x\n\ngo 1.24\n\ntoolchain go1.24.2\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(ws, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o600))

		build := func() time.Duration {
			start := time.Now()
			out, err := inBox(t, m, c.Name, "mise install && go version && go build -o /tmp/x .")
			require.NoError(t, err, out)
			assert.Contains(t, out, "go1.24.2")

			return time.Since(start)
		}

		cold := build()

		require.NoError(t, m.Remove(ctx, c.Name))
		_, err := m.Up(ctx, c, SameMounts)
		require.NoError(t, err, log.String())

		warm := build()
		t.Logf("cold %s, warm %s", cold, warm)
		assert.Less(t, warm, cold/2)
	})

	t.Run("remove", func(t *testing.T) {
		require.NoError(t, m.Remove(ctx, c.Name))
		assert.Empty(t, sh(t, root, FindArgv(c.Name)...))
	})
}

func TestDockerSidecarIntegration(t *testing.T) {
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker is not available")
	}

	ctx := context.Background()
	uid, gid := os.Getuid(), os.Getgid()

	var log bytes.Buffer
	require.NoError(t, image.Builder{Runner: image.Exec{}, Log: &log}.
		Ensure(ctx, image.Options{Tag: itestTag, UID: uid, GID: gid}), log.String())

	root := t.TempDir()
	ws := filepath.Join(root, "ws")
	require.NoError(t, os.MkdirAll(ws, 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(ws, "f"), []byte("from the workspace\n"), 0o600))

	name := fmt.Sprintf("sandboxed_agent-itest-dind-%d", os.Getpid())
	// Everything this test creates has its own name and is removed by name.
	d := DefaultDocker()
	c := Config{
		Name: name, Image: itestTag, Workspace: ws, UID: uid, GID: gid,
		CfgDir: filepath.Join(root, "cfg"), Home: filepath.Join(root, "home"), OpenCodeDir: filepath.Join(root, "oc"),
		Docker: &d,
	}

	m := Manager{Runner: Exec{}, Log: &log}
	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", c.Name, SidecarName(c.Name)).Run()
		_ = exec.Command("docker", "volume", "rm", SockVolume(c.Name), DindVolume(c.Name)).Run()
	})

	_, err := m.Up(ctx, c, SameMounts)
	require.NoError(t, err, log.String())

	t.Run("the box talks to its own dockerd", func(t *testing.T) {
		out, err := inBox(t, m, c.Name, `echo "$DOCKER_HOST $TESTCONTAINERS_HOST_OVERRIDE" && docker info --format '{{.Name}}'`)
		require.NoError(t, err, out)
		assert.Contains(t, out, DockerHost+" localhost")
	})

	t.Run("published ports are on the box's localhost", func(t *testing.T) {
		out, err := inBox(t, m, c.Name, "docker run -d --name web -p 8080:80 nginx >/dev/null 2>&1 && "+
			"for i in $(seq 1 40); do curl -fsS localhost:8080 && exit 0; sleep 0.25; done; exit 1")
		require.NoError(t, err, out)
		assert.Contains(t, out, "Welcome to nginx")
	})

	t.Run("the workspace is at the same path in dind", func(t *testing.T) {
		out, err := inBox(t, m, c.Name, `docker run --rm -v "$PWD:$PWD" alpine cat "$PWD/f" 2>&1`)
		require.NoError(t, err, out)
		assert.Contains(t, out, "from the workspace")
	})

	t.Run("a compose postgres is on localhost:5432", func(t *testing.T) {
		compose := "services:\n  db:\n    image: postgres:17-alpine\n    restart: unless-stopped\n" +
			"    environment: {POSTGRES_PASSWORD: pw}\n    ports: ['5432:5432']\n" +
			"    healthcheck: {test: ['CMD', 'pg_isready', '-U', 'postgres'], interval: 1s, retries: 60}\n"
		require.NoError(t, os.WriteFile(filepath.Join(ws, "compose.yml"), []byte(compose), 0o600))

		out, err := inBox(t, m, c.Name, "docker compose -f compose.yml up -d --wait 2>&1 && "+
			"PGPASSWORD=pw psql -h localhost -U postgres -tAc 'select 40+2'")
		require.NoError(t, err, out)
		assert.Contains(t, out, "42")
	})

	t.Run("Own hands root-written files back", func(t *testing.T) {
		out, err := inBox(t, m, c.Name, `docker run --rm -v "$PWD:$PWD" alpine sh -c "mkdir -p $PWD/out && touch $PWD/out/r"`)
		require.NoError(t, err, out)
		assert.Equal(t, "0", sh(t, root, "stat", "-c", "%u", filepath.Join(ws, "out", "r")))

		require.NoError(t, m.Own(ctx, c.Name, ws, uid, gid), log.String())
		want := strconv.Itoa(uid) + ":" + strconv.Itoa(gid)
		assert.Equal(t, want, sh(t, root, "stat", "-c", "%u:%g", filepath.Join(ws, "out")))
		assert.Equal(t, want, sh(t, root, "stat", "-c", "%u:%g", filepath.Join(ws, "out", "r")))
	})

	t.Run("a restarted sidecar restarts the box", func(t *testing.T) {
		sh(t, root, "docker", "stop", "-t", "1", SidecarName(c.Name))

		_, err := m.Up(ctx, c, SameMounts)
		require.NoError(t, err, log.String())

		out, err := inBox(t, m, c.Name, "docker ps -a --format '{{.Names}}'")
		require.NoError(t, err, out)
		assert.Contains(t, out, "web", "inner containers survive in the dind volume")
	})

	t.Run("down removes box and sidecar, keeps the dind volume", func(t *testing.T) {
		log.Reset()
		require.NoError(t, m.Down(ctx, c.Name))
		assert.Empty(t, sh(t, root, FindArgv(c.Name)...))
		assert.Empty(t, sh(t, root, FindArgv(SidecarName(c.Name))...))
		assert.Equal(t, DindVolume(c.Name), sh(t, root, "docker", "volume", "inspect", "-f", "{{.Name}}", DindVolume(c.Name)))
	})
}

// TestPruneDindIntegration removes only the orphan dind volumes under a
// prefix unique to this test; the user's volumes never match it.
func TestPruneDindIntegration(t *testing.T) {
	if exec.Command("docker", "info").Run() != nil {
		t.Skip("docker is not available")
	}

	ctx := context.Background()
	key := fmt.Sprintf("itestgc-%d-", os.Getpid())
	orphan, kept := DindVolumePrefix+key+"a", DindVolumePrefix+key+"b"
	keeper := "sandboxed_agent-" + key + "b" // the box of kept

	var log bytes.Buffer
	require.NoError(t, image.Builder{Runner: image.Exec{}, Log: &log}.
		Ensure(ctx, image.Options{Tag: itestTag, UID: os.Getuid(), GID: os.Getgid()}), log.String())

	t.Cleanup(func() {
		_ = exec.Command("docker", "rm", "-f", keeper).Run()
		_ = exec.Command("docker", "volume", "rm", orphan, kept).Run()
	})

	for _, v := range []string{orphan, kept} {
		require.NoError(t, exec.Command("docker", "volume", "create", v).Run())
	}
	require.NoError(t, exec.Command("docker", "create", "--name", keeper, itestTag).Run())

	removed, err := Manager{Runner: Exec{}, Log: &log}.PruneDind(ctx, DindVolumePrefix+key)
	require.NoError(t, err, log.String())
	assert.Equal(t, []string{orphan}, removed)

	out, err := exec.Command("docker", "volume", "ls", "-q", "--filter", "name="+DindVolumePrefix+key).Output()
	require.NoError(t, err)
	assert.Equal(t, kept, strings.TrimSpace(string(out)))
}
