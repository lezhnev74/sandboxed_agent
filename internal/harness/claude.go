package harness

import (
	"path/filepath"

	"github.com/lezhnev74/sandboxed_agent/internal/bundle/plan"
)

// Input is what a harness builder needs for one run. Paths are as
// seen in the box (the same as on the host: cfg is mounted at its path).
type Input struct {
	ConfigDir   string   // fresh per run
	Model       string   // verbatim, empty: the harness default
	Dirs        []string // extra dirs (--dir), mounted at the same paths
	Bundle      plan.Parts
	Contents    Contents // what a builder embeds instead of pointing at
	Passthrough []string // after --, appended last
	Interactive bool     // the harness's TUI: no prompt, no output format
}

// Contents are bundle parts read by the caller, for a harness that needs
// them inline (OpenCode's generated config and prompt argument).
type Contents struct {
	Prompt    string
	MCP       []byte            // Claude mcpServers JSON
	Providers []byte            // OpenCode provider block
	Agents    map[string]string // agents/*.md by file name
}

// The generated Claude config: bypass mode needs no prompt, and onboarding
// is done, so a fresh config dir never asks anything.
const (
	claudeSettings = `{"skipDangerousModePermissionPrompt":true}` + "\n"
	claudeState    = `{"hasCompletedOnboarding":true,"bypassPermissionsModeAccepted":true}` + "\n"
)

// Claude builds a bare, fully permitted Claude Code run. Never --bare: it
// ignores the OAuth login.
func Claude(a Adapter, in Input) Plan {
	argv := a.base(in)
	if in.Bundle.MCP != "" {
		argv = append(argv, "--mcp-config", in.Bundle.MCP)
	}

	if in.Bundle.System != "" {
		argv = append(argv, "--append-system-prompt-file", in.Bundle.System)
	}

	if in.Bundle.Skills == "" && !in.Interactive {
		argv = append(argv, "--disable-slash-commands")
	}

	p := Plan{
		Argv:  append(argv, in.Passthrough...),
		Env:   a.env(in.ConfigDir),
		Stdin: in.Bundle.Prompt,
		Files: []File{
			{Path: filepath.Join(in.ConfigDir, "settings.json"), Data: claudeSettings},
			{Path: filepath.Join(in.ConfigDir, ".claude.json"), Data: claudeState},
		},
	}

	for _, d := range []struct{ from, name string }{{in.Bundle.Skills, "skills"}, {in.Bundle.Agents, "agents"}} {
		if d.from != "" {
			p.Copies = append(p.Copies, DirCopy{From: d.from, To: filepath.Join(in.ConfigDir, d.name)})
		}
	}

	return p
}
