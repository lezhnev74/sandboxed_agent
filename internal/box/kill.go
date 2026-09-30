package box

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lezhnev74/sandboxed_agent/internal/clock"
)

// KillGrace is how long a harness gets between TERM and KILL.
const KillGrace = 10 * time.Second

// pollEvery is how often KillGroup checks whether the group is gone.
const pollEvery = 500 * time.Millisecond

// ErrPGID is returned for a pgid file that doesn't hold a usable group id.
var ErrPGID = errors.New("bad pgid file")

// killScript uses dash's kill (procps kill mis-parses a negative pid). It
// exits 1 when the group is gone, 2 on a usage error.
const killScript = `kill "$1" "-$2"`

// KillArgv sends sig (e.g. -TERM, or -0 to probe) to the process group pgid
// inside the box. It exits non-zero when the group is gone.
func KillArgv(name, sig string, pgid int) []string {
	return []string{docker, "exec", name, "sh", "-c", killScript, "kill", sig, strconv.Itoa(pgid)}
}

// ReadPGID reads the group id sandboxed_agent-exec wrote. ok is false when the
// file doesn't exist yet (the harness never started).
func ReadPGID(file string) (pgid int, ok bool, err error) {
	b, err := os.ReadFile(file)
	if errors.Is(err, os.ErrNotExist) {
		return 0, false, nil
	}

	if err != nil {
		return 0, false, fmt.Errorf("box: %w", err)
	}

	pgid, err = strconv.Atoi(strings.TrimSpace(string(b)))
	if err != nil || pgid <= 1 {
		return 0, false, fmt.Errorf("%w %s: %q", ErrPGID, file, b)
	}

	return pgid, true, nil
}

// KillGroup ends the harness whose group id is in pgidFile: TERM, then KILL
// if the group is still alive after grace. Killing a `docker exec` client
// leaves its process running, so this is how a run is stopped.
func (m Manager) KillGroup(ctx context.Context, name, pgidFile string, clk clock.Clock, grace time.Duration) error {
	pgid, ok, err := ReadPGID(pgidFile)
	if !ok {
		return err
	}

	gone, err := m.signal(ctx, KillArgv(name, "-TERM", pgid))
	if gone || err != nil {
		return err
	}

	if gone, err = m.waitGone(ctx, name, pgid, clk, grace); gone || err != nil {
		return err
	}

	_, err = m.signal(ctx, KillArgv(name, "-KILL", pgid))

	return err
}

// waitGone polls until the group is gone or grace has passed.
func (m Manager) waitGone(ctx context.Context, name string, pgid int, clk clock.Clock, grace time.Duration) (bool, error) {
	for waited := time.Duration(0); waited < grace; waited += pollEvery {
		if err := clk.Sleep(ctx, pollEvery); err != nil {
			return false, fmt.Errorf("box: kill: %w", err)
		}

		if gone, err := m.signal(ctx, KillArgv(name, "-0", pgid)); gone || err != nil {
			return gone, err
		}
	}

	return false, nil
}

// signal runs a kill argv; gone reports that kill found no such group.
// kill's own "no such process" is not shown.
func (m Manager) signal(ctx context.Context, argv []string) (gone bool, err error) {
	err = m.Runner.Run(ctx, Cmd{Argv: argv})
	if err == nil {
		return false, nil
	}

	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) && coded.ExitCode() == 1 {
		return true, nil
	}

	return false, fmt.Errorf("box: kill: %w", err)
}
