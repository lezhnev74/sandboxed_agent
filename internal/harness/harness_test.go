package harness

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/lezhnev74/sandboxed_agent/internal/bundle/plan"
)

var update = flag.Bool("update", false, "rewrite golden files")

// TestAdapterTable is the drift guard: every row has every required field.
func TestAdapterTable(t *testing.T) {
	tbl, err := Load()
	require.NoError(t, err)

	for _, name := range []string{"claude", "opencode"} {
		a, err := tbl.Get(name)
		require.NoError(t, err, name)
		require.NoError(t, a.Validate(), name)
		assert.Equal(t, name, a.Name)
	}
}

func TestGetUnknown(t *testing.T) {
	tbl, err := Load()
	require.NoError(t, err)

	_, err = tbl.Get("aider")
	require.ErrorIs(t, err, ErrUnknown)
	assert.Contains(t, err.Error(), "claude, opencode")
}

func TestValidate(t *testing.T) {
	ok := Adapter{
		Name: "x", Command: []string{"x"}, Prompt: PromptStdin, ModelFlag: "-m",
		AutoApprove: []string{"-y"}, Output: []string{"--json"}, ConfigEnv: "X_DIR", Interactive: []string{"x"},
	}
	require.NoError(t, ok.Validate())

	for name, broken := range map[string]func(*Adapter){
		"name":         func(a *Adapter) { a.Name = "" },
		"command":      func(a *Adapter) { a.Command = nil },
		"prompt":       func(a *Adapter) { a.Prompt = "file" },
		"model_flag":   func(a *Adapter) { a.ModelFlag = "" },
		"auto_approve": func(a *Adapter) { a.AutoApprove = nil },
		"output":       func(a *Adapter) { a.Output = nil },
		"config_env":   func(a *Adapter) { a.ConfigEnv = "" },
		"interactive":  func(a *Adapter) { a.Interactive = nil },
	} {
		a := ok
		broken(&a)

		err := a.Validate()
		require.Error(t, err, name)
		assert.Contains(t, err.Error(), name)
	}
}

func TestParseRejectsBadTable(t *testing.T) {
	_, err := parse([]byte(`{"claude": {"name": "claude"}}`))
	require.Error(t, err)

	_, err = parse([]byte(`not json`))
	require.Error(t, err)

	_, err = parse([]byte(`{"a": {"name": "b", "command": ["b"], "prompt": "arg", "model_flag": "-m",
		"auto_approve": ["-y"], "output": ["-j"], "config_env": "B", "interactive": ["b"]}}`))
	require.Error(t, err, "row key must match its name")
}

func claude(t *testing.T) Adapter {
	t.Helper()

	tbl, err := Load()
	require.NoError(t, err)

	a, err := tbl.Get("claude")
	require.NoError(t, err)

	return a
}

func TestClaudeGolden(t *testing.T) {
	const cfg = "/cfg/repo-1234/20260929-120000-abcd/claude"

	const b = "/cfg/repo-1234/20260929-120000-abcd/bundle"

	tests := map[string]Input{
		"claude_minimal": {ConfigDir: cfg, Bundle: plan.Parts{Prompt: b + "/prompt.md"}},
		"claude_full": {
			ConfigDir: cfg, Model: "opus", Dirs: []string{"/srv/data", "/home/u/repo/.git"},
			Bundle: plan.Parts{
				Prompt: b + "/prompt.md", System: b + "/system.md", MCP: b + "/mcp.json",
				Skills: b + "/skills", Agents: b + "/agents",
			},
			Passthrough: []string{"--effort", "high"},
		},
		"claude_agents_no_skills": {
			ConfigDir: cfg, Model: "openrouter/qwen/qwen3#high",
			Bundle: plan.Parts{Prompt: b + "/prompt.md", Agents: b + "/agents"},
		},
	}

	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(Claude(claude(t), in), "", "  ")
			require.NoError(t, err)

			golden(t, name, append(got, '\n'))
		})
	}
}

func TestClaudeNeverBare(t *testing.T) {
	p := Claude(claude(t), Input{ConfigDir: "/c", Passthrough: []string{"-x"}})

	assert.NotContains(t, p.Argv, "--bare")
	assert.Equal(t, "-x", p.Argv[len(p.Argv)-1], "passthrough goes last")
	assert.Contains(t, p.Env, "CLAUDE_CONFIG_DIR=/c")
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()

	path := filepath.Join("testdata", name+".golden")
	if *update {
		require.NoError(t, os.MkdirAll("testdata", 0o750))
		require.NoError(t, os.WriteFile(path, got, 0o600))

		return
	}

	want, err := os.ReadFile(path)
	require.NoError(t, err, "run go test ./internal/harness -update")
	assert.Equal(t, string(want), string(got))
}
