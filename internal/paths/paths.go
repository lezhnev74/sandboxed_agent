// Package paths names every host directory, container and run id sandboxed_agent
// uses. It reads nothing from the environment: the caller injects Env.
package paths

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const app = "sandboxed_agent"

// Env is the part of the host environment that decides the state dirs.
type Env struct {
	Home       string // $HOME
	DataHome   string // $XDG_DATA_HOME, may be empty
	ConfigHome string // $XDG_CONFIG_HOME, may be empty
}

// Dirs is the resolved sandboxed_agent state (Data) and config (Config) roots.
type Dirs struct {
	Data   string
	Config string
}

// New resolves the roots per the XDG base directory spec.
func New(env Env) Dirs {
	return Dirs{
		Data:   filepath.Join(xdg(env.DataHome, env.Home, ".local/share"), app),
		Config: filepath.Join(xdg(env.ConfigHome, env.Home, ".config"), app),
	}
}

// xdg returns val, or home/fallback when val is unset or relative (the spec
// says relative values are invalid).
func xdg(val, home, fallback string) string {
	if filepath.IsAbs(val) {
		return val
	}

	return filepath.Join(home, fallback)
}

// Lock is the workspace lock file: one run per workspace.
func (d Dirs) Lock(repoKey string) string { return filepath.Join(d.Data, "locks", repoKey+".lock") }

// CfgRoot holds the cfg dir of every repo.
func (d Dirs) CfgRoot() string { return filepath.Join(d.Data, "cfg") }

// Cfg is mounted into the repo's box at the same path.
func (d Dirs) Cfg(repoKey string) string { return filepath.Join(d.CfgRoot(), repoKey) }

// RunCfg holds one run's harness config, bundle copy, pgid file and, without
// --log, its raw log. The run removes it when it ends.
func (d Dirs) RunCfg(repoKey, runID string) string { return filepath.Join(d.Cfg(repoKey), runID) }

// BoxHome is mounted as /home/agent.
func (d Dirs) BoxHome() string { return filepath.Join(d.Data, "home") }

// ClaudeAuth holds the stored Claude login and its lock.
func (d Dirs) ClaudeAuth() string { return filepath.Join(d.Data, "claude-auth") }

// OpenCode is the OpenCode data dir (auth.json, sessions).
func (d Dirs) OpenCode() string { return filepath.Join(d.Data, "opencode") }

// EnvFile holds provider keys, passed to the box as --env-file.
func (d Dirs) EnvFile() string { return filepath.Join(d.Config, "env") }

// RepoKey is "<basename>-<first 8 hex of sha256(abs path)>". The basename is
// reduced to characters valid in a container name.
func RepoKey(absPath string) string {
	clean := filepath.Clean(absPath)
	sum := sha256.Sum256([]byte(clean))

	return sanitize(filepath.Base(clean)) + "-" + hex.EncodeToString(sum[:4])
}

func sanitize(base string) string {
	if base == string(filepath.Separator) {
		return "root"
	}

	return strings.Map(func(r rune) rune {
		if isNameRune(r) {
			return r
		}

		return '-'
	}, base)
}

func isNameRune(r rune) bool {
	return r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r) || strings.ContainsRune("_.-", r))
}

// BoxName is the repo's long-lived box container.
func BoxName(repoKey string) string { return app + "-" + repoKey }

// FreshBoxName is a --fresh run's throwaway box.
func FreshBoxName(repoKey, runID string) string { return BoxName(repoKey + "-" + runID) }

// SidecarName is the box's dind sidecar container.
func SidecarName(repoKey string) string { return BoxName(repoKey) + "-dind" }

// NewRunID is "YYYYMMDD-HHMMSS-<4 hex>" in UTC, the hex read from random.
func NewRunID(at time.Time, random io.Reader) (string, error) {
	var b [2]byte
	if _, err := io.ReadFull(random, b[:]); err != nil {
		return "", fmt.Errorf("run id: %w", err)
	}

	return at.UTC().Format(runIDTime) + "-" + hex.EncodeToString(b[:]), nil
}

// runIDTime is the time part of a run id.
const runIDTime = "20060102-150405"

// IsRunID says whether name has the shape NewRunID gives.
func IsRunID(name string) bool {
	n := len(runIDTime)
	if len(name) != n+5 || name[n] != '-' {
		return false
	}

	if _, err := time.Parse(runIDTime, name[:n]); err != nil {
		return false
	}

	_, err := hex.DecodeString(name[n+1:])

	return err == nil
}

// EnsureDir creates dir and its parents. Docker creates a missing bind
// source as a root-owned dir, so every mount source goes through here first.
func EnsureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("ensure dir: %w", err)
	}

	return nil
}
