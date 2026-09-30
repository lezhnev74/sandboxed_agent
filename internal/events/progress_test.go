package events

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func describeFile(t *testing.T, harness, file string) []string {
	t.Helper()

	f, err := os.Open(filepath.Join("testdata", file))
	require.NoError(t, err)
	t.Cleanup(func() { _ = f.Close() })

	var got []string

	sc := bufio.NewScanner(f)
	sc.Buffer(nil, 1<<20)

	for sc.Scan() {
		got = append(got, Describe(harness, sc.Bytes())...)
	}

	require.NoError(t, sc.Err())

	return got
}

func TestDescribeFixtures(t *testing.T) {
	tests := map[string]struct {
		harness string
		want    []string
	}{
		"claude_ok.jsonl": {"claude", []string{
			"I'll start by looking at the project.", "Bash: go test ./...", "Edit: /work/main.go", "Done.",
		}},
		"claude_max_turns.jsonl":   {"claude", []string{"Bash: ls"}},
		"claude_usage_limit.jsonl": {"claude", []string{"You've hit your limit · resets 3pm (UTC)"}},
		"claude_429.jsonl":         {"claude", nil},
		"opencode_ok_tool.jsonl":   {"opencode", []string{"bash: ls -a", "9"}},
		"opencode_error.jsonl":     {"opencode", nil},
	}

	for file, tc := range tests {
		t.Run(file, func(t *testing.T) {
			assert.Equal(t, tc.want, describeFile(t, tc.harness, file))
		})
	}
}

func TestDescribe(t *testing.T) {
	tests := []struct {
		name, harness, line string
		want                []string
	}{
		{
			name: "claude tool input: first known key wins, whitespace folded", harness: "claude",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Grep",` +
				`"input":{"output_mode":"x","pattern":"foo\n  bar"}}]}}`,
			want: []string{"Grep: foo bar"},
		},
		{
			name: "claude tool without a known key", harness: "claude",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"TodoWrite","input":{"todos":[]}}]}}`,
			want: []string{"TodoWrite"},
		},
		{
			name: "claude blank text is skipped", harness: "claude",
			line: `{"type":"assistant","message":{"content":[{"type":"text","text":"\n  \n"}]}}`,
		},
		{
			name: "subagent prompt", harness: "claude",
			line: `{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Task",` +
				`"input":{"description":"find callers","prompt":"long"}}]}}`,
			want: []string{"Task: find callers"},
		},
		{name: "junk", harness: "claude", line: "not json"},
		{name: "opencode junk", harness: "opencode", line: "{"},
		{
			name: "opencode tool with a file path", harness: "opencode",
			line: `{"type":"tool_use","part":{"tool":"read","state":{"input":{"filePath":"/a/b.go"}}}}`,
			want: []string{"read: /a/b.go"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Describe(tc.harness, []byte(tc.line)))
		})
	}
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", Truncate("abc", 3))
	assert.Equal(t, "ab…", Truncate("abcd", 3))
	assert.Equal(t, "жж…", Truncate("жжжж", 3))
	assert.Equal(t, "abcd", Truncate("abcd", 0))
	assert.Equal(t, "…", Truncate("abcd", 1))
}

func TestLineWriter(t *testing.T) {
	var got []string

	w := &LineWriter{Fn: func(l []byte) { got = append(got, string(l)) }}

	for _, chunk := range []string{"a", "b\nc\n", "", "d\ne"} {
		n, err := w.Write([]byte(chunk))
		require.NoError(t, err)
		assert.Equal(t, len(chunk), n)
	}

	assert.Equal(t, []string{"ab", "c", "d"}, got)

	w.Flush()
	w.Flush()
	assert.Equal(t, []string{"ab", "c", "d", "e"}, got)
	assert.Equal(t, "ab c d e", strings.Join(got, " "))
}
