package harness

import (
	"encoding/json"
	"errors"
	"fmt"
)

var (
	errNoFrontMatterEnd = errors.New("front matter has no closing ---")
	errMCPType          = errors.New("unknown server type")
	errMCPCommand       = errors.New("stdio server needs a command")
	errMCPURL           = errors.New("http/sse server needs a url")
)

// claudeServer is one entry of Claude's mcpServers.
type claudeServer struct {
	Type    string            `json:"type"`
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
}

// openCodeMCP converts a Claude mcp.json (mcpServers) into OpenCode's mcp
// block: stdio servers become "local", http and sse servers "remote".
func openCodeMCP(data []byte) (map[string]map[string]any, error) {
	var doc struct {
		Servers map[string]claudeServer `json:"mcpServers"`
	}

	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("%w: mcp.json: %w", ErrBundle, err)
	}

	out := make(map[string]map[string]any, len(doc.Servers))

	for name, s := range doc.Servers {
		entry, err := s.openCode()
		if err != nil {
			return nil, fmt.Errorf("%w: mcp.json server %q: %w", ErrBundle, name, err)
		}

		out[name] = entry
	}

	return out, nil
}

func (s claudeServer) openCode() (map[string]any, error) {
	switch s.Type {
	case "", "stdio":
		if s.Command == "" {
			return nil, errMCPCommand
		}

		return withMap(map[string]any{"type": "local", "command": append([]string{s.Command}, s.Args...)}, "environment", s.Env), nil
	case "http", "sse":
		if s.URL == "" {
			return nil, errMCPURL
		}

		return withMap(map[string]any{"type": "remote", "url": s.URL}, "headers", s.Headers), nil
	default:
		return nil, fmt.Errorf("%w %q", errMCPType, s.Type)
	}
}

// withMap sets m[key] = v when v is not empty.
func withMap(m map[string]any, key string, v map[string]string) map[string]any {
	if len(v) > 0 {
		m[key] = v
	}

	return m
}
