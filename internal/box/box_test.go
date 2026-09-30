package box

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testConfig(t *testing.T) Config {
	t.Helper()

	root := t.TempDir()

	return Config{
		Name:        "sandboxed_agent-foo-12345678",
		Image:       "sandboxed_agent:latest",
		Workspace:   filepath.Join(root, "ws"),
		UID:         1000,
		GID:         1001,
		CfgDir:      filepath.Join(root, "cfg"),
		Home:        filepath.Join(root, "home"),
		OpenCodeDir: filepath.Join(root, "opencode"),
		GitConfig:   filepath.Join(root, "cfg", "gitconfig"),
	}
}

func TestCreateArgv(t *testing.T) {
	c := Config{
		Name: "sandboxed_agent-foo-1", Image: "img", Workspace: "/w", UID: 1000, GID: 1001,
		CfgDir: "/cfg", Home: "/h", OpenCodeDir: "/oc", Dirs: []string{"/d,1"},
		EnvFile: "/env", GitConfig: "/cfg/gitconfig", Docker: &Docker{},
		Volumes: []Volume{{Name: "sandboxed_agent-mise", Target: "/home/agent/.local/share/mise"}},
	}

	want := []string{
		"docker", "run", "-d", "--name", "sandboxed_agent-foo-1",
		"--label", "sandboxed_agent.workspace=/w", "--label", "sandboxed_agent.mounts=" + MountHash(c),
		"--entrypoint", "sandboxed_agent-idle",
		"--init", "--user", "1000:1001", "--pids-limit", "4096", "--cpus", "4", "--memory", "8g",
		"-w", "/w", "-e", "HOME=/home/agent", "--network", "container:sandboxed_agent-foo-1-dind",
		"-e", "DOCKER_HOST=unix:///run/sandboxed_agent-docker/docker.sock", "-e", "TESTCONTAINERS_HOST_OVERRIDE=localhost",
		"-e", "GIT_CONFIG_GLOBAL=/cfg/gitconfig", "--env-file", "/env",
		"--mount", "type=bind,src=/w,dst=/w",
		"--mount", "type=bind,src=/cfg,dst=/cfg",
		"--mount", "type=bind,src=/h,dst=/home/agent",
		"--mount", "type=bind,src=/oc,dst=/home/agent/.local/share/opencode",
		"--mount", `type=bind,"src=/d,1","dst=/d,1"`,
		"--mount", "type=volume,src=sandboxed_agent-mise,dst=/home/agent/.local/share/mise",
		"--mount", "type=volume,src=sandboxed_agent-foo-1-sock,dst=/run/sandboxed_agent-docker",
		"img", "3600", "10", "/run/sandboxed_agent-docker/box.alive",
	}
	assert.Equal(t, want, CreateArgv(c))
}

func TestIdleArgs(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want []string
	}{
		{0, []string{"3600", "10"}},
		{time.Hour, []string{"3600", "10"}},
		{90 * time.Second, []string{"90", "10"}},
		{6 * time.Second, []string{"6", "2"}},
		{2 * time.Second, []string{"2", "1"}},
		{500 * time.Millisecond, []string{"1", "1"}},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, IdleArgs(c.in, ""), c.in)
	}

	assert.Equal(t, []string{"3600", "10", "/run/x/box.alive"}, IdleArgs(0, "/run/x/box.alive"))
}

func TestStaleSecs(t *testing.T) {
	assert.Equal(t, 45, StaleSecs(0), "three default polls and a margin")
	assert.Equal(t, 21, StaleSecs(6*time.Second))
}

func TestIdleTimeoutIsNotAMount(t *testing.T) {
	a := Config{Name: "n", Image: "i", Workspace: "/w"}
	b := a
	b.Idle = time.Minute
	assert.Equal(t, MountHash(a), MountHash(b))
	assert.Equal(t, []string{"60", "10"}, CreateArgv(b)[len(CreateArgv(b))-2:])
}

func TestCreateArgvMinimal(t *testing.T) {
	argv := strings.Join(CreateArgv(Config{Name: "n", Image: "i", Workspace: "/w"}), " ")

	assert.NotContains(t, argv, "--network")
	assert.NotContains(t, argv, "--env-file")
	assert.NotContains(t, argv, "GIT_CONFIG_GLOBAL")
}

func TestMountHash(t *testing.T) {
	base := Config{Name: "n", Image: "i", Workspace: "/w", CfgDir: "/c", Home: "/h", OpenCodeDir: "/o"}
	h := MountHash(base)

	assert.Len(t, h, 16)
	assert.Equal(t, h, MountHash(base), "stable")

	img := base
	img.Image = "other"
	assert.Equal(t, h, MountHash(img), "the image is not a mount")

	for name, change := range map[string]func(*Config){
		"dir":    func(c *Config) { c.Dirs = []string{"/d"} },
		"env":    func(c *Config) { c.EnvFile = "/e" },
		"docker": func(c *Config) { c.Docker = &Docker{} },
		"uid":    func(c *Config) { c.UID = 7 },
		"volume": func(c *Config) { c.Volumes = []Volume{{Name: "v", Target: "/t"}} },
	} {
		c := base
		change(&c)
		assert.NotEqual(t, h, MountHash(c), name)
	}
}

func TestExecArgv(t *testing.T) {
	tests := []struct {
		name string
		o    ExecOpts
		want []string
	}{
		{"plain", ExecOpts{}, []string{"docker", "exec", "b", "ls", "-l"}},
		{
			"all", ExecOpts{Interactive: true, TTY: true, Dir: "/x", Env: []string{"A=1", "B=2"}},
			[]string{"docker", "exec", "-i", "-t", "-w", "/x", "-e", "A=1", "-e", "B=2", "b", "ls", "-l"},
		},
		{"root", ExecOpts{User: "0"}, []string{"docker", "exec", "-u", "0", "b", "ls", "-l"}},
		{"root", ExecOpts{User: "0"}, []string{"docker", "exec", "-u", "0", "b", "ls", "-l"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ExecArgv("b", tc.o, []string{"ls", "-l"}))
		})
	}
}

func TestSmallArgvs(t *testing.T) {
	assert.Equal(t, []string{
		"docker", "ps", "-a", "--no-trunc", "--filter", "name=^/b$",
		"--format", `{{.ID}} {{.State}} {{.Label "sandboxed_agent.mounts"}}`,
	}, FindArgv("b"))
	assert.Equal(t, []string{"docker", "start", "b"}, StartArgv("b"))
	assert.Equal(t, []string{"docker", "rm", "-f", "a", "b"}, RemoveArgv("a", "b"))
	assert.Equal(t, []string{"docker", "ps", "-a", "--filter", "label=sandboxed_agent.workspace", "--format", "{{.Names}}"},
		ListArgv())
}

func TestParseState(t *testing.T) {
	_, ok := parseState("")
	assert.False(t, ok)

	s, ok := parseState("abc running h1\n")
	assert.True(t, ok)
	assert.Equal(t, state{id: "abc", status: "running", hash: "h1"}, s)

	s, ok = parseState("abc exited \n")
	assert.True(t, ok)
	assert.Equal(t, state{id: "abc", status: "exited"}, s)
}

// fake answers docker commands by their first two argv words.
type fake struct {
	calls [][]string
	out   map[string][]string // queued stdout per "docker <verb>"
	fail  map[string]error
	// failAt fails the listed calls (1-based) of a verb with errDocker.
	failAt map[string][]int
	n      map[string]int
}

var errDocker = errors.New("docker failed")

func (f *fake) Run(_ context.Context, c Cmd) error {
	f.calls = append(f.calls, c.Argv)
	key := c.Argv[0] + " " + c.Argv[1]

	if err := f.fail[key]; err != nil {
		return err
	}

	if f.n == nil {
		f.n = map[string]int{}
	}

	f.n[key]++
	if slices.Contains(f.failAt[key], f.n[key]) {
		return errDocker
	}

	if q := f.out[key]; len(q) > 0 {
		f.out[key] = q[1:]
		_, _ = io.WriteString(c.Stdout, q[0])
	}

	return nil
}

func (f *fake) verbs() []string {
	out := make([]string, 0, len(f.calls))
	for _, c := range f.calls {
		out = append(out, c[1])
	}

	return out
}

func TestUpCreatesMissingBox(t *testing.T) {
	c := testConfig(t)
	c.Dirs = []string{filepath.Join(c.Home, "..", "extra")}
	c.Volumes = []Volume{{Name: "v1", Target: HomeDir + "/.cache"}, {Name: "v2", Target: "/opt/x"}}
	f := &fake{out: map[string][]string{"docker run": {"cid\n"}}}

	id, err := Manager{Runner: f}.Up(context.Background(), c, SameMounts)

	require.NoError(t, err)
	assert.Equal(t, "cid", id)
	assert.Equal(t, []string{"ps", "run", "exec"}, f.verbs())
	assert.Equal(t, CreateArgv(c), f.calls[1])
	assert.Equal(t, []string{"docker", "exec", "-u", "0", c.Name, "chown", "1000:1001", HomeDir + "/.cache", "/opt/x"},
		f.calls[2])

	for _, d := range []string{
		c.Workspace, c.CfgDir, c.OpenCodeDir, c.Dirs[0],
		filepath.Join(c.Home, ".local/share/opencode"), filepath.Join(c.Home, ".cache"),
	} {
		assert.DirExists(t, d)
	}
	assert.NoDirExists(t, filepath.Join(c.Home, "opt"), "a target outside the home needs no host dir")
}

func TestUpReuses(t *testing.T) {
	c := testConfig(t)
	h := MountHash(c)

	tests := []struct {
		name  string
		found string
		r     Reuse
		verbs []string
		err   error
	}{
		{"running", "cid running " + h, SameMounts, []string{"ps"}, nil},
		{"stopped is started", "cid exited " + h, SameMounts, []string{"ps", "start"}, nil},
		{"other mounts", "cid running other", SameMounts, []string{"ps"}, ErrMountsChanged},
		{"other mounts, any", "cid running other", AnyMounts, []string{"ps"}, nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := &fake{out: map[string][]string{"docker ps": {tc.found}}}

			id, err := Manager{Runner: f}.Up(context.Background(), c, tc.r)

			assert.Equal(t, tc.verbs, f.verbs())
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				assert.Contains(t, err.Error(), "sandboxed_agent down")

				return
			}

			require.NoError(t, err)
			assert.Equal(t, "cid", id)
		})
	}
}

func TestUpLostCreateRace(t *testing.T) {
	c := testConfig(t)
	f := &fake{
		out:  map[string][]string{"docker ps": {"", "cid running " + MountHash(c)}},
		fail: map[string]error{"docker run": errDocker},
	}

	id, err := Manager{Runner: f}.Up(context.Background(), c, SameMounts)

	require.NoError(t, err)
	assert.Equal(t, "cid", id)
	assert.Equal(t, []string{"ps", "run", "ps"}, f.verbs())
}

func TestUpErrors(t *testing.T) {
	c := testConfig(t)
	c.Volumes = []Volume{{Name: "v", Target: "/t"}}

	tests := []struct {
		name string
		f    *fake
	}{
		{"find fails", &fake{fail: map[string]error{"docker ps": errDocker}}},
		{"create fails", &fake{fail: map[string]error{"docker run": errDocker}}},
		{"chown fails", &fake{fail: map[string]error{"docker exec": errDocker}}},
		{"start fails", &fake{
			out:  map[string][]string{"docker ps": {"cid exited " + MountHash(c)}},
			fail: map[string]error{"docker start": errDocker},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Manager{Runner: tc.f}.Up(context.Background(), c, SameMounts)

			require.ErrorIs(t, err, errDocker)
		})
	}
}

func TestUpSourceError(t *testing.T) {
	c := testConfig(t)
	file := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(file, nil, 0o600))
	c.CfgDir = filepath.Join(file, "sub")

	_, err := Manager{Runner: &fake{}}.Up(context.Background(), c, SameMounts)

	require.Error(t, err)
}

type exitErr struct{ code int }

func (e exitErr) Error() string { return "exit" }
func (e exitErr) ExitCode() int { return e.code }

func TestExec(t *testing.T) {
	f := &fake{}
	m := Manager{Runner: f}
	ctx := context.Background()

	require.NoError(t, m.Exec(ctx, "b", ExecOpts{}, []string{"true"}))
	assert.Equal(t, []string{"docker", "exec", "b", "true"}, f.calls[0])

	f.fail = map[string]error{"docker exec": exitErr{3}}
	err := m.Exec(ctx, "b", ExecOpts{}, []string{"false"})
	var ee *ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, 3, ee.Code)
	assert.Equal(t, "exit status 3", ee.Error())

	f.fail = map[string]error{"docker exec": errDocker}
	require.ErrorIs(t, m.Exec(ctx, "b", ExecOpts{}, []string{"x"}), errDocker)
}

func TestRemoveAndList(t *testing.T) {
	f := &fake{out: map[string][]string{"docker ps": {"a\nb\n"}}}
	m := Manager{Runner: f}
	ctx := context.Background()

	names, err := m.List(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a", "b"}, names)

	require.NoError(t, m.Remove(ctx, "a", "b"))
	assert.Equal(t, RemoveArgv("a", "b"), f.calls[1])
}
