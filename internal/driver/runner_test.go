package driver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/box"
	"github.com/lezhnev74/sandboxed_agent/internal/bundle/plan"
	"github.com/lezhnev74/sandboxed_agent/internal/clock"
	"github.com/lezhnev74/sandboxed_agent/internal/events"
	"github.com/lezhnev74/sandboxed_agent/internal/harness"
)

const resultLine = `{"type":"result","subtype":"success","is_error":false,"result":"all done"}` + "\n"

// fakeBox records the exec and answers with a canned stream.
type fakeBox struct {
	name   string
	opts   box.ExecOpts
	argv   []string
	stdin  string
	out    string
	err    error
	block  bool // wait for ctx before returning
	killed string
}

func (b *fakeBox) Exec(ctx context.Context, name string, o box.ExecOpts, argv []string) error {
	b.name, b.opts, b.argv = name, o, argv

	if o.Stdin != nil {
		in, _ := io.ReadAll(o.Stdin)
		b.stdin = string(in)
	}

	_, _ = io.WriteString(o.Stdout, b.out)

	if b.block {
		<-ctx.Done()

		return ctx.Err()
	}

	return b.err
}

func (b *fakeBox) KillGroup(_ context.Context, name, pgidFile string, _ clock.Clock, grace time.Duration) error {
	b.killed = name + " " + pgidFile + " " + grace.String()

	return nil
}

type fakeCreds struct {
	in, back string
	inErr    error
}

func (c *fakeCreds) CopyIn(dir string) ([]byte, error) {
	c.in = dir

	return []byte(`{"claudeAiOauth":{"expiresAt":1000}}`), c.inErr
}

func (c *fakeCreds) CopyBack(dir string) (bool, error) {
	c.back = dir

	return true, nil
}

func newRunner(t *testing.T, b *fakeBox, c *fakeCreds) (Runner, *bytes.Buffer) {
	t.Helper()

	root := t.TempDir()
	tbl, err := harness.Load()
	require.NoError(t, err)

	a, err := tbl.Get("claude")
	require.NoError(t, err)

	var log bytes.Buffer

	return Runner{
		Box: b, BoxName: "sandboxed_agent-ws-1", Creds: c, Clock: clock.NewFake(time.UnixMilli(5000)), Log: &log,
		Adapter: a, Sources: plan.Sources{PromptText: "list your skills"},
		Workspace: "/ws", Model: "haiku", Passthrough: []string{"--effort", "low"}, Env: []string{"A=1"},
		RunCfg: filepath.Join(root, "cfg", "run1"),
	}, &log
}

func TestRunnerRunsTheHarness(t *testing.T) {
	b := &fakeBox{out: `{"type":"system"}` + "\n" + resultLine}
	c := &fakeCreds{}
	it, logBuf := newRunner(t, b, c)

	res, err := it.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "all done", res.FinalText)
	assert.Equal(t, events.OK, res.Kind)
	assert.Equal(t, "sandboxed_agent-ws-1", b.name)
	assert.Equal(t, "list your skills", b.stdin, "the prompt goes to stdin")
	assert.True(t, b.opts.Interactive)
	assert.Equal(t, "/ws", b.opts.Dir)

	cfg := filepath.Join(it.RunCfg, "claude")
	assert.Equal(t, []string{"sandboxed_agent-exec", filepath.Join(it.RunCfg, "harness.pgid"), "claude", "-p"}, b.argv[:4])
	assert.Equal(t, []string{"--effort", "low"}, b.argv[len(b.argv)-2:])
	assert.Contains(t, b.opts.Env, "CLAUDE_CONFIG_DIR="+cfg)
	assert.Equal(t, "A=1", b.opts.Env[len(b.opts.Env)-1], "extra env comes last and wins")

	assert.Equal(t, cfg, c.in)
	assert.Equal(t, cfg, c.back)
	assert.FileExists(t, filepath.Join(cfg, "settings.json"))

	raw, err := os.ReadFile(filepath.Join(it.RunCfg, "harness.jsonl"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), resultLine)
	assert.Contains(t, logBuf.String(), "expired", "the token warning is shown")
}

func TestRunnerPassesTheHarnessExitCode(t *testing.T) {
	b := &fakeBox{out: resultLine, err: &box.ExitError{Code: 3}}
	c := &fakeCreds{}
	it, _ := newRunner(t, b, c)

	res, err := it.Run(context.Background())
	require.NoError(t, err, "a harness failure is an outcome, not an error")
	assert.Equal(t, "all done", res.FinalText, "the result is read even on failure")
	assert.Equal(t, events.Fail, res.Kind)
	assert.Equal(t, "exit 3", res.Detail)
	assert.Equal(t, 3, res.ExitCode)
	assert.NotEmpty(t, c.back, "copy-back runs even on failure")
	assert.FileExists(t, filepath.Join(it.RunCfg, "harness.jsonl"))
}

func TestRunnerReportsAnErrorResult(t *testing.T) {
	b := &fakeBox{out: `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"out of turns"}`}
	it, _ := newRunner(t, b, &fakeCreds{})

	res, err := it.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, events.Fail, res.Kind)
	assert.Equal(t, "error_max_turns", res.Detail)
}

func TestRunnerWithoutResult(t *testing.T) {
	it, _ := newRunner(t, &fakeBox{out: "garbage\n"}, &fakeCreds{})

	res, err := it.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, events.Fail, res.Kind)
	assert.Equal(t, "no result event", res.Detail)
}

func TestRunnerKillsTheGroupOnCancel(t *testing.T) {
	b := &fakeBox{block: true}
	it, _ := newRunner(t, b, &fakeCreds{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	res, err := it.Run(ctx)
	require.ErrorIs(t, err, context.Canceled)
	assert.Zero(t, res.Kind, "an interrupted run has no outcome")
	assert.Equal(t, "sandboxed_agent-ws-1 "+filepath.Join(it.RunCfg, "harness.pgid")+" 10s", b.killed)
}

func TestRunnerSetupErrors(t *testing.T) {
	errNoLogin := errors.New("no login")

	t.Run("no login", func(t *testing.T) {
		b := &fakeBox{}
		it, _ := newRunner(t, b, &fakeCreds{inErr: errNoLogin})

		_, err := it.Run(context.Background())
		require.ErrorIs(t, err, errNoLogin)
		assert.Empty(t, b.argv, "nothing runs without a login")
	})

	t.Run("unknown harness", func(t *testing.T) {
		it, _ := newRunner(t, &fakeBox{}, &fakeCreds{})
		it.Adapter.Name = "aider"

		_, err := it.Run(context.Background())
		require.ErrorIs(t, err, harness.ErrUnknown)
	})

	t.Run("bad bundle part", func(t *testing.T) {
		b := &fakeBox{}
		it, _ := newRunner(t, b, &fakeCreds{})
		it.Adapter = openCodeAdapter(t)
		it.Sources.MCP = filepath.Join(t.TempDir(), "mcp.json")
		require.NoError(t, os.WriteFile(it.Sources.MCP, []byte("nope"), 0o600))

		_, err := it.Run(context.Background())
		require.ErrorIs(t, err, harness.ErrBundle)
		assert.Empty(t, b.argv)
	})

	t.Run("missing prompt file", func(t *testing.T) {
		it, _ := newRunner(t, &fakeBox{}, &fakeCreds{})
		it.Sources = plan.Sources{Prompt: filepath.Join(t.TempDir(), "gone.md")}

		_, err := it.Run(context.Background())
		require.Error(t, err)
	})
}

func openCodeAdapter(t *testing.T) harness.Adapter {
	t.Helper()

	tbl, err := harness.Load()
	require.NoError(t, err)

	a, err := tbl.Get("opencode")
	require.NoError(t, err)

	return a
}

func TestRunnerRunsOpenCode(t *testing.T) {
	b := &fakeBox{out: `{"type":"text","part":{"text":"all done"}}` + "\n" + `{"type":"step_finish","part":{"reason":"stop"}}` + "\n"}
	it, _ := newRunner(t, b, nil)
	it.Adapter, it.Creds, it.Passthrough = openCodeAdapter(t), nil, nil

	res, err := it.Run(context.Background())
	require.NoError(t, err)

	assert.Equal(t, "all done", res.FinalText)
	assert.Equal(t, []string{"--", "list your skills"}, b.argv[len(b.argv)-2:], "the prompt is the last argument")
	assert.False(t, b.opts.Interactive, "no stdin")
	assert.Contains(t, b.opts.Env, "OPENCODE_CONFIG_DIR="+filepath.Join(it.RunCfg, "opencode"))
}

func TestRunnerReportsAnOpenCodeError(t *testing.T) {
	b := &fakeBox{out: `{"type":"error","error":{"name":"APIError","data":{"message":"bad key"}}}` + "\n", err: &box.ExitError{Code: 1}}
	it, _ := newRunner(t, b, nil)
	it.Adapter, it.Creds = openCodeAdapter(t), nil

	res, err := it.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "bad key", res.FinalText)
	assert.Equal(t, events.Fail, res.Kind)
}

func TestRunnerWritesProgressLines(t *testing.T) {
	tool := `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./..."}}]}}`
	b := &fakeBox{out: tool + "\n" + `{"type":"assistant","message":{"content":[{"type":"text","text":"` +
		strings.Repeat("x", 300) + `"}]}}` + "\n" + resultLine}
	it, _ := newRunner(t, b, &fakeCreds{})

	var lines []string

	it.Progress = func(l string) { lines = append(lines, l) }

	_, err := it.Run(context.Background())
	require.NoError(t, err)
	require.Len(t, lines, 2)
	assert.Equal(t, "Bash: go test ./...", lines[0])
	assert.Len(t, []rune(lines[1]), progressWidth)
}

func TestRunnerTimesOut(t *testing.T) {
	b := &fakeBox{out: `{"type":"system"}` + "\n", block: true}
	it, _ := newRunner(t, b, &fakeCreds{})

	fake := clock.NewFake(time.UnixMilli(5000))
	it.Clock, it.Timeout = fake, time.Minute

	go func() {
		fake.BlockUntilWaiters(1)
		fake.Advance(time.Minute)
	}()

	res, err := it.Run(context.Background())
	require.NoError(t, err, "a timeout is an outcome, not a tool failure")
	assert.Equal(t, events.Timeout, res.Kind)
	assert.Contains(t, b.killed, "harness.pgid", "the hung harness is killed")
}

func TestRunnerWithoutTimeoutNeverWaits(t *testing.T) {
	b := &fakeBox{out: resultLine}
	it, _ := newRunner(t, b, &fakeCreds{})

	_, err := it.Run(context.Background())
	require.NoError(t, err)
}

func TestRunnerWritesTheLogFile(t *testing.T) {
	b := &fakeBox{out: resultLine}
	it, _ := newRunner(t, b, &fakeCreds{})
	it.LogFile = filepath.Join(t.TempDir(), "run.jsonl")
	require.NoError(t, os.WriteFile(it.LogFile, []byte("old contents that are longer\n"), 0o600))

	res, err := it.Run(context.Background())
	require.NoError(t, err)
	assert.Equal(t, events.OK, res.Kind)

	raw, err := os.ReadFile(it.LogFile)
	require.NoError(t, err)
	assert.JSONEq(t, resultLine, string(raw), "truncated, then the raw output")
	assert.NoFileExists(t, filepath.Join(it.RunCfg, "harness.jsonl"))
}

func TestRunnerLogFileNeedsItsParent(t *testing.T) {
	b := &fakeBox{out: resultLine}
	c := &fakeCreds{}
	it, _ := newRunner(t, b, c)
	it.LogFile = filepath.Join(t.TempDir(), "missing", "run.jsonl")

	res, err := it.Run(context.Background())
	require.ErrorContains(t, err, "harness log")
	assert.Zero(t, res.Kind)
	assert.Empty(t, b.argv, "nothing runs without a log")
	assert.NotEmpty(t, c.back, "the login is copied back anyway")
}

func TestRunnerToolFailureHasNoOutcome(t *testing.T) {
	errDocker := errors.New("docker: not running")
	it, _ := newRunner(t, &fakeBox{out: resultLine, err: errDocker}, &fakeCreds{})

	res, err := it.Run(context.Background())
	require.ErrorIs(t, err, errDocker)
	assert.Zero(t, res.Kind)
}
