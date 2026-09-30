// Package image builds the sandboxed_agent image from the Dockerfile and helper
// scripts embedded in the binary. The docker argv is decided here by pure
// functions; run_exec.go runs it.
package image

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"strconv"
	"strings"
)

// Tag is the image every box runs unless --image names another.
const Tag = "sandboxed_agent:latest"

// Labels record the harness versions captured at build time.
const (
	LabelClaude   = "sandboxed_agent.claude.version"
	LabelOpenCode = "sandboxed_agent.opencode.version"
	// LabelAssets is the hash of the embedded build context: an image
	// built from other assets (an older agent binary) is rebuilt.
	LabelAssets = "sandboxed_agent.assets"
)

// docker is the CLI every command runs.
const docker = "docker"

// verbBuild is `docker build`.
const verbBuild = "build"

// ErrVersions means the built image didn't report both harness versions.
var ErrVersions = errors.New("image: harness versions not reported")

//go:embed assets
var assets embed.FS

// Options are the build inputs.
type Options struct {
	Tag        string // default Tag
	UID, GID   int    // the host user the box runs as
	HarnessRev string // non-empty busts the harness layer's cache
}

func (o Options) tag() string { return tagOr(o.Tag) }

func tagOr(tag string) string {
	if tag == "" {
		return Tag
	}

	return tag
}

// Versions are the harness versions baked into an image.
type Versions struct {
	Claude   string
	OpenCode string
}

// WriteContext writes the build context (Dockerfile and scripts) as a tar.
func WriteContext(w io.Writer) error {
	tw := tar.NewWriter(w)

	entries, err := fs.ReadDir(assets, "assets")
	if err != nil {
		return fmt.Errorf("image context: %w", err)
	}

	for _, e := range entries {
		if err := addFile(tw, e.Name()); err != nil {
			return err
		}
	}

	if err := tw.Close(); err != nil {
		return fmt.Errorf("image context: %w", err)
	}

	return nil
}

// AssetsHash is the hash of the build context this binary embeds.
func AssetsHash() string {
	h := sha256.New()
	_ = WriteContext(h) // the embedded assets always read; a hash never fails to write

	return hex.EncodeToString(h.Sum(nil)[:8])
}

func addFile(tw *tar.Writer, name string) error {
	body, err := assets.ReadFile("assets/" + name)
	if err != nil {
		return fmt.Errorf("image context: %w", err)
	}

	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: int64(assetMode(name)), Size: int64(len(body))}); err != nil {
		return fmt.Errorf("image context: %w", err)
	}

	if _, err := tw.Write(body); err != nil {
		return fmt.Errorf("image context: %w", err)
	}

	return nil
}

// assetMode is the file mode of an embedded asset: the scripts are
// executable.
func assetMode(name string) fs.FileMode {
	if name == "Dockerfile" {
		return 0o644
	}

	return 0o755
}

const flagBuildArg = "--build-arg"

// userArgs pass the host user the box runs as.
func (o Options) userArgs() []string {
	return []string{flagBuildArg, "UID=" + strconv.Itoa(o.UID), flagBuildArg, "GID=" + strconv.Itoa(o.GID)}
}

// BuildArgv builds the image from a tar context on stdin.
func BuildArgv(o Options) []string {
	argv := append([]string{docker, verbBuild, "-t", o.tag()}, o.userArgs()...)
	if o.HarnessRev != "" {
		argv = append(argv, flagBuildArg, "HARNESS_REV="+o.HarnessRev)
	}

	return append(argv, "-")
}

// VersionsArgv prints both harness versions from inside the image.
func VersionsArgv(tag string) []string {
	return []string{
		docker, "run", "--rm", "--entrypoint", "sh", tagOr(tag),
		"-c", "claude --version && opencode --version",
	}
}

// LabelArgv re-tags the image with the version labels; the Dockerfile
// ("FROM <tag>") comes on stdin, so the step needs no context.
func LabelArgv(tag string, v Versions) []string {
	const label = "--label"

	return []string{
		docker, "build", "-t", tagOr(tag),
		label, LabelClaude + "=" + v.Claude,
		label, LabelOpenCode + "=" + v.OpenCode,
		label, LabelAssets + "=" + AssetsHash(),
		"-",
	}
}

// ExistsArgv prints the image's LabelAssets and fails when it is missing.
func ExistsArgv(tag string) []string {
	return []string{docker, "image", "inspect", "--format", `{{index .Config.Labels "` + LabelAssets + `"}}`, tag}
}

// ParseVersions reads `claude --version` then `opencode --version` output:
// "2.1.284 (Claude Code)" and "opencode v2.0.10" (or bare versions).
func ParseVersions(out string) (Versions, error) {
	var lines []string

	for l := range strings.Lines(out) {
		if f := strings.Fields(l); len(f) > 0 {
			lines = append(lines, l)
		}
	}

	if len(lines) != 2 {
		return Versions{}, fmt.Errorf("%w: %q", ErrVersions, out)
	}

	claude := strings.Fields(lines[0])[0]
	oc := strings.Fields(lines[1])

	return Versions{Claude: claude, OpenCode: strings.TrimPrefix(oc[len(oc)-1], "v")}, nil
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

// Builder builds and finds the image through a Runner. Log receives docker's
// build output.
type Builder struct {
	Runner Runner
	Log    io.Writer
}

// Ensure builds the image when it is missing or was built from other assets.
func (b Builder) Ensure(ctx context.Context, o Options) error {
	var out bytes.Buffer

	err := b.Runner.Run(ctx, Cmd{Argv: ExistsArgv(o.tag()), Stdout: &out, Stderr: io.Discard})
	if err == nil && strings.TrimSpace(out.String()) == AssetsHash() {
		return nil
	}

	_, err = b.Build(ctx, o)

	return err
}

// Build builds the image, reads the harness versions from it and labels it
// with them.
func (b Builder) Build(ctx context.Context, o Options) (Versions, error) {
	var tarball bytes.Buffer
	if err := WriteContext(&tarball); err != nil {
		return Versions{}, err
	}

	if err := b.run(ctx, BuildArgv(o), &tarball, b.Log); err != nil {
		return Versions{}, err
	}

	var out bytes.Buffer
	if err := b.run(ctx, VersionsArgv(o.tag()), nil, &out); err != nil {
		return Versions{}, err
	}

	v, err := ParseVersions(out.String())
	if err != nil {
		return Versions{}, err
	}

	from := strings.NewReader("FROM " + o.tag() + "\n")
	if err := b.run(ctx, LabelArgv(o.tag(), v), from, b.Log); err != nil {
		return Versions{}, err
	}

	return v, nil
}

func (b Builder) run(ctx context.Context, argv []string, stdin io.Reader, stdout io.Writer) error {
	if err := b.Runner.Run(ctx, Cmd{Argv: argv, Stdin: stdin, Stdout: stdout, Stderr: b.Log}); err != nil {
		return fmt.Errorf("image: %s %s: %w", argv[0], argv[1], err)
	}

	return nil
}

// ErrNoImage is returned for an --image TAG that is not built.
var ErrNoImage = errors.New("image: not found; build it (FROM " + Tag + ") first")

// Exists fails with ErrNoImage when the image tag is missing.
func (b Builder) Exists(ctx context.Context, tag string) error {
	if err := b.Runner.Run(ctx, Cmd{Argv: ExistsArgv(tag), Stdout: io.Discard, Stderr: io.Discard}); err != nil {
		return fmt.Errorf("%w: %s", ErrNoImage, tag)
	}

	return nil
}
