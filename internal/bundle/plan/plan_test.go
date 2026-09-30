package plan

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func existsIn(paths ...string) func(string) bool {
	set := map[string]bool{}
	for _, p := range paths {
		set[p] = true
	}

	return func(p string) bool { return set[p] }
}

func TestResolve(t *testing.T) {
	full := existsIn("/ctx/prompt.md", "/ctx/system.md", "/ctx/mcp.json", "/ctx/skills", "/ctx/agents", "/ctx/providers.json",
		"/f/p.md", "/f/sys.md")

	tests := []struct {
		name   string
		flags  Flags
		exists func(string) bool
		want   Sources
	}{
		{"nothing", Flags{}, existsIn(), Sources{}},
		{
			"ctx parts, never its prompt.md or system.md", Flags{Ctx: "/ctx"}, full,
			Sources{MCP: "/ctx/mcp.json", Skills: "/ctx/skills", Agents: "/ctx/agents", Providers: "/ctx/providers.json"},
		},
		{"ctx, only existing parts", Flags{Ctx: "/ctx"}, existsIn("/ctx/mcp.json"), Sources{MCP: "/ctx/mcp.json"}},
		{"prompt file", Flags{Ctx: "/ctx", PromptFile: "/f/p.md"}, existsIn("/f/p.md"), Sources{Prompt: "/f/p.md"}},
		{"system file", Flags{Prompt: "hi", System: "/f/sys.md"}, full, Sources{PromptText: "hi", System: "/f/sys.md"}},
		{"inline prompt wins over prompt file", Flags{Prompt: "hi", PromptFile: "/f/p.md"}, full, Sources{PromptText: "hi"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Resolve(tt.flags, tt.exists)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveMissingFile(t *testing.T) {
	for _, f := range []Flags{{PromptFile: "/nope"}, {Prompt: "hi", System: "/nope"}} {
		_, err := Resolve(f, existsIn())
		require.ErrorIs(t, err, ErrMissing)
		assert.Contains(t, err.Error(), "/nope")
	}
}

func TestLayout(t *testing.T) {
	got := Layout("/run/bundle", Sources{PromptText: "x", MCP: "/a/m.json", Skills: "/a/s"})
	assert.Equal(t, Parts{Prompt: "/run/bundle/prompt.md", MCP: "/run/bundle/mcp.json", Skills: "/run/bundle/skills"}, got)

	got = Layout("/b", Sources{Prompt: "/p", System: "/s", Agents: "/a", Providers: "/pr"})
	assert.Equal(t, Parts{Prompt: "/b/prompt.md", System: "/b/system.md", Agents: "/b/agents", Providers: "/b/providers.json"}, got)

	assert.Equal(t, Parts{}, Layout("/b", Sources{}))
}

func TestHasPrompt(t *testing.T) {
	assert.False(t, Sources{System: "/s"}.HasPrompt())
	assert.True(t, Sources{Prompt: "/p"}.HasPrompt())
	assert.True(t, Sources{PromptText: "x"}.HasPrompt())
}
