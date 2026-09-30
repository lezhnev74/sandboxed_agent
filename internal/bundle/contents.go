package bundle

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/lezhnev74/sandboxed_agent/internal/bundle/plan"
	"github.com/lezhnev74/sandboxed_agent/internal/harness"
)

// ReadContents reads the bundle copy's parts that a harness embeds rather
// than points at: the prompt, mcp.json, providers.json and agents/*.md.
func ReadContents(p plan.Parts) (harness.Contents, error) {
	var (
		c    harness.Contents
		errs [4]error
	)

	var prompt []byte
	prompt, errs[0] = readOptional(p.Prompt)
	c.Prompt = string(prompt)
	c.MCP, errs[1] = readOptional(p.MCP)
	c.Providers, errs[2] = readOptional(p.Providers)
	c.Agents, errs[3] = readAgents(p.Agents)

	for _, err := range errs {
		if err != nil {
			return harness.Contents{}, err
		}
	}

	return c, nil
}

// readOptional reads path; an empty path is an absent part.
func readOptional(path string) ([]byte, error) {
	if path == "" {
		return nil, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("bundle: %w", err)
	}

	return data, nil
}

// readAgents reads dir's top-level *.md files by name.
func readAgents(dir string) (map[string]string, error) {
	if dir == "" {
		return nil, nil
	}

	files, err := filepath.Glob(filepath.Join(dir, "*.md"))
	if err == nil {
		_, err = os.Stat(dir)
	}

	if err != nil {
		return nil, fmt.Errorf("bundle agents: %w", err)
	}

	out := make(map[string]string, len(files))

	for _, f := range files {
		data, err := readOptional(f)
		if err != nil {
			return nil, err
		}

		out[filepath.Base(f)] = string(data)
	}

	return out, nil
}
