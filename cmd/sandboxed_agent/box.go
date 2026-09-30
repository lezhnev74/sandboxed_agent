package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/lezhnev74/sandboxed_agent/internal/box"
	"github.com/lezhnev74/sandboxed_agent/internal/gitx"
	"github.com/lezhnev74/sandboxed_agent/internal/image"
	"github.com/lezhnev74/sandboxed_agent/internal/paths"
	"github.com/lezhnev74/sandboxed_agent/internal/runs/spec"
)

// host is what the box commands take from the process.
type host struct {
	dirs     paths.Dirs
	uid, gid int
	tty      bool // stdin is a terminal
	stdin    io.Reader
	stdout   io.Writer
	stderr   io.Writer

	idle time.Duration // box idle timeout (SANDBOXED_AGENT_IDLE_TIMEOUT); zero is the default
}

// boxOpts are the per-run choices that shape a new box.
type boxOpts struct {
	name   string // empty: the workspace's box; set: a --fresh run's own
	dirs   []string
	reuse  box.Reuse
	docker bool   // a dind sidecar (--docker on)
	image  string // --image TAG; empty: the default image
	// dockerfile is --dockerfile FILE, built into the image the box runs.
	dockerfile string
}

// runBoxOpts is the box a run of s needs.
func runBoxOpts(s spec.Spec) boxOpts {
	o := boxOpts{dirs: s.Dirs, reuse: box.SameMounts, docker: s.Docker, image: s.Image, dockerfile: s.Dockerfile}
	if s.Fresh {
		o.name = paths.FreshBoxName(s.RepoKey, s.RunID)
	}

	return o
}

func (h host) manager() box.Manager { return box.Manager{Runner: box.Exec{}, Log: h.stderr} }

// up makes sure the workspace's box runs: the image, the generated git
// config, then the box itself. It returns the box name.
func (h host) up(ctx context.Context, workspace string, o boxOpts) (string, error) {
	tag, err := h.ensureImage(ctx, o.image, o.dockerfile)
	if err != nil {
		return "", err
	}

	if o.dockerfile != "" {
		o.image = tag // the box records it, as an --image TAG's
	}

	c, err := h.boxConfig(ctx, workspace, o)
	if err != nil {
		return "", err
	}

	c.Image = tag

	if _, err = h.manager().Up(ctx, c, o.reuse); err != nil {
		return "", err //nolint:wrapcheck // box errors already say what to do
	}

	return c.Name, nil
}

// ensureImage builds the sandboxed_agent image when missing or stale (an
// --image TAG or a --dockerfile may build FROM it), then builds the
// --dockerfile unless built, or checks the --image TAG exists. It returns
// the tag the box runs.
func (h host) ensureImage(ctx context.Context, tag, dockerfile string) (string, error) {
	b := image.Builder{Runner: image.Exec{}, Log: h.stderr}
	o := image.Options{UID: h.uid, GID: h.gid}

	if err := b.Ensure(ctx, o); err != nil {
		return "", err //nolint:wrapcheck // image errors already name the failing docker step
	}

	if dockerfile != "" {
		return b.EnsureFile(ctx, o, dockerfile) //nolint:wrapcheck // image errors already name the failing docker step
	}

	if tag == "" {
		return image.Tag, nil
	}

	return tag, b.Exists(ctx, tag) //nolint:wrapcheck // names the tag
}

// discard removes a --fresh run's box. A failure is only reported: the
// run's own outcome stands.
func (h host) discard(ctx context.Context, name string) {
	if err := h.manager().Discard(context.WithoutCancel(ctx), name); err != nil {
		_, _ = fmt.Fprintln(h.stderr, "sandboxed_agent: removing the --fresh box:", err)
	}
}

func (h host) boxConfig(ctx context.Context, workspace string, o boxOpts) (box.Config, error) {
	key := paths.RepoKey(workspace)
	cfg := h.dirs.Cfg(key)
	gitcfg := filepath.Join(cfg, "gitconfig")

	if err := prepareGit(ctx, workspace, cfg, gitcfg); err != nil {
		return box.Config{}, err
	}

	name := o.name
	if name == "" {
		name = paths.BoxName(key)
	}

	c := box.Config{
		Name: name, Image: image.Tag, ImageName: o.image, Workspace: workspace, UID: h.uid, GID: h.gid,
		CfgDir: cfg, Home: h.dirs.BoxHome(), OpenCodeDir: h.dirs.OpenCode(), Dirs: o.dirs, GitConfig: gitcfg,
		Volumes: box.Volumes(), Idle: h.idle,
	}

	if o.docker {
		d := box.DefaultDocker()
		c.Docker = &d
	}

	if _, err := os.Stat(h.dirs.EnvFile()); err == nil {
		c.EnvFile = h.dirs.EnvFile()
	}

	return c, nil
}

// prepareGit writes the box's git config.
func prepareGit(ctx context.Context, workspace, cfg, gitcfg string) error {
	if err := paths.EnsureDir(cfg); err != nil {
		return err //nolint:wrapcheck // names the dir
	}

	return gitx.WriteConfig(gitcfg, gitx.ReadIdentity(ctx, workspace)) //nolint:wrapcheck // names itself
}

// shell opens bash in the workspace's box, creating it with defaults if absent.
func (h host) shell(ctx context.Context, workspace string) error {
	name, err := h.up(ctx, workspace, boxOpts{reuse: box.AnyMounts, docker: true})
	if err != nil {
		return err
	}

	o := box.ExecOpts{Interactive: true, TTY: h.tty, Dir: workspace, Stdin: h.stdin, Stdout: h.stdout, Stderr: h.stderr}

	return h.manager().Exec(ctx, name, o, []string{"bash", "-l"}) //nolint:wrapcheck // the shell's exit code
}

// down removes the workspace's box and sidecar, or every sandboxed_agent box.
func (h host) down(ctx context.Context, workspace string, all bool) error {
	m := h.manager()
	names := []string{paths.BoxName(paths.RepoKey(workspace))}

	if all {
		var err error
		if names, err = m.List(ctx); err != nil {
			return err //nolint:wrapcheck // names itself
		}
	}

	if len(names) == 0 {
		return nil
	}

	return m.Down(ctx, names...) //nolint:wrapcheck // names itself
}

// exitCode is the exit code a process in the box ended with, if err is one.
func exitCode(err error) (int, bool) {
	var ee *box.ExitError
	if errors.As(err, &ee) {
		return ee.Code, true
	}

	return 0, false
}
