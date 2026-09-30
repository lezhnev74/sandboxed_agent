// Package bundle copies a run's context bundle into its cfg dir and writes
// the harness config files. What to copy is decided by bundle/plan and
// harness; this package only touches the filesystem.
package bundle

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/lezhnev74/sandboxed_agent/internal/bundle/plan"
	"github.com/lezhnev74/sandboxed_agent/internal/harness"
)

const (
	dirMode  = 0o750
	fileMode = 0o600
)

// Resolve picks the bundle parts, checking the real filesystem.
func Resolve(f plan.Flags) (plan.Sources, error) {
	return plan.Resolve(f, func(p string) bool { //nolint:wrapcheck // plan errors name the path
		_, err := os.Stat(p)

		return err == nil
	})
}

// Copy replaces dir with a fresh copy of s and returns where each part
// landed. It runs once per run, before the harness.
func Copy(s plan.Sources, dir string) (plan.Parts, error) {
	if err := reset(dir); err != nil {
		return plan.Parts{}, err
	}

	parts := plan.Layout(dir, s)

	if s.PromptText != "" {
		if err := writeFile(parts.Prompt, s.PromptText); err != nil {
			return plan.Parts{}, err
		}
	}

	err := copyDirs([]harness.DirCopy{
		{From: s.Prompt, To: parts.Prompt}, {From: s.System, To: parts.System}, {From: s.MCP, To: parts.MCP},
		{From: s.Skills, To: parts.Skills}, {From: s.Agents, To: parts.Agents},
	})
	if err != nil {
		return plan.Parts{}, err
	}

	return parts, nil
}

// Apply writes a harness plan's generated files and copies its dirs.
func Apply(files []harness.File, copies []harness.DirCopy) error {
	for _, f := range files {
		if err := writeFile(f.Path, f.Data); err != nil {
			return err
		}
	}

	return copyDirs(copies)
}

// copyDirs replaces each To with a copy of its From; an empty From is skipped.
func copyDirs(copies []harness.DirCopy) error {
	for _, c := range copies {
		if c.From == "" {
			continue
		}

		if err := copyTree(c.From, c.To); err != nil {
			return err
		}
	}

	return nil
}

// reset makes dir an empty directory.
func reset(dir string) error {
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("bundle: %w", err)
	}

	if err := os.MkdirAll(dir, dirMode); err != nil {
		return fmt.Errorf("bundle: %w", err)
	}

	return nil
}

// writeFile writes data to path, creating its parent dirs.
func writeFile(path, data string) error {
	if err := os.MkdirAll(filepath.Dir(path), dirMode); err != nil {
		return fmt.Errorf("bundle: %w", err)
	}

	if err := os.WriteFile(path, []byte(data), fileMode); err != nil {
		return fmt.Errorf("bundle: %w", err)
	}

	return nil
}

// copyTree replaces dst with a copy of the file or directory tree src.
func copyTree(src, dst string) error {
	if err := os.RemoveAll(dst); err != nil {
		return fmt.Errorf("bundle: %w", err)
	}

	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(src, p)
		if err != nil {
			return fmt.Errorf("rel: %w", err)
		}

		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, dirMode)
		}

		return copyFile(p, to)
	})
	if err != nil {
		return fmt.Errorf("bundle copy %s: %w", src, err)
	}

	return nil
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err //nolint:wrapcheck // wrapped by copyTree
	}

	if err := os.MkdirAll(filepath.Dir(dst), dirMode); err != nil {
		return err //nolint:wrapcheck // wrapped by copyTree
	}

	return os.WriteFile(dst, data, fileMode) //nolint:gosec,wrapcheck // dst is under the run dir we chose; wrapped by copyTree
}
