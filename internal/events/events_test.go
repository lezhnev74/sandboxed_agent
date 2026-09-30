package events

import (
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClaudeResult(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want Result
	}{
		{
			name: "success",
			in: `{"type":"system","subtype":"init"}
{"type":"assistant","message":{"content":[{"type":"text","text":"hi"}]}}
{"type":"result","subtype":"success","is_error":false,"result":"done: 3 skills"}
`,
			want: Result{Found: true, Subtype: "success", Text: "done: 3 skills"},
		},
		{
			name: "error, no trailing newline, junk lines skipped",
			in:   "not json\n\n" + `{"type":"result","subtype":"error_max_turns","is_error":true,"result":"x"}`,
			want: Result{Found: true, IsError: true, Subtype: "error_max_turns", Text: "x"},
		},
		{
			name: "no result event",
			in:   `{"type":"system"}` + "\n",
			want: Result{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ClaudeResult(strings.NewReader(tc.in))
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestClaudeResultLongLine(t *testing.T) {
	long := strings.Repeat("a", 1<<20)

	got, err := ClaudeResult(strings.NewReader(`{"type":"result","result":"` + long + `"}`))
	require.NoError(t, err)
	assert.Len(t, got.Text, 1<<20)
}

type failing struct{}

var errRead = errors.New("read failed")

func (failing) Read([]byte) (int, error) { return 0, errRead }

func TestClaudeResultReadError(t *testing.T) {
	_, err := ClaudeResult(failing{})
	require.ErrorIs(t, err, errRead)
}
