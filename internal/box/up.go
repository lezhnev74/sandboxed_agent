package box

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lezhnev74/sandboxed_agent/internal/paths"
)

// Reuse says how Up treats an existing box.
type Reuse int

// The reuse modes.
const (
	// SameMounts reuses a box only if it was created with the same options.
	SameMounts Reuse = iota
	// AnyMounts reuses whatever box exists (shell and TUIs need no mounts
	// of their own).
	AnyMounts
)

// Up finds the box or creates it, starts it if stopped and returns its id.
// With c.Docker, the dind sidecar comes first.
func (m Manager) Up(ctx context.Context, c Config, r Reuse) (string, error) {
	fresh := false
	if c.Docker != nil {
		var err error
		if fresh, err = m.upDocker(ctx, c, r); err != nil {
			return "", err
		}
	}

	s, ok, err := m.find(ctx, c.Name)
	if err != nil {
		return "", err
	}

	if ok {
		return m.reuse(ctx, c, s, r, fresh)
	}

	return m.create(ctx, c, r)
}

func (m Manager) reuse(ctx context.Context, c Config, s state, r Reuse, sidecarFresh bool) (string, error) {
	if r == SameMounts && s.hash != MountHash(c) {
		return "", fmt.Errorf("%w: %s was created with other mounts or options; run `sandboxed_agent down` and retry",
			ErrMountsChanged, c.Name)
	}

	if argv := wakeArgv(c.Name, s.status, sidecarFresh); argv != nil {
		if _, err := m.output(ctx, argv); err != nil {
			return "", err
		}
	}

	return s.id, nil
}

func (m Manager) create(ctx context.Context, c Config, r Reuse) (string, error) {
	if err := ensureSources(c); err != nil {
		return "", err
	}

	out, err := m.output(ctx, CreateArgv(c))
	if err == nil {
		return strings.TrimSpace(out), m.ownVolumes(ctx, c)
	}

	// Another agent may have created the same box first.
	s, ok, ferr := m.find(ctx, c.Name)
	if ferr != nil || !ok {
		return "", err
	}

	return m.reuse(ctx, c, s, r, false)
}

// ownVolumes hands the named volumes to the box user: docker creates them
// root-owned.
func (m Manager) ownVolumes(ctx context.Context, c Config) error {
	if len(c.Volumes) == 0 {
		return nil
	}

	argv := []string{"chown", strconv.Itoa(c.UID) + ":" + strconv.Itoa(c.GID)}
	for _, v := range c.Volumes {
		argv = append(argv, v.Target)
	}

	return m.Exec(ctx, c.Name, ExecOpts{User: "0", Stderr: m.Log}, argv)
}

// ensureSources creates every bind source, and the mount points in the box
// home (OpenCode data, volumes): docker would create them as root-owned dirs.
func ensureSources(c Config) error {
	dirs := append([]string{c.Workspace, c.CfgDir, c.Home, c.OpenCodeDir, inHome(c, OpenCodeTarget)}, c.Dirs...)

	for _, v := range c.Volumes {
		if strings.HasPrefix(v.Target, HomeDir+"/") {
			dirs = append(dirs, inHome(c, v.Target))
		}
	}

	for _, d := range dirs {
		if err := paths.EnsureDir(d); err != nil {
			return fmt.Errorf("box: %w", err)
		}
	}

	return nil
}

// inHome is the host path of target, a path under HomeDir in the box.
func inHome(c Config, target string) string {
	return filepath.Join(c.Home, strings.TrimPrefix(target, HomeDir))
}
