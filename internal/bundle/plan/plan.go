// Package plan decides which context-bundle parts a run uses: the --ctx
// directory's files plus the prompt (-p or -f) and the system prompt
// (--system), and where each part lands in the run's bundle copy.
package plan

import (
	"errors"
	"fmt"
	"path/filepath"
)

// ErrMissing is returned for a -f or --system file that doesn't exist.
var ErrMissing = errors.New("bundle part not found")

// The bundle's file names, in --ctx and in the run's copy. The prompt and
// the system prompt come from flags only; they exist in the copy alone.
const (
	PromptName = "prompt.md"
	SystemName = "system.md"
	MCPName    = "mcp.json"
	SkillsName = "skills"
	AgentsName = "agents"
	// ProvidersName names OpenCode's provider block, merged into its config.
	ProvidersName = "providers.json"
	// MiseName names the bundle's toolchains, used when the repo names none.
	MiseName = "mise.toml"
)

// Flags are the bundle options as given. Paths are absolute.
type Flags struct {
	Ctx        string
	Prompt     string // inline text (-p, or -f - read once)
	PromptFile string
	System     string // system prompt file (--system)
}

// Sources are the parts a run uses: host paths, empty when absent.
// PromptText, when set, is the prompt and Prompt is empty.
type Sources struct {
	PromptText string
	Prompt     string
	System     string
	MCP        string
	Skills     string
	Agents     string
	Providers  string // --ctx only, no flag
}

// Parts are the paths of the parts in a bundle copy, empty when absent.
type Parts struct {
	Prompt    string
	System    string
	MCP       string
	Skills    string
	Agents    string
	Providers string
}

// Resolve picks the prompts and f.Ctx's parts. A --ctx file is used only if
// it exists; a -f or --system file that doesn't exist is an error.
func Resolve(f Flags, exists func(string) bool) (Sources, error) {
	fromCtx := func(name string) string {
		if p := filepath.Join(f.Ctx, name); f.Ctx != "" && exists(p) {
			return p
		}

		return ""
	}

	s := Sources{
		PromptText: f.Prompt, System: f.System,
		MCP: fromCtx(MCPName), Skills: fromCtx(SkillsName), Agents: fromCtx(AgentsName), Providers: fromCtx(ProvidersName),
	}

	if s.PromptText == "" {
		s.Prompt = f.PromptFile
	}

	for _, p := range []string{s.Prompt, s.System} {
		if p != "" && !exists(p) {
			return Sources{}, fmt.Errorf("%w: %s", ErrMissing, p)
		}
	}

	return s, nil
}

// Layout is where s's parts land in the bundle copy at dir.
func Layout(dir string, s Sources) Parts {
	at := func(src, name string) string {
		if src == "" {
			return ""
		}

		return filepath.Join(dir, name)
	}

	prompt := s.Prompt
	if s.PromptText != "" {
		prompt = s.PromptText
	}

	return Parts{
		Prompt: at(prompt, PromptName),
		System: at(s.System, SystemName),
		MCP:    at(s.MCP, MCPName),
		Skills: at(s.Skills, SkillsName),
		Agents: at(s.Agents, AgentsName),

		Providers: at(s.Providers, ProvidersName),
	}
}

// HasPrompt reports whether s has a prompt from any source.
func (s Sources) HasPrompt() bool { return s.PromptText != "" || s.Prompt != "" }
