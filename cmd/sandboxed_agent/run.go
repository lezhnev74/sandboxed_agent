package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/lezhnev74/sandboxed_agent/internal/auth"
	"github.com/lezhnev74/sandboxed_agent/internal/box"
	"github.com/lezhnev74/sandboxed_agent/internal/bundle"
	"github.com/lezhnev74/sandboxed_agent/internal/bundle/plan"
	"github.com/lezhnev74/sandboxed_agent/internal/clock"
	"github.com/lezhnev74/sandboxed_agent/internal/driver"
	"github.com/lezhnev74/sandboxed_agent/internal/events"
	"github.com/lezhnev74/sandboxed_agent/internal/harness"
	"github.com/lezhnev74/sandboxed_agent/internal/paths"
	"github.com/lezhnev74/sandboxed_agent/internal/runs"
	"github.com/lezhnev74/sandboxed_agent/internal/runs/spec"
)

// The harnesses; claude is the default.
const (
	harnessClaude   = "claude"
	harnessOpenCode = "opencode"
)

var errUnknownHarness = errors.New("unknown harness (claude or opencode)")

var errNoPrompt = errors.New("no prompt: give -p or -f")

// run is `sandboxed_agent run`: one harness process under the workspace
// lock. Its exit code is the outcome's, carried as a *box.ExitError.
func (h host) run(ctx context.Context, s spec.Spec) error {
	err := h.runLocked(ctx, s)
	if ctx.Err() != nil {
		_, _ = fmt.Fprintln(h.stderr, "sandboxed_agent: interrupted")

		return &box.ExitError{Code: events.ExitInterrupted}
	}

	return err
}

func (h host) runLocked(ctx context.Context, s spec.Spec) error {
	a, src, err := resolveRun(s)
	if err != nil {
		return err
	}

	if err = newRun(&s); err != nil {
		return err
	}

	lock, err := runs.Lock(h.dirs.Lock(s.RepoKey))
	if err != nil {
		return err //nolint:wrapcheck // says what to do
	}
	defer lock.Release() //nolint:errcheck // closing the lock file cannot lose data

	o := runBoxOpts(s)
	if s.Fresh {
		defer h.discard(ctx, o.name)
	}

	return h.runIn(ctx, s, a, src, o)
}

// runIn brings the run's box up, runs the harness once in it and reports
// the outcome. The run's cfg dir is removed when it ends.
func (h host) runIn(ctx context.Context, s spec.Spec, a harness.Adapter, src plan.Sources, o boxOpts) error {
	name, err := h.up(ctx, s.Workspace, o)
	if err != nil {
		return toolErr(err)
	}

	r := h.runner(s, a, src, name)
	defer h.removeCfg(r.RunCfg)

	if err = h.installTools(ctx, s, &r); err != nil {
		return toolErr(err)
	}

	m := events.Meta{Harness: a.Name, Model: s.Model, RunID: s.RunID}
	if s.JSON {
		m.Version = h.harnessVersion(ctx, r)
	}

	start := clock.Real{}.Now()
	out, err := r.Run(ctx)
	m.Duration = clock.Real{}.Now().Sub(start)

	h.own(ctx, name, s.Workspace)

	if err != nil {
		return toolErr(err)
	}

	return h.report(s.JSON, out, m)
}

// report prints the outcome (the final text, or --json's report) and
// returns its exit code as a *box.ExitError; nil for ok.
func (h host) report(asJSON bool, out events.Outcome, m events.Meta) error {
	if out.Kind != events.OK {
		_, _ = fmt.Fprintln(h.stderr, "sandboxed_agent:", failureLine(out))
	}

	var err error

	switch {
	case asJSON:
		err = json.NewEncoder(h.stdout).Encode(events.NewReport(out, m))
	case out.FinalText != "":
		_, err = fmt.Fprintln(h.stdout, out.FinalText)
	}

	if err != nil {
		return fmt.Errorf("print the result: %w", err)
	}

	if code := out.Kind.ExitCode(); code != events.ExitOK {
		return &box.ExitError{Code: code}
	}

	return nil
}

// failureLine says how a failed run ended: "rate-limit: api 429, resets at ...".
func failureLine(o events.Outcome) string {
	line := o.Kind.String() + ": " + o.Detail
	if !o.ResetAt.IsZero() {
		line += ", resets at " + o.ResetAt.UTC().Format(time.RFC3339)
	}

	return line
}

// toolError is the tool's own failure: exit 1. It hides a *box.ExitError it
// wraps (say, of `mise install`) from runMain, so a run exits with its
// outcome's code or 1, never with the code of some process in the box.
type toolError struct{ err error }

func (e toolError) Error() string { return e.err.Error() }

func toolErr(err error) error {
	if err == nil {
		return nil
	}

	return toolError{err: err}
}

// removeCfg removes a run's cfg dir; a failure is only reported, `gc`
// prunes it later.
func (h host) removeCfg(dir string) {
	if err := os.RemoveAll(dir); err != nil {
		_, _ = fmt.Fprintln(h.stderr, "sandboxed_agent: removing the run's cfg dir:", err)
	}
}

// newRun gives a new run its id.
func newRun(s *spec.Spec) error {
	var err error
	s.RunID, err = paths.NewRunID(clock.Real{}.Now(), rand.Reader)

	return err //nolint:wrapcheck // names itself
}

// harnessVersion asks the box for the harness's version; "" when it can't.
func (h host) harnessVersion(ctx context.Context, r driver.Runner) string {
	var out bytes.Buffer

	o := box.ExecOpts{Dir: r.Workspace, Stdout: &out, Stderr: io.Discard}
	if err := r.Box.Exec(ctx, r.BoxName, o, r.Adapter.VersionArgv()); err != nil {
		_, _ = fmt.Fprintln(h.stderr, "sandboxed_agent: harness version:", err)

		return ""
	}

	return harness.ParseVersion(out.String())
}

// resolveRun picks the adapter and the bundle parts, before anything starts.
func resolveRun(s spec.Spec) (harness.Adapter, plan.Sources, error) {
	tbl, err := harness.Load()
	if err != nil {
		return harness.Adapter{}, plan.Sources{}, err //nolint:wrapcheck // names itself
	}

	a, err := tbl.Get(s.Harness)
	if err != nil {
		return harness.Adapter{}, plan.Sources{}, err //nolint:wrapcheck // lists the known ones
	}

	src, err := bundle.Resolve(plan.Flags{
		Ctx: s.Ctx, Prompt: s.Prompt, PromptFile: s.PromptFile, System: s.System,
	})
	if err != nil {
		return harness.Adapter{}, plan.Sources{}, err //nolint:wrapcheck // names the path
	}

	if !src.HasPrompt() {
		return harness.Adapter{}, plan.Sources{}, errNoPrompt
	}

	return a, src, nil
}

func (h host) runner(s spec.Spec, a harness.Adapter, src plan.Sources, name string) driver.Runner {
	return driver.Runner{
		Box: h.manager(), BoxName: name, Creds: h.creds(a.Name), Clock: clock.Real{}, Log: h.stderr,
		Adapter: a, Sources: src, Workspace: s.Workspace, Model: s.Model, Dirs: s.Dirs, Passthrough: s.Passthrough, Env: s.Env,
		Timeout: s.Timeout, RunCfg: h.dirs.RunCfg(s.RepoKey, s.RunID), LogFile: s.LogFile,
		Progress: func(line string) { _, _ = fmt.Fprintln(h.stderr, line) },
	}
}

// creds is the harness's login store: only Claude's is copied in and back;
// OpenCode's lives in its mounted data dir.
func (h host) creds(harnessName string) driver.Creds {
	if harnessName != harnessClaude {
		return nil
	}

	return auth.Store{Dir: h.dirs.ClaudeAuth()}
}

// own hands the files inner containers wrote as root back to the host
// user. A failure is only reported: the run's own outcome stands.
func (h host) own(ctx context.Context, name, workspace string) {
	err := h.manager().Own(context.WithoutCancel(ctx), name, workspace, h.uid, h.gid)
	if err != nil {
		_, _ = fmt.Fprintln(h.stderr, "sandboxed_agent: fixing workspace ownership:", err)
	}
}

// installTools runs `mise install` before the harness, and passes the env
// it needs on to the harness.
func (h host) installTools(ctx context.Context, s spec.Spec, r *driver.Runner) error {
	var bundleMise string
	if s.Ctx != "" {
		bundleMise = filepath.Join(s.Ctx, plan.MiseName)
	}

	env, err := driver.Toolchains{
		Box: r.Box, BoxName: r.BoxName, Workspace: s.Workspace, BundleMise: bundleMise, RunCfg: r.RunCfg, Log: h.stderr,
	}.Install(ctx)
	r.Env = slices.Concat(r.Env, env)

	return err //nolint:wrapcheck // names itself
}

// loginClaude runs `claude auth login` in the box with a staging config dir,
// then stores the credential it wrote.
func (h host) loginClaude(ctx context.Context, workspace string) error {
	name, err := h.up(ctx, workspace, boxOpts{reuse: box.AnyMounts, docker: true})
	if err != nil {
		return err
	}

	staging := filepath.Join(h.dirs.Cfg(paths.RepoKey(workspace)), "login")
	if err = os.RemoveAll(staging); err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer os.RemoveAll(staging)

	if err = paths.EnsureDir(staging); err != nil {
		return err //nolint:wrapcheck // names the dir
	}

	o := box.ExecOpts{
		Interactive: true, TTY: h.tty, Stdin: h.stdin, Stdout: h.stdout, Stderr: h.stderr,
		Env: []string{"CLAUDE_CONFIG_DIR=" + staging, "DISABLE_AUTOUPDATER=1"},
	}
	if err = h.manager().Exec(ctx, name, o, []string{harnessClaude, "auth", "login"}); err != nil {
		return err //nolint:wrapcheck // the login's exit code
	}

	if err = (auth.Store{Dir: h.dirs.ClaudeAuth()}).Put(filepath.Join(staging, auth.CredentialsName)); err != nil {
		return err //nolint:wrapcheck // says what went wrong
	}

	_, err = fmt.Fprintln(h.stdout, "stored the Claude login in", h.dirs.ClaudeAuth())

	return err //nolint:wrapcheck // a failed write to stdout needs no context
}

// login is `sandboxed_agent login [HARNESS]`.
func (h host) login(ctx context.Context, workspace, harnessName string) error {
	switch harnessName {
	case harnessClaude:
		return h.loginClaude(ctx, workspace)
	case harnessOpenCode:
		return h.loginOpenCode(ctx, workspace)
	default:
		return fmt.Errorf("login %s: %w", harnessName, errUnknownHarness)
	}
}

// loginOpenCode runs `opencode auth login` in the box; it writes auth.json
// to the mounted OpenCode data dir, which every later run sees.
func (h host) loginOpenCode(ctx context.Context, workspace string) error {
	name, err := h.up(ctx, workspace, boxOpts{reuse: box.AnyMounts, docker: true})
	if err != nil {
		return err
	}

	o := box.ExecOpts{
		Interactive: true, TTY: h.tty, Stdin: h.stdin, Stdout: h.stdout, Stderr: h.stderr,
		Env: []string{"OPENCODE_DISABLE_AUTOUPDATE=1"},
	}
	if err = h.manager().Exec(ctx, name, o, []string{harnessOpenCode, "auth", "login"}); err != nil {
		return err //nolint:wrapcheck // the login's exit code
	}

	_, err = fmt.Fprintln(h.stdout, "stored the OpenCode login in", h.dirs.OpenCode())

	return err //nolint:wrapcheck // a failed write to stdout needs no context
}
