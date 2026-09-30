package box

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func dockerConfig(t *testing.T) Config {
	t.Helper()

	c := testConfig(t)
	d := DefaultDocker()
	c.Docker = &d
	c.Dirs = []string{c.Workspace + "-extra"}

	return c
}

func TestDockerNames(t *testing.T) {
	assert.Equal(t, "sandboxed_agent-foo-1-dind", SidecarName("sandboxed_agent-foo-1"))
	assert.Equal(t, "sandboxed_agent-dind-foo-1", DindVolume("sandboxed_agent-foo-1"))
	assert.Equal(t, "sandboxed_agent-foo-1-sock", SockVolume("sandboxed_agent-foo-1"))
	assert.Equal(t, Docker{DindImage: "docker:dind"}, DefaultDocker())
}

func TestSidecarArgv(t *testing.T) {
	d := DefaultDocker()
	c := Config{Name: "sandboxed_agent-foo-1", Workspace: "/w", Dirs: []string{"/d"}, GID: 1001, Docker: &d}

	want := []string{
		"docker", "run", "-d", "--name", "sandboxed_agent-foo-1-dind",
		"--label", "sandboxed_agent.sidecar=sandboxed_agent-foo-1", "--label", "sandboxed_agent.mounts=" + SidecarHash(c),
		"-e", "SANDBOXED_AGENT_STALE=45",
		"--privileged", "--init", "--memory", "8g", "--tmpfs", "/tmp:exec,mode=1777",
		"--mount", "type=volume,src=sandboxed_agent-dind-foo-1,dst=/var/lib/docker",
		"--mount", "type=volume,src=sandboxed_agent-foo-1-sock,dst=/run/sandboxed_agent-docker",
		"--mount", "type=bind,src=/w,dst=/w",
		"--mount", "type=bind,src=/d,dst=/d",
		"docker:dind", "sh", "-c", sidecarScript, "sidecar", "dockerd", "--host=unix:///var/run/docker.sock",
		"--host=unix:///run/sandboxed_agent-docker/docker.sock", "--group=1001",
	}
	assert.Equal(t, want, SidecarArgv(c))

	other := c
	other.Dirs = nil
	assert.NotEqual(t, SidecarHash(c), SidecarHash(other))

	other = c
	other.Idle = 6e9
	assert.Contains(t, SidecarArgv(other), "SANDBOXED_AGENT_STALE=21")
	assert.Equal(t, SidecarHash(c), SidecarHash(other), "the heartbeat limit only shapes new sidecars")
}

func TestHeartbeat(t *testing.T) {
	c := Config{Name: "b", Workspace: "/w"}
	assert.NotContains(t, CreateArgv(c), Heartbeat, "no sidecar, no heartbeat")

	d := DefaultDocker()
	c.Docker = &d
	argv := CreateArgv(c)
	assert.Equal(t, Heartbeat, argv[len(argv)-1], "sandboxed_agent-idle's third argument")
	assert.Contains(t, sidecarScript, "beat="+Heartbeat)
}

func TestSockRemoveArgv(t *testing.T) {
	assert.Equal(t, []string{"docker", "volume", "rm", "-f", "a-sock", "b-sock"}, SockRemoveArgv("a", "b"))
}

func TestWakeArgv(t *testing.T) {
	assert.Equal(t, StartArgv("b"), wakeArgv("b", "exited", false))
	assert.Equal(t, StartArgv("b"), wakeArgv("b", "exited", true))
	assert.Equal(t, []string{"docker", "restart", "b"}, wakeArgv("b", "running", true))
	assert.Nil(t, wakeArgv("b", "running", false))
}

func TestUpWithDockerCreatesAll(t *testing.T) {
	c := dockerConfig(t)
	f := &fake{out: map[string][]string{
		"docker ps":  {"", ""}, // sidecar, box absent
		"docker run": {"s", "cid\n"},
	}}

	id, err := Manager{Runner: f}.Up(context.Background(), c, SameMounts)

	require.NoError(t, err)
	assert.Equal(t, "cid", id)
	assert.Equal(t, [][]string{
		FindArgv(SidecarName(c.Name)), SidecarArgv(c),
		{"docker", "exec", SidecarName(c.Name), "sh", "-c", dockerReadyScript},
		FindArgv(c.Name), CreateArgv(c),
	}, f.calls)
	assert.DirExists(t, c.Dirs[0])
}

func TestUpWithDockerReuses(t *testing.T) {
	c := dockerConfig(t)
	box, side := "cid running "+MountHash(c), "sid running "+SidecarHash(c)

	tests := []struct {
		name  string
		found string
		box   string
		verbs []string
	}{
		{"all running", side, box, []string{"ps", "exec", "ps"}},
		{
			"stopped sidecar restarts the box", "sid exited " + SidecarHash(c), box,
			[]string{"ps", "start", "exec", "ps", "restart"},
		},
		{
			"both stopped", "sid exited " + SidecarHash(c), "cid exited " + MountHash(c),
			[]string{"ps", "start", "exec", "ps", "start"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{out: map[string][]string{"docker ps": {tc.found, tc.box}}}

			id, err := Manager{Runner: f}.Up(context.Background(), c, SameMounts)

			require.NoError(t, err)
			assert.Equal(t, "cid", id)
			assert.Equal(t, tc.verbs, f.verbs())
		})
	}
}

func TestUpWithDockerSidecarMounts(t *testing.T) {
	c := dockerConfig(t)
	out := func() map[string][]string {
		return map[string][]string{"docker ps": {"sid running other", "cid running other"}}
	}

	_, err := Manager{Runner: &fake{out: out()}}.Up(context.Background(), c, SameMounts)
	require.ErrorIs(t, err, ErrMountsChanged)
	assert.Contains(t, err.Error(), SidecarName(c.Name))

	_, err = Manager{Runner: &fake{out: out()}}.Up(context.Background(), c, AnyMounts)
	require.NoError(t, err)
}

func TestUpWithDockerRaces(t *testing.T) {
	c := dockerConfig(t)
	f := &fake{
		out: map[string][]string{
			"docker ps": {"", "sid running " + SidecarHash(c), "cid running " + MountHash(c)},
		},
		failAt: map[string][]int{"docker run": {1}},
	}

	_, err := Manager{Runner: f}.Up(context.Background(), c, SameMounts)

	require.NoError(t, err, "another agent created the sidecar first")
	assert.Equal(t, []string{"ps", "run", "ps", "exec", "ps"}, f.verbs())
}

func TestWaitDockerRetries(t *testing.T) {
	f := &fake{failAt: map[string][]int{"docker exec": {1, 2}}}
	require.NoError(t, Manager{Runner: f}.waitDocker(context.Background(), "s"))
	assert.Len(t, f.calls, 3)

	f = &fake{failAt: map[string][]int{"docker exec": {1, 2, 3}}}
	require.ErrorIs(t, Manager{Runner: f}.waitDocker(context.Background(), "s"), errDocker)
	assert.Len(t, f.calls, 3)
}

func TestUpWithDockerErrors(t *testing.T) {
	c := dockerConfig(t)

	tests := []struct {
		name string
		f    *fake
	}{
		{"sidecar find", &fake{fail: map[string]error{"docker ps": errDocker}}},
		{"sidecar create", &fake{fail: map[string]error{"docker run": errDocker}}},
		{"dockerd not ready", &fake{
			out:  map[string][]string{"docker ps": {"s running"}},
			fail: map[string]error{"docker exec": errDocker},
		}},
		{"box restart", &fake{
			out:  map[string][]string{"docker ps": {"s exited " + SidecarHash(c), "cid running " + MountHash(c)}},
			fail: map[string]error{"docker restart": errDocker},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Manager{Runner: tc.f}.Up(context.Background(), c, AnyMounts)

			require.ErrorIs(t, err, errDocker)
		})
	}
}

func TestDown(t *testing.T) {
	f := &fake{}

	require.NoError(t, Manager{Runner: f}.Down(context.Background(), "a", "b"))
	assert.Equal(t, [][]string{
		RemoveArgv("a", "a-dind", "b", "b-dind"),
		SockRemoveArgv("a", "b"),
	}, f.calls)

	f = &fake{fail: map[string]error{"docker rm": errDocker}}
	require.ErrorIs(t, Manager{Runner: f}.Down(context.Background(), "a"), errDocker)
	assert.Len(t, f.calls, 1)
}
