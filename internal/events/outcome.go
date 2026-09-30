package events

import (
	"io"
	"strconv"
	"strings"
	"time"
)

// Kind is how a run ended.
type Kind int

// The outcome kinds. The zero value means "no outcome".
const (
	OK Kind = iota + 1
	Fail
	Timeout
	RateLimit
	Overloaded
)

var kindNames = map[Kind]string{
	OK: "ok", Fail: "fail", Timeout: "timeout", RateLimit: "rate-limit", Overloaded: "overloaded",
}

func (k Kind) String() string {
	if n, ok := kindNames[k]; ok {
		return n
	}

	return "none"
}

// Outcome is a run's classified result.
type Outcome struct {
	Kind      Kind
	Detail    string    // why, for anything but OK: subtype, "api 429", "exit 2", ...
	ResetAt   time.Time // RateLimit only: when the limit resets; zero is unknown
	CostUSD   float64
	InTokens  int
	OutTokens int
	FinalText string
	ExitCode  int
}

// Read classifies the named harness's JSON log given its exit code and
// whether it was killed for taking too long.
func Read(harness string, r io.Reader, exitCode int, timedOut bool) (Outcome, error) {
	res, err := Final(harness, r)

	return Classify(res, exitCode, timedOut), err
}

// Classify turns the final result and the exit code into an outcome. The
// final event wins over the exit code: a clean result with a non-zero exit
// still fails, and a rate limit is a rate limit whatever the exit code.
func Classify(r Result, exitCode int, timedOut bool) Outcome {
	o := Outcome{
		CostUSD: r.CostUSD, InTokens: r.InTokens, OutTokens: r.OutTokens, FinalText: r.Text, ExitCode: exitCode,
	}
	o.Kind, o.Detail = kind(r, exitCode, timedOut)

	if o.Kind == RateLimit {
		o.ResetAt = resetAt(r)
	}

	return o
}

func kind(r Result, exitCode int, timedOut bool) (Kind, string) {
	if timedOut {
		return Timeout, "timeout"
	}

	if r.Found && !r.IsError && exitCode == 0 {
		return OK, ""
	}

	if k, detail, ok := limitKind(r); ok {
		return k, detail
	}

	return Fail, failDetail(r, exitCode)
}

// limitKind spots rate limits and overload: worth a retry later.
func limitKind(r Result) (Kind, string, bool) {
	text := strings.ToLower(r.Text)

	switch {
	case r.APIStatus == 429:
		return RateLimit, "api 429", true
	case r.RateLimited:
		return RateLimit, "rate limit", true
	case strings.Contains(text, "usage limit reached") || strings.Contains(text, "hit your limit"):
		return RateLimit, "usage limit", true
	case r.APIStatus >= 500:
		return Overloaded, "api " + strconv.Itoa(r.APIStatus), true
	case strings.Contains(text, "overloaded"):
		return Overloaded, "overloaded", true
	default:
		return 0, "", false
	}
}

func failDetail(r Result, exitCode int) string {
	switch {
	case r.IsError && r.Subtype != "":
		return r.Subtype
	case exitCode != 0:
		return "exit " + strconv.Itoa(exitCode)
	case !r.Found:
		return "no result event"
	default:
		return "error"
	}
}

// resetAt is the reset time of a rate-limit event, else the epoch after
// the `|` of the legacy "Claude AI usage limit reached|<epoch>" message.
func resetAt(r Result) time.Time {
	if !r.ResetAt.IsZero() {
		return r.ResetAt
	}

	_, after, ok := strings.Cut(r.Text, "|")
	if !ok {
		return time.Time{}
	}

	sec, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64)
	if err != nil {
		return time.Time{}
	}

	return unixOrZero(sec)
}
