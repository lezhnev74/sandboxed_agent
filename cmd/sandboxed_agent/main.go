// Command sandboxed_agent runs coding-agent harnesses headless in a per-directory
// Docker box. It is the composition root: it reads the environment and
// wires the adapters.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/lezhnev74/sandboxed_agent/internal/box"
	"github.com/lezhnev74/sandboxed_agent/internal/paths"
	"github.com/lezhnev74/sandboxed_agent/internal/runs/spec"
)

var errNotImplemented = errors.New("not implemented yet")

// actions are what the commands do once their flags are resolved.
type actions struct {
	run func(ctx context.Context, s spec.Spec) error
	// rebuild is `sandboxed_agent --rebuild`; main wires it (it needs the host uid).
	rebuild func(ctx context.Context) error
	// shell and down act on the box of the workspace (cwd); main wires them.
	shell func(ctx context.Context) error
	down  func(ctx context.Context, all bool) error
	// login is `sandboxed_agent login HARNESS`; main wires it.
	login func(ctx context.Context, harness string) error
	// gc prunes what crashed runs and removed boxes left behind.
	gc func(ctx context.Context, older time.Duration) error
	// tui is `sandboxed_agent claude|opencode`: the harness's TUI in the box.
	tui func(ctx context.Context, harness, model string, passthrough []string) error
	// dump is `sandboxed_agent dockerfile DIR`: the image's build context.
	dump func(ctx context.Context, dir string) error
}

func defaultActions() actions {
	return actions{
		run: func(context.Context, spec.Spec) error { return errNotImplemented },
	}
}

// deps is everything the CLI takes from the process.
type deps struct {
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
	cwd     string
	actions actions
}

func main() {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandboxed_agent:", err)
		os.Exit(1)
	}

	h := host{
		dirs: paths.New(paths.Env{
			Home: os.Getenv("HOME"), DataHome: os.Getenv("XDG_DATA_HOME"), ConfigHome: os.Getenv("XDG_CONFIG_HOME"),
		}),
		uid: os.Getuid(), gid: os.Getgid(), tty: isTerminal(os.Stdin),
		stdin: os.Stdin, stdout: os.Stdout, stderr: os.Stderr,
	}

	if h.idle, err = parseIdle(os.Getenv(idleEnv)); err != nil {
		fmt.Fprintln(os.Stderr, "sandboxed_agent:", err)
		os.Exit(1)
	}

	a := h.actions(cwd)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	code := runMain(ctx, deps{
		stdin:   os.Stdin,
		stdout:  os.Stdout,
		stderr:  os.Stderr,
		cwd:     cwd,
		actions: a,
	}, os.Args[1:])

	stop()
	os.Exit(code)
}

// actions wires every command to h, acting on the workspace cwd.
func (h host) actions(cwd string) actions {
	a := defaultActions()
	a.rebuild = rebuildImage(os.Stdout, os.Stderr, h.uid, h.gid)
	a.shell = func(ctx context.Context) error { return h.shell(ctx, cwd) }
	a.down = func(ctx context.Context, all bool) error { return h.down(ctx, cwd, all) }
	a.login = func(ctx context.Context, harness string) error { return h.login(ctx, cwd, harness) }
	a.run = h.run
	a.dump = func(_ context.Context, dir string) error { return dumpImage(h.stdout, dir) }
	a.gc = func(ctx context.Context, older time.Duration) error { return h.gc(ctx, older, box.DindVolumePrefix) }
	a.tui = func(ctx context.Context, harness, model string, pass []string) error {
		return h.tui(ctx, cwd, harness, model, pass)
	}

	return a
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()

	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// runMain executes the CLI and maps an error to exit code 1, or to the exit
// code it carries: the process a shell or TUI ran in the box, or a run's
// outcome.
func runMain(ctx context.Context, d deps, args []string) int {
	err := execCLI(ctx, d, args)
	if code, ok := exitCode(err); ok {
		return code
	}

	if err != nil {
		fmt.Fprintln(d.stderr, "sandboxed_agent:", err)

		return 1
	}

	return 0
}
