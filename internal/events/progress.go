package events

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// typeText marks a text part or event in both harnesses' output.
const typeText = "text"

// inputKeys are the tool-input fields that best say what a call does, in
// order of preference (Claude and OpenCode spellings).
var inputKeys = []string{
	"command", "file_path", "filePath", "path", "pattern", "url", "query", "description", "skill", "prompt",
}

// Describe returns one progress line per tool call or assistant message in
// a line of the named harness's JSON log: "Bash: go test ./..." or the
// first line of the text. Other lines give nothing.
func Describe(harness string, line []byte) []string {
	if harness == "opencode" {
		return describeOpenCode(line)
	}

	return describeClaude(line)
}

func describeClaude(line []byte) []string {
	var e struct {
		Type    string `json:"type"`
		Message struct {
			Content []struct {
				Type  string         `json:"type"`
				Text  string         `json:"text"`
				Name  string         `json:"name"`
				Input map[string]any `json:"input"`
			} `json:"content"`
		} `json:"message"`
	}

	if json.Unmarshal(line, &e) != nil || e.Type != "assistant" {
		return nil
	}

	var out []string

	for _, c := range e.Message.Content {
		switch c.Type {
		case "tool_use":
			out = append(out, toolLine(c.Name, c.Input))
		case typeText:
			out = appendText(out, c.Text)
		}
	}

	return out
}

func describeOpenCode(line []byte) []string {
	var e openCodeEvent
	if json.Unmarshal(line, &e) != nil {
		return nil
	}

	switch e.Type {
	case "tool_use":
		return []string{toolLine(e.Part.Tool, e.Part.State.Input)}
	case typeText:
		return appendText(nil, e.Part.Text)
	default:
		return nil
	}
}

func toolLine(name string, input map[string]any) string {
	for _, k := range inputKeys {
		if s, ok := input[k].(string); ok && strings.TrimSpace(s) != "" {
			return name + ": " + strings.Join(strings.Fields(s), " ")
		}
	}

	return name
}

// appendText appends the first non-blank line of text, if any.
func appendText(out []string, text string) []string {
	for l := range strings.SplitSeq(text, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			return append(out, l)
		}
	}

	return out
}

// Truncate cuts s to width runes, the last one an ellipsis. A width of 0
// or less leaves s whole.
func Truncate(s string, width int) string {
	if width <= 0 || utf8.RuneCountInString(s) <= width {
		return s
	}

	r := []rune(s)

	return string(r[:width-1]) + "…"
}

// LineWriter calls Fn with every complete line written to it, without the
// newline. Flush hands over a trailing partial line.
type LineWriter struct {
	Fn  func(line []byte)
	buf []byte
}

func (w *LineWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)

	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			return len(p), nil
		}

		w.Fn(w.buf[:i])
		w.buf = w.buf[i+1:]
	}
}

// Flush hands over the partial line left, if any.
func (w *LineWriter) Flush() {
	if len(w.buf) > 0 {
		w.Fn(w.buf)
		w.buf = nil
	}
}
