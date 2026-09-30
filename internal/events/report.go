package events

import "time"

// Exit codes of `sandboxed_agent run`, one per outcome kind. ExitInterrupted
// is Ctrl-C or SIGTERM; ExitToolFailed is the tool itself (setup, box, bad
// flags) or no outcome at all.
const (
	ExitOK          = 0
	ExitToolFailed  = 1
	ExitFail        = 2
	ExitTimeout     = 3
	ExitRateLimit   = 4
	ExitOverloaded  = 5
	ExitInterrupted = 130
)

var exitCodes = map[Kind]int{
	OK: ExitOK, Fail: ExitFail, Timeout: ExitTimeout, RateLimit: ExitRateLimit, Overloaded: ExitOverloaded,
}

// ExitCode is the exit code of `sandboxed_agent run` for k.
func (k Kind) ExitCode() int {
	if c, ok := exitCodes[k]; ok {
		return c
	}

	return ExitToolFailed
}

// Meta is what a run adds to its outcome in the report.
type Meta struct {
	Harness  string
	Version  string
	Model    string
	RunID    string
	Duration time.Duration
}

// Report is the `run --json` result. ExitCode is the harness's own.
type Report struct {
	Outcome   string  `json:"outcome"`
	Detail    string  `json:"detail"`
	FinalText string  `json:"final_text"`
	ExitCode  int     `json:"exit_code"`
	CostUSD   float64 `json:"cost_usd"`
	InTokens  int     `json:"in_tokens"`
	OutTokens int     `json:"out_tokens"`
	ResetAt   string  `json:"reset_at,omitempty"` // RFC 3339, UTC; omitted when unknown
	Harness   string  `json:"harness"`
	Version   string  `json:"version"`
	Model     string  `json:"model"`
	DurationS float64 `json:"duration_s"`
	RunID     string  `json:"run_id"`
}

// NewReport is the report of outcome o with m.
func NewReport(o Outcome, m Meta) Report {
	r := Report{
		Outcome: o.Kind.String(), Detail: o.Detail, FinalText: o.FinalText, ExitCode: o.ExitCode,
		CostUSD: o.CostUSD, InTokens: o.InTokens, OutTokens: o.OutTokens,
		Harness: m.Harness, Version: m.Version, Model: m.Model, DurationS: m.Duration.Seconds(), RunID: m.RunID,
	}

	if !o.ResetAt.IsZero() {
		r.ResetAt = o.ResetAt.UTC().Format(time.RFC3339)
	}

	return r
}
