package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/box"
	"github.com/lezhnev74/sandboxed_agent/internal/events"
	"github.com/lezhnev74/sandboxed_agent/internal/harness"
	"github.com/lezhnev74/sandboxed_agent/internal/runs/spec"
)

func TestResolveRun(t *testing.T) {
	ctx := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(ctx, "prompt.md"), []byte("p"), 0o600))

	a, src, err := resolveRun(spec.Spec{Harness: "claude", Ctx: ctx, Prompt: "x"})
	require.NoError(t, err)
	assert.Equal(t, "claude", a.Name)
	assert.Equal(t, "x", src.PromptText)

	_, _, err = resolveRun(spec.Spec{Harness: "claude", Ctx: ctx})
	require.ErrorIs(t, err, errNoPrompt, "the ctx's prompt.md is not a prompt")

	_, _, err = resolveRun(spec.Spec{Harness: "aider", Prompt: "x"})
	require.ErrorIs(t, err, harness.ErrUnknown)

	_, _, err = resolveRun(spec.Spec{Harness: "claude", PromptFile: "/nonexistent/p.md"})
	require.Error(t, err)
}

func TestRunBoxOpts(t *testing.T) {
	s := spec.Spec{RepoKey: "foo-1", RunID: "r1", Dirs: []string{"/d"}, Docker: true, Image: "cuda:1"}

	o := runBoxOpts(s)
	assert.Equal(t, boxOpts{dirs: []string{"/d"}, reuse: box.SameMounts, docker: true, image: "cuda:1"}, o)

	s.Fresh = true
	assert.Equal(t, "sandboxed_agent-foo-1-r1", runBoxOpts(s).name)

	s.Image, s.Dockerfile = "", "/img/Dockerfile"
	assert.Equal(t, "/img/Dockerfile", runBoxOpts(s).dockerfile)
}

func reportHost() (host, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer

	return host{stdout: &out, stderr: &errOut}, &out, &errOut
}

func TestReportPrintsTheFinalText(t *testing.T) {
	h, out, errOut := reportHost()

	require.NoError(t, h.report(false, events.Outcome{Kind: events.OK, FinalText: "all done"}, events.Meta{}))
	assert.Equal(t, "all done\n", out.String())
	assert.Empty(t, errOut.String())
}

func TestReportFailureExitsWithTheOutcomeCode(t *testing.T) {
	h, out, errOut := reportHost()

	err := h.report(false, events.Outcome{Kind: events.Fail, Detail: "exit 3", ExitCode: 3}, events.Meta{})

	var ee *box.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, events.ExitFail, ee.Code, "not the harness's own code")
	assert.Empty(t, out.String())
	assert.Equal(t, "sandboxed_agent: fail: exit 3\n", errOut.String())
}

func TestReportJSON(t *testing.T) {
	h, out, errOut := reportHost()
	o := events.Outcome{Kind: events.RateLimit, Detail: "api 429", ResetAt: time.Unix(1790000000, 0), FinalText: "limit"}

	err := h.report(true, o, events.Meta{Harness: "claude", RunID: "r1"})

	var ee *box.ExitError
	require.ErrorAs(t, err, &ee)
	assert.Equal(t, events.ExitRateLimit, ee.Code)
	assert.Contains(t, errOut.String(), "rate-limit: api 429, resets at 2026-09-21T")

	var got events.Report
	require.NoError(t, json.Unmarshal(out.Bytes(), &got))
	assert.Equal(t, events.NewReport(o, events.Meta{Harness: "claude", RunID: "r1"}), got)
	assert.Equal(t, 1, strings.Count(out.String(), "\n"), "one line")
}

func TestRunExitCodes(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want int
	}{
		{"ok", nil, 0},
		{"outcome", &box.ExitError{Code: events.ExitOverloaded}, 5},
		{"tool failure hides a box exit code", toolErr(fmt.Errorf("mise: %w", &box.ExitError{Code: 7})), 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			d := deps{
				stdin: strings.NewReader(""), stdout: &out, stderr: &out, cwd: cwd,
				actions: actions{run: func(context.Context, spec.Spec) error { return tc.err }},
			}

			assert.Equal(t, tc.want, runMain(context.Background(), d, []string{"run", "claude", "-p", "x"}))
		})
	}
}

func TestToolErr(t *testing.T) {
	require.NoError(t, toolErr(nil))
	assert.EqualError(t, toolErr(fmt.Errorf("up: %w", &box.ExitError{Code: 7})), "up: exit status 7")
}
