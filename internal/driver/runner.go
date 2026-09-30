// Package driver executes a run: it composes the adapters (box, bundle,
// auth) to run a harness once and classify how it ended.
package driver

import (
	"context"
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
	"github.com/lezhnev74/sandboxed_agent/internal/events"
	"github.com/lezhnev74/sandboxed_agent/internal/harness"
)

// errTimeout cancels a run that ran past its --timeout.
var errTimeout = errors.New("timed out")

// Box is what a run needs from box.Manager.
type Box interface {
	Exec(ctx context.Context, name string, o box.ExecOpts, argv []string) error
	KillGroup(ctx context.Context, name, pgidFile string, clk clock.Clock, grace time.Duration) error
}

// Creds moves the stored login in and out of a run's config dir (auth.Store).
// A harness whose login lives in a mounted data dir (OpenCode) has none.
type Creds interface {
	CopyIn(cfgDir string) ([]byte, error)
	CopyBack(cfgDir string) (bool, error)
}

// Runner runs the harness once.
type Runner struct {
	Box     Box
	BoxName string
	Creds   Creds
	Clock   clock.Clock
	Log     io.Writer // warnings and the harness's stderr

	Adapter     harness.Adapter
	Sources     plan.Sources
	Workspace   string
	Model       string
	Dirs        []string // extra dirs (--dir), granted to the harness
	Passthrough []string
	Env         []string      // extra K=V, after the harness's own
	Timeout     time.Duration // kills a hung harness; zero is none

	RunCfg  string // cfg/<repo-key>/<run-id>: mounted, the same path in the box
	LogFile string // the raw JSON log on the host; "" is harness.jsonl in RunCfg

	// Progress gets one line per tool call or message while the harness
	// runs ("Bash: go test ./..."); nil drops them.
	Progress func(line string)
}

// progressWidth caps a progress line.
const progressWidth = 200

func (r Runner) configDir() string { return filepath.Join(r.RunCfg, r.Adapter.Name) }

// PGIDFile holds the harness's process group id while it runs.
func (r Runner) PGIDFile() string { return filepath.Join(r.RunCfg, "harness.pgid") }

func (r Runner) logFile() string {
	if r.LogFile != "" {
		return r.LogFile
	}

	return filepath.Join(r.RunCfg, "harness.jsonl")
}

// Run prepares the run (bundle copy, config, login), runs the harness,
// copies the login back and returns its classified outcome. How the harness
// ended (non-zero exit, error result, timeout, limits) is the outcome only.
// The error is set when the tool failed or ctx was canceled; the outcome's
// Kind is zero then.
func (r Runner) Run(ctx context.Context) (events.Outcome, error) {
	p, err := r.prepare()
	if err != nil {
		return events.Outcome{}, err
	}

	logf, err := os.OpenFile(r.logFile(), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return events.Outcome{}, errors.Join(fmt.Errorf("harness log: %w", err), r.copyBack())
	}

	rctx, done := r.deadline(ctx)
	runErr := r.exec(rctx, p, logf)

	done()

	code, timedOut, runErr := harnessEnd(runErr)
	if err = errors.Join(runErr, logf.Close(), r.copyBack()); err != nil {
		return events.Outcome{}, err
	}

	return readOutcome(r.Adapter.Name, r.logFile(), code, timedOut)
}

// harnessEnd splits the exec's error into the harness's exit code, whether
// it timed out, and what is left: a tool failure or a cancel.
func harnessEnd(err error) (int, bool, error) {
	var ee *box.ExitError

	switch {
	case errors.Is(err, errTimeout):
		return 0, true, nil
	case errors.As(err, &ee):
		return ee.Code, false, nil
	default:
		return 0, false, err
	}
}

// deadline cancels the returned ctx with errTimeout after r.Timeout; the
// returned func releases it.
func (r Runner) deadline(ctx context.Context) (context.Context, func()) {
	if r.Timeout <= 0 {
		return ctx, func() {}
	}

	rctx, cancel := context.WithCancelCause(ctx)
	fired := r.Clock.After(r.Timeout)

	go func() {
		select {
		case <-fired:
			cancel(errTimeout)
		case <-rctx.Done():
		}
	}()

	return rctx, func() { cancel(nil) }
}

// prepare copies the bundle, writes the harness config and copies the login in.
func (r Runner) prepare() (harness.Plan, error) {
	parts, err := bundle.Copy(r.Sources, filepath.Join(r.RunCfg, "bundle"))
	if err != nil {
		return harness.Plan{}, err //nolint:wrapcheck // bundle errors name the path
	}

	contents, err := bundle.ReadContents(parts)
	if err != nil {
		return harness.Plan{}, err //nolint:wrapcheck // bundle errors name the path
	}

	in := harness.Input{
		ConfigDir: r.configDir(), Model: r.Model, Dirs: r.Dirs, Bundle: parts, Contents: contents, Passthrough: r.Passthrough,
	}

	p, err := harness.Build(r.Adapter, in)
	if err != nil {
		return harness.Plan{}, err //nolint:wrapcheck // names the harness or the bundle part
	}

	if err = bundle.Apply(p.Files, p.Copies); err != nil {
		return harness.Plan{}, err //nolint:wrapcheck // bundle errors name the path
	}

	return p, r.copyIn()
}

// copyIn copies the stored login into the config dir and warns when it is
// about to expire.
func (r Runner) copyIn() error {
	if r.Creds == nil {
		return nil
	}

	cred, err := r.Creds.CopyIn(r.configDir())
	if err != nil {
		return err //nolint:wrapcheck // auth errors say what to do
	}

	if w := auth.Warning(cred, r.Clock.Now()); w != "" {
		_, _ = fmt.Fprintln(r.Log, w)
	}

	return nil
}

func (r Runner) copyBack() error {
	if r.Creds == nil {
		return nil
	}

	_, err := r.Creds.CopyBack(r.configDir())

	return err //nolint:wrapcheck // auth errors say what went wrong
}

// exec runs the plan under sandboxed_agent-exec, so its process group can be
// killed; a canceled ctx kills it. The harness's output goes to raw.
func (r Runner) exec(ctx context.Context, p harness.Plan, raw io.Writer) error {
	lines := r.progressWriter()
	defer lines.Flush()

	o := box.ExecOpts{
		Env: slices.Concat(p.Env, r.Env), Dir: r.Workspace, Stdout: io.MultiWriter(raw, lines), Stderr: r.Log,
	}

	if p.Stdin != "" {
		in, err := os.Open(p.Stdin)
		if err != nil {
			return fmt.Errorf("prompt: %w", err)
		}
		defer in.Close()

		o.Interactive, o.Stdin = true, in
	}

	argv := append([]string{"sandboxed_agent-exec", r.PGIDFile()}, p.Argv...)

	err := r.Box.Exec(ctx, r.BoxName, o, argv)
	if ctx.Err() != nil {
		kerr := r.Box.KillGroup(context.WithoutCancel(ctx), r.BoxName, r.PGIDFile(), r.Clock, box.KillGrace)

		return errors.Join(context.Cause(ctx), kerr)
	}

	return err //nolint:wrapcheck // *box.ExitError carries the harness's exit code
}

// progressWriter turns the harness's output lines into progress lines.
func (r Runner) progressWriter() *events.LineWriter {
	return &events.LineWriter{Fn: func(line []byte) {
		if r.Progress == nil {
			return
		}

		for _, d := range events.Describe(r.Adapter.Name, line) {
			r.Progress(events.Truncate(d, progressWidth))
		}
	}}
}

func readOutcome(harnessName, logFile string, code int, timedOut bool) (events.Outcome, error) {
	f, err := os.Open(logFile)
	if err != nil {
		return events.Outcome{}, fmt.Errorf("harness log: %w", err)
	}
	defer f.Close()

	return events.Read(harnessName, f, code, timedOut) //nolint:wrapcheck // names itself
}
