package driver

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/lezhnev74/sandboxed_agent/internal/box"
	"github.com/lezhnev74/sandboxed_agent/internal/paths"
)

// toolchainFiles in a repo make mise install its toolchains. go.mod counts
// through mise's idiomatic version files (enabled in the image).
var toolchainFiles = []string{"mise.toml", ".mise.toml", ".tool-versions", "go.mod"}

// miseGlobalEnv points mise at the bundle's mise.toml when the repo has none.
const miseGlobalEnv = "MISE_GLOBAL_CONFIG_FILE"

// Toolchains installs a run's toolchains with `mise install` before its
// harness.
type Toolchains struct {
	Box        Box
	BoxName    string
	Workspace  string
	BundleMise string // the bundle's mise.toml on the host; "" when none
	RunCfg     string // mounted at the same path in the box
	Log        io.Writer
}

// toolchainPlan says whether mise has anything to install, and whether it
// comes from the bundle's mise.toml (used only when the repo names none).
func toolchainPlan(workspace, bundleMise string, exists func(string) bool) (install, fromBundle bool) {
	for _, f := range toolchainFiles {
		if exists(filepath.Join(workspace, f)) {
			return true, false
		}
	}

	ok := bundleMise != "" && exists(bundleMise)

	return ok, ok
}

// Install runs `mise install` in the workspace when the repo or the bundle
// names toolchains. It returns the env the harness needs to see them.
func (t Toolchains) Install(ctx context.Context) ([]string, error) {
	install, fromBundle := toolchainPlan(t.Workspace, t.BundleMise, fileExists)
	if !install {
		return nil, nil
	}

	var env []string

	if fromBundle {
		cfg, err := t.copyBundleMise()
		if err != nil {
			return nil, err
		}

		env = []string{miseGlobalEnv + "=" + cfg}
	}

	o := box.ExecOpts{Env: env, Dir: t.Workspace, Stdout: t.Log, Stderr: t.Log}
	if err := t.Box.Exec(ctx, t.BoxName, o, []string{"mise", "install"}); err != nil {
		return nil, fmt.Errorf("toolchains: mise install: %w", err)
	}

	return env, nil
}

// copyBundleMise copies the bundle's mise.toml into the run's config dir,
// which the box sees.
func (t Toolchains) copyBundleMise() (string, error) {
	body, err := os.ReadFile(t.BundleMise)
	if err != nil {
		return "", fmt.Errorf("toolchains: %w", err)
	}

	if err = paths.EnsureDir(t.RunCfg); err != nil {
		return "", fmt.Errorf("toolchains: %w", err)
	}

	dst := filepath.Join(t.RunCfg, "mise.toml")
	if err = os.WriteFile(dst, body, 0o600); err != nil { //nolint:gosec // dst is under the run dir we chose
		return "", fmt.Errorf("toolchains: %w", err)
	}

	return dst, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)

	return err == nil
}
