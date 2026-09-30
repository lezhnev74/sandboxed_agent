package bundle

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/bundle/plan"
	"github.com/lezhnev74/sandboxed_agent/internal/harness"
)

func write(t *testing.T, path, data string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
}

func read(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	require.NoError(t, err)

	return string(b)
}

func TestResolveUsesTheFilesystem(t *testing.T) {
	ctx := t.TempDir()
	write(t, filepath.Join(ctx, "mcp.json"), "{}")
	require.NoError(t, os.Mkdir(filepath.Join(ctx, "skills"), 0o750))

	got, err := Resolve(plan.Flags{Ctx: ctx, Prompt: "hi"})
	require.NoError(t, err)
	assert.Equal(t, plan.Sources{PromptText: "hi", MCP: filepath.Join(ctx, "mcp.json"), Skills: filepath.Join(ctx, "skills")}, got)
}

func TestCopy(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "p.md"), "prompt")
	write(t, filepath.Join(src, "sys.md"), "system")
	write(t, filepath.Join(src, "m.json"), "{}")
	write(t, filepath.Join(src, "skills", "go", "SKILL.md"), "skill")
	write(t, filepath.Join(src, "agents", "rev.md"), "agent")

	dst := filepath.Join(t.TempDir(), "run", "bundle")
	write(t, filepath.Join(dst, "stale.md"), "old")

	s := plan.Sources{
		Prompt: filepath.Join(src, "p.md"), System: filepath.Join(src, "sys.md"), MCP: filepath.Join(src, "m.json"),
		Skills: filepath.Join(src, "skills"), Agents: filepath.Join(src, "agents"),
	}

	for range 2 { // idempotent
		got, err := Copy(s, dst)
		require.NoError(t, err)
		assert.Equal(t, plan.Layout(dst, s), got)
	}

	assert.NoFileExists(t, filepath.Join(dst, "stale.md"))
	assert.Equal(t, "prompt", read(t, filepath.Join(dst, "prompt.md")))
	assert.Equal(t, "system", read(t, filepath.Join(dst, "system.md")))
	assert.Equal(t, "{}", read(t, filepath.Join(dst, "mcp.json")))
	assert.Equal(t, "skill", read(t, filepath.Join(dst, "skills", "go", "SKILL.md")))
	assert.Equal(t, "agent", read(t, filepath.Join(dst, "agents", "rev.md")))
}

func TestCopyInlinePrompt(t *testing.T) {
	dst := filepath.Join(t.TempDir(), "bundle")

	got, err := Copy(plan.Sources{PromptText: "do it"}, dst)
	require.NoError(t, err)
	assert.Equal(t, plan.Parts{Prompt: filepath.Join(dst, "prompt.md")}, got)
	assert.Equal(t, "do it", read(t, got.Prompt))
}

func TestCopyMissingSource(t *testing.T) {
	_, err := Copy(plan.Sources{System: "/nonexistent/x"}, t.TempDir())
	require.Error(t, err)
}

func TestApply(t *testing.T) {
	src := t.TempDir()
	write(t, filepath.Join(src, "skills", "a", "SKILL.md"), "s")

	cfg := filepath.Join(t.TempDir(), "claude")
	files := []harness.File{{Path: filepath.Join(cfg, "settings.json"), Data: "{}"}}
	copies := []harness.DirCopy{{From: filepath.Join(src, "skills"), To: filepath.Join(cfg, "skills")}}

	require.NoError(t, Apply(files, copies))
	assert.Equal(t, "{}", read(t, filepath.Join(cfg, "settings.json")))
	assert.Equal(t, "s", read(t, filepath.Join(cfg, "skills", "a", "SKILL.md")))

	info, err := os.Stat(filepath.Join(cfg, "settings.json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
}

func TestReadContents(t *testing.T) {
	dir := t.TempDir()
	write(t, filepath.Join(dir, "prompt.md"), "do it")
	write(t, filepath.Join(dir, "mcp.json"), `{"mcpServers":{}}`)
	write(t, filepath.Join(dir, "providers.json"), `{"x":{}}`)
	write(t, filepath.Join(dir, "agents", "rev.md"), "review")
	write(t, filepath.Join(dir, "agents", "notes.txt"), "skipped")
	write(t, filepath.Join(dir, "agents", "sub", "deep.md"), "skipped")

	got, err := ReadContents(plan.Parts{
		Prompt: filepath.Join(dir, "prompt.md"), MCP: filepath.Join(dir, "mcp.json"),
		Providers: filepath.Join(dir, "providers.json"), Agents: filepath.Join(dir, "agents"),
	})
	require.NoError(t, err)
	assert.Equal(t, harness.Contents{
		Prompt: "do it", MCP: []byte(`{"mcpServers":{}}`), Providers: []byte(`{"x":{}}`),
		Agents: map[string]string{"rev.md": "review"},
	}, got)

	got, err = ReadContents(plan.Parts{})
	require.NoError(t, err)
	assert.Equal(t, harness.Contents{}, got)

	_, err = ReadContents(plan.Parts{MCP: filepath.Join(dir, "gone.json")})
	require.Error(t, err)

	_, err = ReadContents(plan.Parts{Agents: filepath.Join(dir, "gone")})
	require.Error(t, err)
}
