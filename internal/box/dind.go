package box

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// LabelSidecar marks a dind sidecar (value: its box).
const LabelSidecar = "sandboxed_agent.sidecar"

// The shared docker socket: dockerd in the sidecar listens on it, the box
// reaches it through the same volume.
const (
	SockDir    = "/run/sandboxed_agent-docker"
	DockerHost = "unix://" + SockDir + "/docker.sock"
)

// dockerReadyScript waits up to 30 s for the sidecar's dockerd.
const dockerReadyScript = `for i in $(seq 1 60); do docker info >/dev/null 2>&1 && exit 0; sleep 0.5; done; ` +
	`echo "dockerd did not start" >&2; exit 1`

// Heartbeat is the file in the shared socket volume that the box's
// sandboxed_agent-idle rewrites every poll while the box runs.
const Heartbeat = SockDir + "/box.alive"

// staleEnv tells the sidecar how old the heartbeat may get, in seconds.
const staleEnv = "SANDBOXED_AGENT_STALE"

// sidecarScript runs dockerd through the dind image's entrypoint and stops
// it once the box's heartbeat is older than $SANDBOXED_AGENT_STALE seconds:
// the box stopped (idle, crashed or `docker stop`), so its sidecar stops
// too. It empties the heartbeat at start; until the box first writes it,
// the box gets 300 s to come up.
const sidecarScript = `beat=` + Heartbeat + `
: > "$beat" && chmod 666 "$beat"
dockerd-entrypoint.sh "$@" & pid=$!
trap 'kill -TERM $pid 2>/dev/null' TERM INT
while kill -0 $pid 2>/dev/null; do
  sleep 5 & wait $!
  limit=300; [ -s "$beat" ] && limit=${` + staleEnv + `:-60}
  age=$(( $(date +%s) - $(stat -c %Y "$beat" 2>/dev/null || echo 0) ))
  [ "$age" -gt "$limit" ] && kill -TERM $pid 2>/dev/null
done
wait $pid`

// Docker is the dind sidecar setup of a box. The box joins the sidecar's
// network namespace, so inner services are on its localhost.
type Docker struct {
	DindImage string
}

// DefaultDocker is the machine-wide setup.
func DefaultDocker() Docker { return Docker{DindImage: "docker:dind"} }

// SidecarName is the dind sidecar of a box.
func SidecarName(box string) string { return box + "-dind" }

// DindVolume holds the sidecar's /var/lib/docker; it outlives the box.
func DindVolume(box string) string {
	return "sandboxed_agent-dind-" + strings.TrimPrefix(box, "sandboxed_agent-")
}

// SockVolume shares the docker socket and the heartbeat between a box and
// its sidecar.
func SockVolume(box string) string { return box + "-sock" }

// sidecarFixed are the sidecar options fixed at creation.
func sidecarFixed(c Config) []string {
	args := []string{
		"--privileged", "--init", "--memory", memory,
		// dind mounts a tmpfs over /tmp unless /tmp is a mountpoint, which
		// would hide workspaces under /tmp.
		"--tmpfs", "/tmp:exec,mode=1777",
		flagMount, csvLine("type=volume", "src="+DindVolume(c.Name), "dst=/var/lib/docker"),
		flagMount, csvLine("type=volume", "src="+SockVolume(c.Name), "dst="+SockDir),
		flagMount, bind(c.Workspace, c.Workspace),
	}
	for _, dir := range c.Dirs {
		args = append(args, flagMount, bind(dir, dir))
	}

	return append(args, c.Docker.DindImage, "sh", "-c", sidecarScript, "sidecar", "dockerd",
		"--host=unix:///var/run/docker.sock", "--host="+DockerHost,
		"--group="+strconv.Itoa(c.GID))
}

// SidecarHash is the hash of the sidecar's creation options.
func SidecarHash(c Config) string { return hash(sidecarFixed(c)) }

// SidecarArgv starts the box's dind sidecar. The heartbeat limit follows
// the box's idle poll; like the idle timeout it only shapes new sidecars.
func SidecarArgv(c Config) []string {
	return runDetached(SidecarName(c.Name), append(
		[]string{
			flagLabel, LabelSidecar + "=" + c.Name, flagLabel, LabelMounts + "=" + SidecarHash(c),
			"-e", staleEnv + "=" + strconv.Itoa(StaleSecs(c.Idle)),
		},
		sidecarFixed(c)...)...)
}

// SockRemoveArgv removes the socket volumes of the boxes; absent ones are no error.
func SockRemoveArgv(boxes ...string) []string {
	argv := make([]string, 0, 4+len(boxes))
	argv = append(argv, docker, "volume", "rm", "-f")
	for _, b := range boxes {
		argv = append(argv, SockVolume(b))
	}

	return argv
}

// wakeArgv starts a stopped box, or restarts a running one whose sidecar
// was (re)started: its network namespace went with the old sidecar.
func wakeArgv(name, status string, sidecarFresh bool) []string {
	switch {
	case status != statusRunning:
		return StartArgv(name)
	case sidecarFresh:
		return []string{docker, "restart", name}
	default:
		return nil
	}
}

// upDocker makes sure the box's sidecar runs and its dockerd answers.
// fresh: the sidecar was not running.
func (m Manager) upDocker(ctx context.Context, c Config, r Reuse) (bool, error) {
	if err := ensureSources(c); err != nil {
		return false, err
	}

	fresh, err := m.running(ctx, SidecarName(c.Name), SidecarArgv(c), sidecarCheck(c, r))
	if err != nil {
		return false, err
	}

	return fresh, m.waitDocker(ctx, SidecarName(c.Name))
}

// waitDocker waits for the sidecar's dockerd. The exec itself is retried:
// once, right after a start, it failed with 128 and no output.
func (m Manager) waitDocker(ctx context.Context, sidecar string) error {
	var err error
	for range 3 {
		if err = m.Exec(ctx, sidecar, ExecOpts{Stderr: m.Log}, []string{"sh", "-c", dockerReadyScript}); err == nil {
			return nil
		}
	}

	return err
}

// sidecarCheck refuses a sidecar created with other options, like the box.
func sidecarCheck(c Config, r Reuse) func(state) error {
	return func(s state) error {
		if r == SameMounts && s.hash != SidecarHash(c) {
			return fmt.Errorf("%w: %s was created with other mounts or options; run `sandboxed_agent down` and retry",
				ErrMountsChanged, SidecarName(c.Name))
		}

		return nil
	}
}

// running makes sure the named container runs, creating it with create
// when absent; check vets an existing one. fresh: it was not running.
func (m Manager) running(ctx context.Context, name string, create []string, check func(state) error) (bool, error) {
	s, ok, err := m.find(ctx, name)
	if err != nil {
		return false, err
	}

	if !ok {
		_, cerr := m.output(ctx, create)
		if cerr == nil {
			return true, nil
		}

		// Another agent may have created it first.
		if s, ok, _ = m.find(ctx, name); !ok {
			return false, cerr
		}
	}

	if err = check(s); err != nil {
		return false, err
	}

	if s.status == statusRunning {
		return false, nil
	}

	_, err = m.output(ctx, StartArgv(name))

	return true, err
}

// Down removes the boxes, their sidecars and socket volumes. The dind volumes stay (that's `sandboxed_agent gc`).
func (m Manager) Down(ctx context.Context, boxes ...string) error {
	names := make([]string, 0, 2*len(boxes))
	for _, b := range boxes {
		names = append(names, b, SidecarName(b))
	}

	if err := m.Remove(ctx, names...); err != nil {
		return err
	}

	_, err := m.output(ctx, SockRemoveArgv(boxes...))

	return err
}
