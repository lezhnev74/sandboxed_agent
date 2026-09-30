package harness

import (
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"
)

const (
	allow        = "allow"
	openCodeConf = "OPENCODE_CONFIG_CONTENT"
)

// openCodeBuiltins are the built-in agents a bare run disables, so only the
// bundle's subagents (and the primary build agent) remain.
var openCodeBuiltins = []string{"explore", "general", "plan"}

// OpenCode builds a bare, fully permitted OpenCode run. The generated config
// goes in OPENCODE_CONFIG_CONTENT; the prompt is the last argument, after
// "--" so a leading dash is not read as a flag.
func OpenCode(a Adapter, in Input) (Plan, error) {
	cfg, err := openCodeConfig(in)
	if err != nil {
		return Plan{}, err
	}

	data, err := json.Marshal(cfg)
	if err != nil {
		return Plan{}, fmt.Errorf("opencode config: %w", err)
	}

	argv := append(a.base(in), in.Passthrough...)
	env := append(a.env(in.ConfigDir), openCodeConf+"="+string(data))
	slices.Sort(env)

	if !in.Interactive {
		argv = append(argv, "--", in.Contents.Prompt)
	}

	p := Plan{Argv: argv, Env: env}
	if in.Bundle.Skills != "" {
		p.Copies = []DirCopy{{From: in.Bundle.Skills, To: filepath.Join(in.ConfigDir, "skills")}}
	}

	return p, nil
}

func openCodeConfig(in Input) (map[string]any, error) {
	cfg := map[string]any{
		"$schema":    "https://opencode.ai/config.json",
		"autoupdate": false,
		"permission": map[string]string{
			"edit": allow, "bash": allow, "webfetch": allow, "external_directory": allow, "doom_loop": allow,
		},
	}

	if in.Bundle.System != "" {
		cfg["instructions"] = []string{in.Bundle.System}
	}

	agents, err := openCodeAgents(in.Contents.Agents)
	if err != nil {
		return nil, err
	}

	cfg["agent"] = agents

	return cfg, addOptional(cfg, in.Contents)
}

// addOptional adds the mcp and provider blocks when the bundle has them.
func addOptional(cfg map[string]any, c Contents) error {
	if len(c.MCP) > 0 {
		mcp, err := openCodeMCP(c.MCP)
		if err != nil {
			return err
		}

		cfg["mcp"] = mcp
	}

	if len(c.Providers) > 0 {
		var providers map[string]any
		if err := json.Unmarshal(c.Providers, &providers); err != nil {
			return fmt.Errorf("%w: providers.json: %w", ErrBundle, err)
		}

		cfg["provider"] = providers
	}

	return nil
}

// openCodeAgents disables the built-ins and adds each agents/*.md file as a
// subagent.
func openCodeAgents(files map[string]string) (map[string]any, error) {
	out := map[string]any{}
	for _, name := range openCodeBuiltins {
		out[name] = map[string]bool{"disable": true}
	}

	for _, file := range slices.Sorted(maps.Keys(files)) {
		name, agent, err := parseAgent(file, files[file])
		if err != nil {
			return nil, err
		}

		out[name] = agent
	}

	return out, nil
}

// parseAgent turns a Claude subagent file into an OpenCode agent entry. Only
// name and description are taken from the front matter: Claude's tools and
// model values mean nothing to OpenCode.
func parseAgent(file, data string) (string, map[string]string, error) {
	name := strings.TrimSuffix(file, filepath.Ext(file))

	front, body, err := splitFrontMatter(data)
	if err != nil {
		return "", nil, fmt.Errorf("%w: agents/%s: %w", ErrBundle, file, err)
	}

	if v := front["name"]; v != "" {
		name = v
	}

	desc := front["description"]
	if desc == "" {
		desc = name
	}

	return name, map[string]string{"mode": "subagent", "description": desc, "prompt": body}, nil
}

// splitFrontMatter splits "---\nkey: value\n---\nbody". Only flat
// "key: value" lines are read; anything else in the front matter is ignored.
func splitFrontMatter(data string) (map[string]string, string, error) {
	rest, ok := strings.CutPrefix(data, "---\n")
	if !ok {
		return nil, strings.TrimSpace(data), nil
	}

	head, body, ok := strings.Cut(rest, "\n---")
	if !ok {
		return nil, "", errNoFrontMatterEnd
	}

	front := map[string]string{}

	for line := range strings.SplitSeq(head, "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok {
			front[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}

	return front, strings.TrimSpace(body), nil
}
