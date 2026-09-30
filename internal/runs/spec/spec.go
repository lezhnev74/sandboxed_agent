// Package spec is a run's resolved options: the CLI fills it and the run
// executes it.
package spec

import "time"

// Spec holds every resolved run option. Paths are absolute.
type Spec struct {
	Harness   string
	Model     string // passed to the harness verbatim
	Workspace string
	RepoKey   string
	RunID     string

	// Prompt is inline text (-p, or -f - read from stdin). PromptFile is
	// copied into the run's bundle.
	Prompt     string
	PromptFile string
	System     string // system prompt file (--system)

	Ctx  string // context bundle dir; defaults to the workspace's CtxDirName
	Dirs []string

	Image       string
	Dockerfile  string // built into a CustomTag image; exclusive with Image
	Docker      bool
	Fresh       bool
	Env         []string // K=V
	Passthrough []string // after --

	Timeout time.Duration // kills a hung harness; zero is none
	JSON    bool          // print the result as one JSON object
	LogFile string        // the harness's raw JSON log; "" keeps it in the run's cfg dir only
}

// CtxDirName is the workspace dir used as --ctx when none is given.
const CtxDirName = ".sandboxed_agent"

// DefaultTimeout is --timeout's default.
const DefaultTimeout = 60 * time.Minute

// Default is a Spec with every default set.
func Default() Spec {
	return Spec{Docker: true, Timeout: DefaultTimeout}
}
