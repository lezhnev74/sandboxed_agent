// Package box finds, creates, execs into and removes a repo's long-lived
// box container. The docker argv is decided here by pure functions;
// run_exec.go runs it.
package box

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"
)

// Box labels: LabelWorkspace marks sandboxed_agent boxes (value: the workspace),
// LabelMounts holds the hash of everything fixed at creation.
const (
	LabelWorkspace = "sandboxed_agent.workspace"
	LabelMounts    = "sandboxed_agent.mounts"
)

// HomeDir is the box user's home.
const HomeDir = "/home/agent"

// Resource limits from goal.md "Max access".
const (
	pidsLimit = "4096"
	cpus      = "4"
	memory    = "8g"
)

const docker = "docker"

// Repeated docker flags.
const (
	flagMount  = "--mount"
	flagLabel  = "--label"
	flagFilter = "--filter"
	flagFormat = "--format"
)

// runDetached is `docker run -d --name name args...`.
func runDetached(name string, args ...string) []string {
	return append([]string{docker, "run", "-d", "--name", name}, args...)
}

// ErrMountsChanged means the box exists with other mounts than asked for.
var ErrMountsChanged = errors.New("box mounts changed")

// Config is everything a box is created with. Host paths are absolute.
type Config struct {
	Name      string
	Image     string
	Workspace string // mounted at the same path, the working dir
	UID, GID  int

	CfgDir      string   // host cfg/<repo-key>, mounted at the same path
	Home        string   // host box home, mounted at HomeDir
	OpenCodeDir string   // host OpenCode data, mounted at HomeDir/.local/share/opencode
	Dirs        []string // extra rw dirs, mounted at the same paths
	EnvFile     string   // provider keys, passed as --env-file when non-empty
	GitConfig   string   // GIT_CONFIG_GLOBAL inside the box, when non-empty
	Docker      *Docker  // the dind sidecar; nil: none, docker's default network
	Volumes     []Volume // named volumes, owned by UID:GID once the box is created
	// ImageName is the --image TAG the box runs (empty: the default
	// image). It is a label, so a box of another image is refused.
	ImageName string
	// Idle stops the box after this long with no exec'd process; zero is
	// DefaultIdle. Not part of the mount hash: it only shapes new boxes.
	Idle time.Duration
}

// IdleCmd is the box's main process: it exits, and so stops the box, once
// no process has been exec'd into the box for a while.
const IdleCmd = "sandboxed_agent-idle"

// DefaultIdle is how long an idle box keeps running.
const DefaultIdle = time.Hour

// idlePoll is how often sandboxed_agent-idle looks for exec'd processes.
const idlePoll = 10 * time.Second

// idleTimes are the idle timeout and the poll interval in whole seconds
// (at least 1). A short timeout polls a third as often, so it is hit within
// a third of its length.
func idleTimes(d time.Duration) (int, int) {
	if d <= 0 {
		d = DefaultIdle
	}

	secs := max(int(d/time.Second), 1)

	return secs, min(int(idlePoll/time.Second), max(secs/3, 1))
}

// IdleArgs are sandboxed_agent-idle's arguments: the timeout, the poll
// interval and, for a box with a sidecar, the heartbeat file it rewrites
// every poll.
func IdleArgs(d time.Duration, heartbeat string) []string {
	secs, poll := idleTimes(d)
	args := []string{strconv.Itoa(secs), strconv.Itoa(poll)}

	if heartbeat != "" {
		args = append(args, heartbeat)
	}

	return args
}

// StaleSecs is how old the heartbeat of a box with idle timeout d may get
// before its sidecar stops: three polls and a margin.
func StaleSecs(d time.Duration) int {
	_, poll := idleTimes(d)

	return 3*poll + 15
}

// Volume is a named docker volume mounted in the box.
type Volume struct {
	Name   string
	Target string // absolute path in the box
}

// Volumes shared by every box: toolchains and build caches survive
// `sandboxed_agent down` and are downloaded once per machine.
func Volumes() []Volume {
	return []Volume{
		{Name: "sandboxed_agent-mise", Target: HomeDir + "/.local/share/mise"},
		{Name: "sandboxed_agent-cache", Target: HomeDir + "/.cache"},
		{Name: "sandboxed_agent-gomod", Target: HomeDir + "/go/pkg/mod"},
	}
}

// OpenCodeTarget is where the OpenCode data dir is mounted in the box.
const OpenCodeTarget = HomeDir + "/.local/share/opencode"

// LabelImage records a box's --image TAG.
const LabelImage = "sandboxed_agent.image"

// fixedArgs are the create options that can't change without recreating the
// box. Their hash is stored as LabelMounts.
func fixedArgs(c Config) []string {
	args := []string{
		"--entrypoint", IdleCmd, "--init", "--user", strconv.Itoa(c.UID) + ":" + strconv.Itoa(c.GID),
		"--pids-limit", pidsLimit, "--cpus", cpus, "--memory", memory,
		"-w", c.Workspace, "-e", "HOME=" + HomeDir,
	}
	if c.Docker != nil {
		args = append(args, "--network", "container:"+SidecarName(c.Name),
			"-e", "DOCKER_HOST="+DockerHost, "-e", "TESTCONTAINERS_HOST_OVERRIDE=localhost")
	}

	if c.GitConfig != "" {
		args = append(args, "-e", "GIT_CONFIG_GLOBAL="+c.GitConfig)
	}

	if c.EnvFile != "" {
		args = append(args, "--env-file", c.EnvFile)
	}

	if c.ImageName != "" {
		args = append(args, flagLabel, LabelImage+"="+c.ImageName)
	}

	for _, m := range mounts(c) {
		args = append(args, flagMount, m)
	}

	return args
}

// mounts lists the --mount values.
func mounts(c Config) []string {
	out := make([]string, 0, 4+len(c.Dirs)+len(c.Volumes))
	out = append(out,
		bind(c.Workspace, c.Workspace),
		bind(c.CfgDir, c.CfgDir),
		bind(c.Home, HomeDir),
		bind(c.OpenCodeDir, OpenCodeTarget),
	)
	for _, d := range c.Dirs {
		out = append(out, bind(d, d))
	}

	for _, v := range c.Volumes {
		out = append(out, csvLine("type=volume", "src="+v.Name, "dst="+v.Target))
	}

	if c.Docker != nil {
		out = append(out, csvLine("type=volume", "src="+SockVolume(c.Name), "dst="+SockDir))
	}

	return out
}

func bind(src, dst string) string {
	return csvLine("type=bind", "src="+src, "dst="+dst)
}

// csvLine joins --mount fields; docker parses them as CSV, so a path with a
// comma or quote gets quoted.
func csvLine(fields ...string) string {
	var b bytes.Buffer

	w := csv.NewWriter(&b)
	_ = w.Write(fields) // a bytes.Buffer never fails
	w.Flush()

	return strings.TrimSuffix(b.String(), "\n")
}

// MountHash is the hash of the create options fixed at creation.
func MountHash(c Config) string { return hash(fixedArgs(c)) }

func hash(args []string) string {
	sum := sha256.Sum256([]byte(strings.Join(args, "\x00")))

	return hex.EncodeToString(sum[:8])
}

// CreateArgv starts the box detached, running sandboxed_agent-idle.
func CreateArgv(c Config) []string {
	argv := runDetached(c.Name, flagLabel, LabelWorkspace+"="+c.Workspace, flagLabel, LabelMounts+"="+MountHash(c))
	argv = append(argv, fixedArgs(c)...)
	argv = append(argv, c.Image)

	heartbeat := ""
	if c.Docker != nil {
		heartbeat = Heartbeat
	}

	return append(argv, IdleArgs(c.Idle, heartbeat)...)
}

// FindArgv prints "<id> <state> <mount hash>" for the box, nothing if absent.
func FindArgv(name string) []string {
	return []string{
		docker, "ps", "-a", "--no-trunc", flagFilter, "name=^/" + name + "$",
		flagFormat, `{{.ID}} {{.State}} {{.Label "` + LabelMounts + `"}}`,
	}
}

// StartArgv starts a stopped box.
func StartArgv(name string) []string { return []string{docker, "start", name} }

// RemoveArgv removes boxes, running or not; absent ones are no error.
func RemoveArgv(names ...string) []string {
	return append([]string{docker, "rm", "-f"}, names...)
}

// ListArgv prints the name of every sandboxed_agent box.
func ListArgv() []string {
	return []string{docker, "ps", "-a", flagFilter, "label=" + LabelWorkspace, flagFormat, "{{.Names}}"}
}

// ExecOpts shape one `docker exec`.
type ExecOpts struct {
	Env         []string // K=V
	Dir         string   // empty: the box's working dir (the workspace)
	User        string   // empty: the box's user
	Interactive bool     // keep stdin open
	TTY         bool
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
}

// ExecArgv runs argv in the box.
func ExecArgv(name string, o ExecOpts, argv []string) []string {
	out := []string{docker, "exec"}
	if o.Interactive {
		out = append(out, "-i")
	}

	if o.TTY {
		out = append(out, "-t")
	}

	if o.User != "" {
		out = append(out, "-u", o.User)
	}

	if o.Dir != "" {
		out = append(out, "-w", o.Dir)
	}

	for _, e := range o.Env {
		out = append(out, "-e", e)
	}

	out = append(out, name)

	return append(out, argv...)
}

// state is what FindArgv reports.
type state struct {
	id, status, hash string
}

func parseState(out string) (state, bool) {
	f := strings.Fields(out)
	if len(f) < 2 {
		return state{}, false
	}

	s := state{id: f[0], status: f[1]}
	if len(f) > 2 {
		s.hash = f[2]
	}

	return s, true
}

// Cmd is one docker invocation.
type Cmd struct {
	Argv   []string
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer
}

// Runner runs a Cmd and fails on a non-zero exit.
type Runner interface {
	Run(ctx context.Context, c Cmd) error
}

// ExitError is the exit code of a process exec'd in the box.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return "exit status " + strconv.Itoa(e.Code) }

// Manager runs the box's docker commands through a Runner. Log receives
// docker's own stderr.
type Manager struct {
	Runner Runner
	Log    io.Writer
}

func (m Manager) output(ctx context.Context, argv []string) (string, error) {
	var out bytes.Buffer
	if err := m.Runner.Run(ctx, Cmd{Argv: argv, Stdout: &out, Stderr: m.Log}); err != nil {
		return "", fmt.Errorf("box: %s %s: %w", argv[0], argv[1], err)
	}

	return out.String(), nil
}

func (m Manager) find(ctx context.Context, name string) (state, bool, error) {
	out, err := m.output(ctx, FindArgv(name))
	if err != nil {
		return state{}, false, err
	}

	s, ok := parseState(out)

	return s, ok, nil
}

// Exec runs argv in the box. A non-zero exit of the process is an *ExitError.
func (m Manager) Exec(ctx context.Context, name string, o ExecOpts, argv []string) error {
	err := m.Runner.Run(ctx, Cmd{Argv: ExecArgv(name, o, argv), Stdin: o.Stdin, Stdout: o.Stdout, Stderr: o.Stderr})

	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return &ExitError{Code: coded.ExitCode()}
	}

	if err != nil {
		return fmt.Errorf("box: exec: %w", err)
	}

	return nil
}

// Remove removes the named boxes.
func (m Manager) Remove(ctx context.Context, names ...string) error {
	_, err := m.output(ctx, RemoveArgv(names...))

	return err
}

// List names every sandboxed_agent box.
func (m Manager) List(ctx context.Context) ([]string, error) {
	out, err := m.output(ctx, ListArgv())

	return strings.Fields(out), err
}
