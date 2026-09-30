package harness

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/bundle/plan"
)

func opencode(t *testing.T) Adapter {
	t.Helper()

	tbl, err := Load()
	require.NoError(t, err)

	a, err := tbl.Get("opencode")
	require.NoError(t, err)

	return a
}

const ocMCP = `{"mcpServers": {
  "mem": {"command": "npx", "args": ["-y", "@mcp/memory"], "env": {"K": "v"}},
  "plain": {"type": "stdio", "command": "srv"},
  "web": {"type": "http", "url": "https://mcp.example/mcp", "headers": {"Authorization": "Bearer x"}},
  "old": {"type": "sse", "url": "https://mcp.example/sse"}
}}`

const ocAgent = `---
name: reviewer
description: "Reviews diffs"
tools: Read, Grep
model: sonnet
---

Review the diff.
`

func TestOpenCodeGolden(t *testing.T) {
	const cfg = "/cfg/repo-1234/20260929-120000-abcd/opencode"

	const b = "/cfg/repo-1234/20260929-120000-abcd/bundle"

	tests := map[string]Input{
		"opencode_minimal": {ConfigDir: cfg, Bundle: plan.Parts{Prompt: b + "/prompt.md"}, Contents: Contents{Prompt: "fix it"}},
		"opencode_full": {
			ConfigDir: cfg, Model: "openrouter/qwen/qwen3-coder#high",
			Bundle: plan.Parts{
				Prompt: b + "/prompt.md", System: b + "/system.md", MCP: b + "/mcp.json",
				Skills: b + "/skills", Agents: b + "/agents", Providers: b + "/providers.json",
			},
			Contents: Contents{
				Prompt: "-starts with a dash", MCP: []byte(ocMCP),
				Providers: []byte(`{"openrouter": {"options": {"baseURL": "https://openrouter.ai/api/v1"}}}`),
				Agents:    map[string]string{"rev.md": ocAgent, "plain.md": "Just a body."},
			},
			Passthrough: []string{"--title", "x"},
		},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			p, err := OpenCode(opencode(t), in)
			require.NoError(t, err)

			got, err := json.MarshalIndent(readable(t, p), "", "  ")
			require.NoError(t, err)

			golden(t, name, append(got, '\n'))
		})
	}
}

// readable pulls the generated config out of the env, so the golden shows
// it as JSON rather than as one escaped string.
func readable(t *testing.T, p Plan) map[string]any {
	t.Helper()

	var env []string

	var cfg any

	for _, kv := range p.Env {
		if v, ok := strings.CutPrefix(kv, "OPENCODE_CONFIG_CONTENT="); ok {
			require.NoError(t, json.Unmarshal([]byte(v), &cfg))

			continue
		}

		env = append(env, kv)
	}

	require.NotNil(t, cfg, "OPENCODE_CONFIG_CONTENT is set")

	return map[string]any{"argv": p.Argv, "env": env, "config": cfg, "copies": p.Copies, "files": p.Files}
}

func TestOpenCodeArgv(t *testing.T) {
	p, err := OpenCode(opencode(t), Input{ConfigDir: "/c", Model: "a/b", Contents: Contents{Prompt: "go"}, Passthrough: []string{"-x"}})
	require.NoError(t, err)

	assert.Equal(t, []string{"-x", "--", "go"}, p.Argv[len(p.Argv)-3:], "passthrough, then the prompt after --")
	assert.NotContains(t, p.Argv, "--variant", "no # in the model: no variant")
	assert.Contains(t, p.Env, "OPENCODE_CONFIG_DIR=/c")
	assert.Contains(t, p.Env, "OPENCODE_DISABLE_PROJECT_CONFIG=1")
	assert.Empty(t, p.Stdin)
}

func TestOpenCodeBadInputs(t *testing.T) {
	for name, c := range map[string]Contents{
		"mcp not json":             {MCP: []byte("nope")},
		"mcp unknown type":         {MCP: []byte(`{"mcpServers": {"x": {"type": "ws", "url": "u"}}}`)},
		"mcp stdio no cmd":         {MCP: []byte(`{"mcpServers": {"x": {"args": ["a"]}}}`)},
		"mcp http no url":          {MCP: []byte(`{"mcpServers": {"x": {"type": "http"}}}`)},
		"providers not json":       {Providers: []byte("[1]")},
		"agent no frontmatter end": {Agents: map[string]string{"a.md": "---\nname: a\n"}},
	} {
		_, err := OpenCode(opencode(t), Input{ConfigDir: "/c", Contents: c})
		require.ErrorIs(t, err, ErrBundle, name)
	}
}

func TestSplitVariant(t *testing.T) {
	a := Adapter{ModelFlag: "-m", VariantFlag: "--variant"}
	assert.Equal(t, []string{"-m", "p/m", "--variant", "high"}, a.modelArgs("p/m#high"))
	assert.Equal(t, []string{"-m", "p/m"}, a.modelArgs("p/m"))
	assert.Empty(t, a.modelArgs(""))

	a.VariantFlag = ""
	assert.Equal(t, []string{"-m", "p/m#high"}, a.modelArgs("p/m#high"), "verbatim without a variant flag")
}

func TestBuild(t *testing.T) {
	for _, name := range []string{"claude", "opencode"} {
		tbl, err := Load()
		require.NoError(t, err)

		a, err := tbl.Get(name)
		require.NoError(t, err)

		p, err := Build(a, Input{ConfigDir: "/c"})
		require.NoError(t, err)
		assert.Equal(t, name, p.Argv[0])
	}

	_, err := Build(Adapter{Name: "aider"}, Input{})
	require.ErrorIs(t, err, ErrUnknown)
}

func TestInteractive(t *testing.T) {
	tbl, err := Load()
	require.NoError(t, err)

	c, err := tbl.Get("claude")
	require.NoError(t, err)

	p, err := Build(c, Input{ConfigDir: "/c", Model: "opus", Interactive: true, Passthrough: []string{"-x"}})
	require.NoError(t, err)
	assert.Equal(t, []string{
		"claude", "--dangerously-skip-permissions", "--model", "opus",
		"--setting-sources", "user", "--strict-mcp-config", "-x",
	}, p.Argv, "no -p, no output format, slash commands on")
	assert.Contains(t, p.Env, "CLAUDE_CONFIG_DIR=/c")
	assert.Len(t, p.Files, 2, "settings.json and .claude.json")
	assert.Empty(t, p.Stdin)

	o, err := tbl.Get("opencode")
	require.NoError(t, err)

	p, err = Build(o, Input{ConfigDir: "/c", Model: "a/b", Interactive: true})
	require.NoError(t, err)
	assert.Equal(t, []string{"opencode", "-m", "a/b"}, p.Argv, "the TUI: no run, no prompt")
	assert.Contains(t, strings.Join(p.Env, "\n"), "OPENCODE_CONFIG_CONTENT=")
}

func TestOpenCodeDirsNeedNoFlag(t *testing.T) {
	p, err := OpenCode(opencode(t), Input{ConfigDir: "/c", Dirs: []string{"/srv"}, Contents: Contents{Prompt: "x"}})

	require.NoError(t, err)
	assert.NotContains(t, p.Argv, "/srv", "external_directory: allow grants it")
}
