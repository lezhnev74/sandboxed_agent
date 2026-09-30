package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/box"
	"github.com/lezhnev74/sandboxed_agent/internal/paths"
	"github.com/lezhnev74/sandboxed_agent/internal/runs/spec"
)

const cwd = "/home/u/Code/foo"

type recorder struct {
	spec  spec.Spec
	cmd   string
	all   bool
	login string
	older time.Duration
	dir   string
	model string
	pass  []string
}

// execute runs the CLI with actions that record what they were given.
func execute(t *testing.T, stdin string, args ...string) (*recorder, string, error) {
	t.Helper()

	rec := &recorder{}
	var out bytes.Buffer
	d := deps{
		stdin:  strings.NewReader(stdin),
		stdout: &out,
		stderr: &out,
		cwd:    cwd,
		actions: actions{
			run:     func(_ context.Context, s spec.Spec) error { rec.cmd, rec.spec = "run", s; return nil },
			rebuild: func(context.Context) error { rec.cmd = "rebuild"; return nil },
			shell:   func(context.Context) error { rec.cmd = "shell"; return nil },
			down:    func(_ context.Context, all bool) error { rec.cmd, rec.all = "down", all; return nil },
			login:   func(_ context.Context, h string) error { rec.cmd, rec.login = "login", h; return nil },
			gc:      func(_ context.Context, older time.Duration) error { rec.cmd, rec.older = "gc", older; return nil },
			dump:    func(_ context.Context, dir string) error { rec.cmd, rec.dir = "dockerfile", dir; return nil },
			tui: func(_ context.Context, h, m string, pass []string) error {
				rec.cmd, rec.model, rec.pass = h, m, pass
				return nil
			},
		},
	}

	err := execCLI(context.Background(), d, args)

	return rec, out.String(), err
}

func TestRunDefaults(t *testing.T) {
	rec, _, err := execute(t, "", "run", "claude")
	require.NoError(t, err)

	want := spec.Default()
	want.Harness, want.Workspace, want.RepoKey = "claude", cwd, paths.RepoKey(cwd)
	want.Ctx = cwd + "/.sandboxed_agent"
	assert.Equal(t, "run", rec.cmd)
	assert.Equal(t, want, rec.spec)
}

func TestRunPassthroughAfterDash(t *testing.T) {
	rec, _, err := execute(t, "", "run", "claude", "-m", "sonnet", "--", "--effort", "high", "--json")
	require.NoError(t, err)

	assert.Equal(t, "sonnet", rec.spec.Model)
	assert.Equal(t, []string{"--effort", "high", "--json"}, rec.spec.Passthrough)
	assert.False(t, rec.spec.JSON, "--json after -- belongs to the harness")
}

func TestModelIsVerbatim(t *testing.T) {
	rec, _, err := execute(t, "", "run", "opencode", "-m", "openrouter/qwen/qwen3-coder#high")
	require.NoError(t, err)

	assert.Equal(t, "openrouter/qwen/qwen3-coder#high", rec.spec.Model)
}

func TestRunAllFlags(t *testing.T) {
	rec, _, err := execute(t, "",
		"run", "claude", "-m", "opus",
		"-f", "p.md", "--system", "sys.md", "--ctx", "ctx", "--dir", "d1", "--dir", "/d2",
		"--image", "cuda:1", "--docker", "off", "--fresh",
		"-e", "A=1", "-e", "B=x=y",
		"--timeout", "90s", "--json", "--log", "out/run.jsonl",
	)
	require.NoError(t, err)

	want := spec.Default()
	want.Harness = "claude"
	want.Model = "opus"
	want.Workspace = cwd
	want.RepoKey = paths.RepoKey(cwd)
	want.PromptFile = cwd + "/p.md"
	want.System = cwd + "/sys.md"
	want.Ctx = cwd + "/ctx"
	want.Dirs = []string{cwd + "/d1", "/d2"}
	want.Image = "cuda:1"
	want.Docker = false
	want.Fresh = true
	want.Env = []string{"A=1", "B=x=y"}
	want.Timeout = 90 * time.Second
	want.JSON = true
	want.LogFile = cwd + "/out/run.jsonl"
	assert.Equal(t, want, rec.spec)
}

func TestRunDockerfile(t *testing.T) {
	rec, _, err := execute(t, "", "run", "claude", "--dockerfile", "img/Dockerfile")
	require.NoError(t, err)

	assert.Equal(t, cwd+"/img/Dockerfile", rec.spec.Dockerfile)
	assert.Empty(t, rec.spec.Image)
}

func TestDumpDockerfile(t *testing.T) {
	rec, _, err := execute(t, "", "dockerfile", "img")
	require.NoError(t, err)

	assert.Equal(t, "dockerfile", rec.cmd)
	assert.Equal(t, cwd+"/img", rec.dir)

	_, _, err = execute(t, "", "dockerfile")
	require.Error(t, err, "DIR is required")
}

func TestPromptInline(t *testing.T) {
	rec, _, err := execute(t, "", "run", "claude", "-p", "fix the test")
	require.NoError(t, err)

	assert.Equal(t, "run", rec.cmd)
	assert.Equal(t, "fix the test", rec.spec.Prompt)
	assert.Empty(t, rec.spec.PromptFile)
}

func TestPromptFromStdin(t *testing.T) {
	rec, _, err := execute(t, "from stdin\n", "run", "claude", "-f", "-")
	require.NoError(t, err)

	assert.Equal(t, "from stdin\n", rec.spec.Prompt)
	assert.Empty(t, rec.spec.PromptFile)
}

func TestRebuild(t *testing.T) {
	rec, _, err := execute(t, "", "--rebuild")
	require.NoError(t, err)

	assert.Equal(t, "rebuild", rec.cmd)
}

func TestShellAndDown(t *testing.T) {
	rec, _, err := execute(t, "", "shell")
	require.NoError(t, err)
	assert.Equal(t, "shell", rec.cmd)

	rec, _, err = execute(t, "", "down")
	require.NoError(t, err)
	assert.Equal(t, "down", rec.cmd)
	assert.False(t, rec.all)

	rec, _, err = execute(t, "", "down", "--all")
	require.NoError(t, err)
	assert.True(t, rec.all)
}

func TestMainPassesBoxExitCode(t *testing.T) {
	var out bytes.Buffer
	d := deps{
		stdin: strings.NewReader(""), stdout: &out, stderr: &out, cwd: cwd,
		actions: actions{shell: func(context.Context) error { return fmt.Errorf("wrapped: %w", &box.ExitError{Code: 7}) }},
	}

	assert.Equal(t, 7, runMain(context.Background(), d, []string{"shell"}))
	assert.Empty(t, out.String(), "the shell's own exit is not an agent error")
}

func TestLogin(t *testing.T) {
	rec, _, err := execute(t, "", "login")
	require.NoError(t, err)
	assert.Equal(t, "login", rec.cmd)
	assert.Equal(t, "claude", rec.login, "claude is the default")

	rec, _, err = execute(t, "", "login", "opencode")
	require.NoError(t, err)
	assert.Equal(t, "opencode", rec.login)

	_, _, err = execute(t, "", "login", "a", "b")
	require.Error(t, err)
}

func TestUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no harness", []string{"run"}, "HARNESS"},
		{"two harnesses", []string{"run", "claude", "opencode"}, "HARNESS"},
		{"-p and -f", []string{"run", "claude", "-p", "x", "-f", "y"}, "-p"},
		{"bad env", []string{"run", "claude", "-e", "NOEQUALS"}, "K=V"},
		{"bad docker", []string{"run", "claude", "--docker", "maybe"}, "--docker"},
		{"image and dockerfile", []string{"run", "claude", "--image", "x:1", "--dockerfile", "D"}, "exclusive"},
		{"no loop", []string{"loop", "claude"}, "unknown command"},
		{"removed flag", []string{"run", "claude", "--worktree"}, "unknown flag"},
		{"bad timeout", []string{"run", "claude", "--timeout", "soon"}, "timeout"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, _, err := execute(t, "", tc.args...)

			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Empty(t, rec.cmd, "no action runs on a usage error")
		})
	}
}

func TestTUIArgsGoAfterDash(t *testing.T) {
	rec, _, err := execute(t, "", "claude", "a")
	require.ErrorIs(t, err, errUsage)
	assert.Empty(t, rec.cmd)
}

func TestRunCommandsReachTheirActions(t *testing.T) {
	tests := []struct {
		args []string
		want recorder
	}{
		{[]string{"gc"}, recorder{cmd: "gc"}},
		{[]string{"gc", "--older", "72h"}, recorder{cmd: "gc", older: 72 * time.Hour}},
		{[]string{"claude"}, recorder{cmd: "claude"}},
		{[]string{"opencode", "-m", "x", "--", "--y"}, recorder{cmd: "opencode", model: "x", pass: []string{"--y"}}},
	}

	for _, tc := range tests {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			rec, _, err := execute(t, "", tc.args...)
			require.NoError(t, err)
			assert.Equal(t, tc.want, *rec)
		})
	}
}

func TestDefaultActionsAreNotImplemented(t *testing.T) {
	a := defaultActions()
	ctx := context.Background()

	require.ErrorIs(t, a.run(ctx, spec.Spec{}), errNotImplemented)
}

func TestMainExitCode(t *testing.T) {
	var out bytes.Buffer
	d := deps{stdin: strings.NewReader(""), stdout: &out, stderr: &out, cwd: cwd, actions: defaultActions()}

	assert.Equal(t, 1, runMain(context.Background(), d, []string{"run", "claude", "-p", "x"}))
	assert.Contains(t, out.String(), "not implemented yet")

	out.Reset()
	assert.Equal(t, 0, runMain(context.Background(), d, []string{"--help"}))
}

func TestRootHelpListsEveryCommand(t *testing.T) {
	_, out, err := execute(t, "", "--help")
	require.NoError(t, err)

	for _, c := range []string{
		"run", "shell", "down", "gc", "dockerfile",
		"login", "claude", "opencode", "--rebuild",
	} {
		assert.Contains(t, out, c)
	}
}

func TestRunHelpListsEveryFlag(t *testing.T) {
	_, out, err := execute(t, "", "run", "--help")
	require.NoError(t, err)

	for _, f := range []string{
		"-m, --model", "-p, --prompt", "-f, --file", "--system", "--ctx", "--dir", "--image", "--dockerfile", "--docker", "--fresh",
		"-e, --env", "--timeout", "--json", "--log",
	} {
		assert.Contains(t, out, f)
	}
}

func TestNoMaskingFlag(t *testing.T) {
	_, _, err := execute(t, "", "run", "claude", "--keep-repo-context")
	require.Error(t, err, "repo agent files are never masked")
}

func TestPromptFileReadError(t *testing.T) {
	rec := &recorder{}
	d := deps{
		stdin: errReader{}, stdout: os.Stderr, stderr: os.Stderr, cwd: cwd,
		actions: actions{run: func(_ context.Context, s spec.Spec) error { rec.spec = s; return nil }},
	}

	err := execCLI(context.Background(), d, []string{"run", "claude", "-f", "-"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "stdin")
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, os.ErrClosed }

func TestAbsPath(t *testing.T) {
	assert.Empty(t, absPath(cwd, ""))
	assert.Equal(t, "/x", absPath(cwd, "/x"))
	assert.Equal(t, filepath.Join(cwd, "a/b"), absPath(cwd, "a/../a/b"))
}
