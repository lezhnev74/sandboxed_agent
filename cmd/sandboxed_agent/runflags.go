package main

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/lezhnev74/sandboxed_agent/internal/paths"
	"github.com/lezhnev74/sandboxed_agent/internal/runs/spec"
)

var errUsage = errors.New("usage")

// runFlags are run's flags, bound to a Spec.
type runFlags struct {
	s          spec.Spec
	promptFile string
	docker     string
}

func newRunFlags() *runFlags {
	return &runFlags{s: spec.Default(), docker: "on"}
}

func (f *runFlags) bind(fs *pflag.FlagSet) {
	s := &f.s
	fs.StringVarP(&s.Model, "model", "m", "", "`MODEL` id, passed to the harness verbatim")
	fs.StringVarP(&s.Prompt, "prompt", "p", "", "prompt `TEXT`")
	fs.StringVarP(&f.promptFile, "file", "f", "", "prompt `FILE`; - reads stdin")
	fs.StringVar(&s.System, "system", "", "system prompt `FILE`, appended to the harness's own")
	fs.StringVar(&s.Ctx, "ctx", "", "context bundle `DIR` (mcp.json, skills/, agents/, ...; default ./"+spec.CtxDirName+")")
	fs.StringArrayVar(&s.Dirs, "dir", nil, "extra read-write `PATH`, mounted at the same path (repeatable)")
	fs.StringVar(&s.Image, "image", "", "run the box on image `TAG` (built by you, FROM sandboxed_agent:latest)")
	fs.StringVar(&s.Dockerfile, "dockerfile", "",
		"build `FILE` (its dir is the context) once and run the box on it; start from `sandboxed_agent dockerfile`")
	fs.StringVar(&f.docker, "docker", "on", "run a dockerd sidecar: `on|off`")
	fs.BoolVar(&s.Fresh, "fresh", false, "throwaway box for this run")
	fs.StringArrayVarP(&s.Env, "env", "e", nil, "extra env var `K=V` (repeatable)")
	fs.DurationVar(&s.Timeout, "timeout", spec.DefaultTimeout, "kill a hung harness after `D` (0: never)")
	fs.BoolVar(&s.JSON, "json", false, "print the result as one JSON object instead of the final message")
	fs.StringVar(&s.LogFile, "log", "", "write the harness's raw JSON log to `FILE` (its dir must exist)")
}

// resolve fills what flags can't: harness, passthrough, prompt, absolute
// paths, and validates the rest.
func (f *runFlags) resolve(cmd *cobra.Command, args []string, d deps) (spec.Spec, error) {
	harness, pass := splitDash(cmd, args)
	if len(harness) != 1 {
		return spec.Spec{}, fmt.Errorf("%w: need exactly one HARNESS (claude or opencode)", errUsage)
	}

	s := f.s
	s.Harness, s.Passthrough = harness[0], pass
	s.Workspace, s.RepoKey = d.cwd, paths.RepoKey(d.cwd)

	if err := f.resolvePrompt(&s, d); err != nil {
		return spec.Spec{}, err
	}

	if err := f.resolveOptions(&s); err != nil {
		return spec.Spec{}, err
	}

	if s.Ctx == "" {
		s.Ctx = spec.CtxDirName
	}

	absPaths(&s, d.cwd)

	return s, nil
}

func (f *runFlags) resolvePrompt(s *spec.Spec, d deps) error {
	switch {
	case f.promptFile == "":
		return nil
	case s.Prompt != "":
		return fmt.Errorf("%w: -p and -f are exclusive", errUsage)
	case f.promptFile == "-":
		b, err := io.ReadAll(d.stdin)
		if err != nil {
			return fmt.Errorf("read prompt from stdin: %w", err)
		}

		s.Prompt = string(b)

		return nil
	default:
		s.PromptFile = absPath(d.cwd, f.promptFile)

		return nil
	}
}

func (f *runFlags) resolveOptions(s *spec.Spec) error {
	if s.Image != "" && s.Dockerfile != "" {
		return fmt.Errorf("%w: --image and --dockerfile are exclusive", errUsage)
	}

	switch f.docker {
	case "on", "off":
		s.Docker = f.docker == "on"
	default:
		return fmt.Errorf("%w: --docker is on or off, got %q", errUsage, f.docker)
	}

	for _, kv := range s.Env {
		if k, _, ok := strings.Cut(kv, "="); !ok || k == "" {
			return fmt.Errorf("%w: -e wants K=V, got %q", errUsage, kv)
		}
	}

	return nil
}

func absPaths(s *spec.Spec, cwd string) {
	for _, p := range []*string{&s.System, &s.Ctx, &s.LogFile, &s.Dockerfile} {
		*p = absPath(cwd, *p)
	}

	for i := range s.Dirs {
		s.Dirs[i] = absPath(cwd, s.Dirs[i])
	}
}

// absPath resolves p against cwd; empty stays empty.
func absPath(cwd, p string) string {
	if p == "" {
		return ""
	}

	if filepath.IsAbs(p) {
		return filepath.Clean(p)
	}

	return filepath.Join(cwd, p)
}

// splitDash separates positional args from what follows --.
func splitDash(cmd *cobra.Command, args []string) (before, after []string) {
	at := cmd.ArgsLenAtDash()
	if at < 0 {
		return args, nil
	}

	return args[:at], args[at:]
}
