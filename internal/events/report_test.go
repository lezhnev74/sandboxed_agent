package events

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExitCode(t *testing.T) {
	tests := []struct {
		kind Kind
		want int
	}{
		{OK, 0},
		{0, 1},
		{Fail, 2},
		{Timeout, 3},
		{RateLimit, 4},
		{Overloaded, 5},
		{Kind(99), 1},
	}
	for _, tc := range tests {
		t.Run(tc.kind.String(), func(t *testing.T) {
			assert.Equal(t, tc.want, tc.kind.ExitCode())
		})
	}
}

func TestNewReport(t *testing.T) {
	o := Outcome{
		Kind: RateLimit, Detail: "api 429", ResetAt: time.Date(2026, 9, 29, 17, 0, 0, 0, time.FixedZone("x", 3600)),
		CostUSD: 0.25, InTokens: 1200, OutTokens: 30, FinalText: "limit", ExitCode: 1,
	}
	m := Meta{Harness: "claude", Version: "2.1.3", Model: "opus", RunID: "20260929-070503-ab0c", Duration: 1500 * time.Millisecond}

	b, err := json.Marshal(NewReport(o, m))
	require.NoError(t, err)
	assert.JSONEq(t, `{"outcome":"rate-limit","detail":"api 429","final_text":"limit","exit_code":1,
		"cost_usd":0.25,"in_tokens":1200,"out_tokens":30,"reset_at":"2026-09-29T16:00:00Z",
		"harness":"claude","version":"2.1.3","model":"opus","duration_s":1.5,"run_id":"20260929-070503-ab0c"}`, string(b))
}

func TestNewReportOmitsAnUnknownReset(t *testing.T) {
	b, err := json.Marshal(NewReport(Outcome{Kind: Fail}, Meta{Harness: "opencode"}))
	require.NoError(t, err)
	assert.JSONEq(t, `{"outcome":"fail","detail":"","final_text":"","exit_code":0,
		"cost_usd":0,"in_tokens":0,"out_tokens":0,
		"harness":"opencode","version":"","model":"","duration_s":0,"run_id":""}`, string(b))
}
