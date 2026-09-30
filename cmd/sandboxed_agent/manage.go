package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/lezhnev74/sandboxed_agent/internal/box"
	"github.com/lezhnev74/sandboxed_agent/internal/bundle"
	"github.com/lezhnev74/sandboxed_agent/internal/clock"
	"github.com/lezhnev74/sandboxed_agent/internal/harness"
	"github.com/lezhnev74/sandboxed_agent/internal/paths"
	"github.com/lezhnev74/sandboxed_agent/internal/runs"
)

// idleEnv overrides how long an idle box keeps running (a Go duration); it
// shapes boxes created from then on.
const idleEnv = "SANDBOXED_AGENT_IDLE_TIMEOUT"

var errBadIdle = errors.New(idleEnv + " must be a positive duration, e.g. 30m")

// parseIdle reads idleEnv; empty is the default.
func parseIdle(v string) (time.Duration, error) {
	if v == "" {
		return 0, nil
	}

	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return 0, fmt.Errorf("%w: %q", errBadIdle, v)
	}

	return d, nil
}

// gc is `sandboxed_agent gc`: prune the stale run cfg dirs of every repo,
// then the dind volumes (named dindPrefix*) of boxes that no longer exist.
func (h host) gc(ctx context.Context, older time.Duration, dindPrefix string) error {
	entries, err := os.ReadDir(h.dirs.CfgRoot())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("gc: %w", err)
	}

	var (
		n    int
		errs []error
	)

	for _, e := range entries {
		if e.IsDir() {
			removed, rerr := h.gcRepo(e.Name(), older)
			n, errs = n+removed, append(errs, rerr)
		}
	}

	vols, err := h.manager().PruneDind(ctx, dindPrefix)
	errs = append(errs, err)

	_, _ = fmt.Fprintf(h.stdout, "removed %d run cfg dirs, %d dind volumes\n", n, len(vols))

	return errors.Join(errs...)
}

// gcRepo prunes one repo's stale run cfg dirs under its workspace lock; a
// repo with a run going on is skipped.
func (h host) gcRepo(key string, older time.Duration) (int, error) {
	lock, err := runs.Lock(h.dirs.Lock(key))
	if errors.Is(err, runs.ErrBusy) {
		return 0, nil
	}

	if err != nil {
		return 0, err //nolint:wrapcheck // names itself
	}
	defer lock.Release() //nolint:errcheck // closing the lock file cannot lose data

	dirs, err := runs.StaleCfgDirs(h.dirs.Cfg(key), clock.Real{}.Now(), older)
	if err != nil {
		return 0, err //nolint:wrapcheck // names the dir
	}

	var errs []error
	for _, d := range dirs {
		errs = append(errs, os.RemoveAll(d))
	}

	if err = errors.Join(errs...); err != nil {
		return 0, fmt.Errorf("gc %s: %w", key, err)
	}

	return len(dirs), nil
}

// tui is `sandboxed_agent claude|opencode`: the harness's TUI in the box with a
// fresh bare config dir, removed when it exits.
func (h host) tui(ctx context.Context, workspace, harnessName, model string, pass []string) error {
	tbl, err := harness.Load()
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}

	a, err := tbl.Get(harnessName)
	if err != nil {
		return err //nolint:wrapcheck // lists the known ones
	}

	name, err := h.up(ctx, workspace, boxOpts{reuse: box.AnyMounts, docker: true})
	if err != nil {
		return err
	}

	id, err := paths.NewRunID(clock.Real{}.Now(), rand.Reader)
	if err != nil {
		return err //nolint:wrapcheck // names itself
	}

	runCfg := h.dirs.RunCfg(paths.RepoKey(workspace), "tui-"+id)
	defer os.RemoveAll(runCfg)

	cfgDir := filepath.Join(runCfg, a.Name)

	p, err := h.tuiPlan(a, cfgDir, model, pass)
	if err != nil {
		return err
	}

	o := box.ExecOpts{
		Interactive: true, TTY: h.tty, Env: p.Env, Dir: workspace, Stdin: h.stdin, Stdout: h.stdout, Stderr: h.stderr,
	}

	return h.execWithLogin(a.Name, cfgDir, func() error { return h.manager().Exec(ctx, name, o, p.Argv) })
}

// tuiPlan writes the TUI's bare config into cfgDir and returns its plan.
func (h host) tuiPlan(a harness.Adapter, cfgDir, model string, pass []string) (harness.Plan, error) {
	if err := paths.EnsureDir(cfgDir); err != nil {
		return harness.Plan{}, err //nolint:wrapcheck // names the dir
	}

	p, err := harness.Build(a, harness.Input{ConfigDir: cfgDir, Model: model, Passthrough: pass, Interactive: true})
	if err != nil {
		return harness.Plan{}, err //nolint:wrapcheck // names the harness
	}

	return p, bundle.Apply(p.Files, p.Copies) //nolint:wrapcheck // names the path
}

// execWithLogin copies the stored login into cfgDir around run, when the
// harness has one to copy.
func (h host) execWithLogin(harnessName, cfgDir string, run func() error) error {
	creds := h.creds(harnessName)
	if creds == nil {
		return run()
	}

	if _, err := creds.CopyIn(cfgDir); err != nil {
		return err //nolint:wrapcheck // says what to do
	}

	err := run()
	_, backErr := creds.CopyBack(cfgDir)

	return errors.Join(err, backErr)
}
