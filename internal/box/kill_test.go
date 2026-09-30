package box

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/clock"
)

// scripted answers each docker exec with the next exit code (0: success).
type scripted struct {
	calls []string
	codes []int
}

type codeErr int

func (c codeErr) Error() string { return "exit" }
func (c codeErr) ExitCode() int { return int(c) }

func (s *scripted) Run(_ context.Context, c Cmd) error {
	s.calls = append(s.calls, strings.Join(c.Argv[7:], " "))

	code := 0
	if len(s.codes) > 0 {
		code, s.codes = s.codes[0], s.codes[1:]
	}

	if code != 0 {
		return codeErr(code)
	}

	return nil
}

func pgidFile(t *testing.T, content string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), "iter.pgid")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))

	return p
}

func TestKillArgv(t *testing.T) {
	assert.Equal(t, []string{"docker", "exec", "b", "sh", "-c", `kill "$1" "-$2"`, "kill", "-TERM", "42"},
		KillArgv("b", "-TERM", 42))
}

func TestReadPGID(t *testing.T) {
	pgid, ok, err := ReadPGID(pgidFile(t, "42\n"))
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, 42, pgid)

	_, ok, err = ReadPGID(filepath.Join(t.TempDir(), "none"))
	require.NoError(t, err)
	assert.False(t, ok, "no harness started yet")

	_, _, err = ReadPGID(pgidFile(t, "x"))
	require.Error(t, err)

	_, _, err = ReadPGID(pgidFile(t, "0"))
	require.Error(t, err, "group 0 would be the killer's own")
}

// killWith runs KillGroup while advancing the fake clock `ticks` polls.
func killWith(t *testing.T, s *scripted, file string, ticks int) error {
	t.Helper()

	clk := clock.NewFake(time.Unix(0, 0))
	done := make(chan error, 1)

	go func() {
		done <- Manager{Runner: s}.KillGroup(context.Background(), "b", file, clk, 10*time.Second)
	}()

	for range ticks {
		clk.BlockUntilWaiters(1)
		clk.Advance(pollEvery)
	}

	return <-done
}

func TestKillGroupTermIsEnough(t *testing.T) {
	s := &scripted{codes: []int{0, 0, 1}} // TERM ok, alive, gone

	require.NoError(t, killWith(t, s, pgidFile(t, "42"), 2))
	assert.Equal(t, []string{"-TERM 42", "-0 42", "-0 42"}, s.calls)
}

func TestKillGroupKillsAfterGrace(t *testing.T) {
	s := &scripted{} // always alive

	require.NoError(t, killWith(t, s, pgidFile(t, "42"), 20))
	assert.Equal(t, "-TERM 42", s.calls[0])
	assert.Equal(t, "-KILL 42", s.calls[len(s.calls)-1])
	assert.Len(t, s.calls, 22, "TERM, 20 polls over 10 s, KILL")
}

func TestKillGroupAlreadyGone(t *testing.T) {
	s := &scripted{codes: []int{1}}

	require.NoError(t, killWith(t, s, pgidFile(t, "42"), 0))
	assert.Len(t, s.calls, 1)

	s = &scripted{}
	require.NoError(t, killWith(t, s, filepath.Join(t.TempDir(), "none"), 0))
	assert.Empty(t, s.calls, "no pgid file: nothing to kill")
}

func TestKillGroupErrors(t *testing.T) {
	require.Error(t, killWith(t, &scripted{codes: []int{2}}, pgidFile(t, "42"), 0), "a usage error is not a gone group")
	require.Error(t, killWith(t, &scripted{}, pgidFile(t, "junk"), 0))

	f := &fake{fail: map[string]error{"docker exec": errDocker}}
	err := Manager{Runner: f}.KillGroup(context.Background(), "b", pgidFile(t, "42"), clock.NewFake(time.Unix(0, 0)), time.Second)
	require.ErrorIs(t, err, errDocker)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = Manager{Runner: &scripted{}}.KillGroup(ctx, "b", pgidFile(t, "42"), clock.NewFake(time.Unix(0, 0)), time.Second)
	require.ErrorIs(t, err, context.Canceled)
}
