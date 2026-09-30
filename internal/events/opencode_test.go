package events

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOpenCodeResultFixtures(t *testing.T) {
	tests := map[string]Result{
		// opencode 1.18.33, opencode/mimo-v2.6-flash-free: a bash call, then the answer.
		"opencode_ok_tool.jsonl": {
			Found: true, Subtype: "stop", Text: "9", Usage: Usage{InTokens: 16395, OutTokens: 77},
		},
		// opencode 1.18.33, an unknown model.
		"opencode_error.jsonl": {
			Found: true, IsError: true, Subtype: "UnknownError",
			Text: "Unexpected server error. Check server logs for details.",
		},
	}

	for file, want := range tests {
		t.Run(file, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", file))
			require.NoError(t, err)
			t.Cleanup(func() { _ = f.Close() })

			got, err := OpenCodeResult(f)
			require.NoError(t, err)
			assert.Equal(t, want, got)
		})
	}
}

func TestOpenCodeResult(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Result
	}{
		{
			name: "text parts of the last step are joined",
			in: `{"type":"text","part":{"type":"text","text":"old"}}
{"type":"step_start","part":{}}
{"type":"text","part":{"type":"text","text":"a"}}
junk
{"type":"text","part":{"type":"text","text":"b"}}
{"type":"step_finish","part":{"reason":"stop"}}`,
			want: Result{Found: true, Subtype: "stop", Text: "a\nb"},
		},
		{
			name: "stopped mid tool calls: no final answer",
			in:   `{"type":"step_finish","part":{"reason":"tool-calls"}}` + "\n",
			want: Result{Subtype: "tool-calls"},
		},
		{name: "empty", in: "", want: Result{}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := OpenCodeResult(strings.NewReader(tc.in))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestOpenCodeResultReadError(t *testing.T) {
	_, err := OpenCodeResult(failing{})
	require.Error(t, err)
}

func TestFinal(t *testing.T) {
	got, err := Final("opencode", strings.NewReader(`{"type":"step_finish","part":{"reason":"stop"}}`))
	require.NoError(t, err)
	assert.True(t, got.Found)

	got, err = Final("claude", strings.NewReader(`{"type":"result","result":"x"}`))
	require.NoError(t, err)
	assert.Equal(t, "x", got.Text)
}
