package events

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Fixture sources: opencode_ok_tool and opencode_error are real (opencode
// 1.18.33). Every claude_* fixture and opencode_ratelimit/_overloaded are
// synthetic, hand-written from the observed Claude Code 2.1.x stream-json
// schema (result: is_error, subtype, api_error_status, total_cost_usd,
// usage; rate_limit_event: rate_limit_info.status/resetsAt).
const (
	apiError429 = `API Error: 429 {"type":"error","error":{"type":"rate_limit_error",` +
		`"message":"Number of request tokens has exceeded your per-minute rate limit"}}`
	apiError529 = `API Error: 529 {"type":"error","error":{"type":"overloaded_error","message":"Overloaded"}}`
	apiError500 = `API Error: 500 {"type":"error","error":{"type":"api_error","message":"Internal server error"}}`
)

func TestOutcomeFixtures(t *testing.T) {
	reset := time.Unix(1790701200, 0).UTC()

	tests := []struct {
		file, harness string
		exit          int
		timedOut      bool
		want          Outcome
	}{
		{
			file: "claude_ok.jsonl", harness: "claude",
			want: Outcome{
				Kind: OK, CostUSD: 0.451483, InTokens: 23 + 14448 + 284384, OutTokens: 6559,
				FinalText: "Done.\n\nThe tests pass.",
			},
		},
		{
			file: "claude_is_error.jsonl", harness: "claude", exit: 1,
			want: Outcome{Kind: Fail, Detail: "error_during_execution", CostUSD: 0.0021, InTokens: 200, OutTokens: 40, ExitCode: 1},
		},
		{
			file: "claude_max_turns.jsonl", harness: "claude", exit: 1,
			want: Outcome{Kind: Fail, Detail: "error_max_turns", CostUSD: 0.0034, InTokens: 300, OutTokens: 25, ExitCode: 1},
		},
		{
			file: "claude_usage_limit.jsonl", harness: "claude", exit: 1,
			want: Outcome{
				Kind: RateLimit, Detail: "api 429", ResetAt: reset, ExitCode: 1,
				FinalText: "You've hit your limit · resets 3pm (UTC)",
			},
		},
		{
			file: "claude_429.jsonl", harness: "claude", exit: 1,
			want: Outcome{Kind: RateLimit, Detail: "api 429", ExitCode: 1, FinalText: apiError429},
		},
		{
			file: "claude_overloaded.jsonl", harness: "claude", exit: 1,
			want: Outcome{Kind: Overloaded, Detail: "api 529", ExitCode: 1, FinalText: apiError529},
		},
		{
			file: "claude_500.jsonl", harness: "claude", exit: 1,
			want: Outcome{Kind: Overloaded, Detail: "api 500", ExitCode: 1, FinalText: apiError500},
		},
		{
			file: "claude_ok.jsonl", harness: "claude", exit: 143, timedOut: true,
			want: Outcome{
				Kind: Timeout, Detail: "timeout", CostUSD: 0.451483, InTokens: 298855, OutTokens: 6559,
				FinalText: "Done.\n\nThe tests pass.", ExitCode: 143,
			},
		},
		{
			file: "opencode_ok_tool.jsonl", harness: "opencode",
			want: Outcome{Kind: OK, InTokens: 738 + 7424 + 105 + 8128, OutTokens: 21 + 16 + 3 + 37, FinalText: "9"},
		},
		{
			file: "opencode_error.jsonl", harness: "opencode", exit: 1,
			want: Outcome{
				Kind: Fail, Detail: "UnknownError", ExitCode: 1,
				FinalText: "Unexpected server error. Check server logs for details.",
			},
		},
		{
			file: "opencode_ratelimit.jsonl", harness: "opencode", exit: 1,
			want: Outcome{Kind: RateLimit, Detail: "api 429", ExitCode: 1, FinalText: "Rate limit exceeded"},
		},
		{
			file: "opencode_overloaded.jsonl", harness: "opencode", exit: 1,
			want: Outcome{Kind: Overloaded, Detail: "api 529", ExitCode: 1, FinalText: "Overloaded"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.file, func(t *testing.T) {
			f, err := os.Open(filepath.Join("testdata", tc.file))
			require.NoError(t, err)
			t.Cleanup(func() { _ = f.Close() })

			got, err := Read(tc.harness, f, tc.exit, tc.timedOut)
			require.NoError(t, err)

			assert.InDelta(t, tc.want.CostUSD, got.CostUSD, 1e-9)
			got.CostUSD = tc.want.CostUSD
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestClassify(t *testing.T) {
	legacyReset := time.Unix(1759248000, 0).UTC()

	tests := []struct {
		name string
		res  Result
		exit int
		want Outcome
	}{
		{name: "ok", res: Result{Found: true, Text: "x"}, want: Outcome{Kind: OK, FinalText: "x"}},
		{
			name: "ok result but non-zero exit fails",
			res:  Result{Found: true}, exit: 2,
			want: Outcome{Kind: Fail, Detail: "exit 2", ExitCode: 2},
		},
		{name: "no result", res: Result{}, want: Outcome{Kind: Fail, Detail: "no result event"}},
		{
			name: "legacy usage limit text carries the reset time",
			res:  Result{Found: true, IsError: true, Text: "Claude AI usage limit reached|1759248000"}, exit: 1,
			want: Outcome{
				Kind: RateLimit, Detail: "usage limit", ResetAt: legacyReset, ExitCode: 1,
				FinalText: "Claude AI usage limit reached|1759248000",
			},
		},
		{
			name: "rejected rate limit event without an api status",
			res:  Result{Found: true, IsError: true, RateLimited: true, ResetAt: legacyReset},
			want: Outcome{Kind: RateLimit, Detail: "rate limit", ResetAt: legacyReset},
		},
		{
			name: "overloaded text without a status",
			res:  Result{Found: true, IsError: true, Text: "Error: Overloaded"},
			want: Outcome{Kind: Overloaded, Detail: "overloaded", FinalText: "Error: Overloaded"},
		},
		{
			name: "a rejected event does not spoil a success",
			res:  Result{Found: true, RateLimited: true, ResetAt: legacyReset},
			want: Outcome{Kind: OK},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, Classify(tc.res, tc.exit, false))
		})
	}
}

func TestKindString(t *testing.T) {
	want := map[Kind]string{
		OK: "ok", Fail: "fail", Timeout: "timeout", RateLimit: "rate-limit", Overloaded: "overloaded", Kind(0): "none",
	}
	for k, s := range want {
		assert.Equal(t, s, k.String())
	}
}
