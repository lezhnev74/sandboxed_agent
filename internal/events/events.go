// Package events reads a harness's JSON event stream: the final result
// (Claude and OpenCode), the outcome it classifies to, and one progress line
// per tool call or assistant message.
package events

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Usage is what a run cost. Input counts every input token the
// model processed, cache reads and writes included; output counts reasoning
// tokens too.
type Usage struct {
	CostUSD   float64
	InTokens  int
	OutTokens int
}

// Result is the harness's final event, plus the usage and rate-limit
// signals seen on the way.
type Result struct {
	Usage

	Found       bool      // a final event was seen
	IsError     bool      // is_error, or an OpenCode error event
	Subtype     string    // success, error_max_turns, stop, UnknownError, ...
	Text        string    // the final message
	APIStatus   int       // HTTP status of the failed API call; 0 is none
	RateLimited bool      // a rate-limit event said the limit is hit
	ResetAt     time.Time // when that limit resets; zero is unknown
}

// claudeEvent is the part of a stream-json line the result needs.
type claudeEvent struct {
	Type      string  `json:"type"`
	Subtype   string  `json:"subtype"`
	IsError   bool    `json:"is_error"`
	Result    string  `json:"result"`
	APIStatus int     `json:"api_error_status"`
	CostUSD   float64 `json:"total_cost_usd"`
	Usage     struct {
		Input         int `json:"input_tokens"`
		CacheCreation int `json:"cache_creation_input_tokens"`
		CacheRead     int `json:"cache_read_input_tokens"`
		Output        int `json:"output_tokens"`
	} `json:"usage"`
	RateLimit struct {
		Status   string `json:"status"`
		ResetsAt int64  `json:"resetsAt"`
	} `json:"rate_limit_info"`
}

// ClaudeResult returns the last `result` event of a stream-json log, with
// the last rejected rate-limit event. Lines that are not JSON are skipped.
func ClaudeResult(r io.Reader) (Result, error) {
	var (
		out     Result
		limited bool
		reset   time.Time
	)

	err := scan(r, func(line []byte) {
		var e claudeEvent
		if json.Unmarshal(line, &e) != nil {
			return
		}

		switch e.Type {
		case "result":
			out = e.result()
		case "rate_limit_event":
			if e.RateLimit.Status == "rejected" {
				limited, reset = true, unixOrZero(e.RateLimit.ResetsAt)
			}
		}
	})

	out.RateLimited, out.ResetAt = limited, reset

	return out, err
}

func (e claudeEvent) result() Result {
	u := e.Usage

	return Result{
		Found: true, IsError: e.IsError, Subtype: e.Subtype, Text: e.Result, APIStatus: e.APIStatus,
		Usage: Usage{
			CostUSD: e.CostUSD, InTokens: u.Input + u.CacheCreation + u.CacheRead, OutTokens: u.Output,
		},
	}
}

func unixOrZero(sec int64) time.Time {
	if sec <= 0 {
		return time.Time{}
	}

	return time.Unix(sec, 0).UTC()
}

// Final reads the final result of the named harness's JSON log.
func Final(harness string, r io.Reader) (Result, error) {
	if harness == "opencode" {
		return OpenCodeResult(r)
	}

	return ClaudeResult(r)
}

// scan calls fn with every line of r, the last one even without a newline.
func scan(r io.Reader, fn func(line []byte)) error {
	br := bufio.NewReader(r)

	for {
		line, err := br.ReadBytes('\n')
		fn(line)

		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			return fmt.Errorf("events: %w", err)
		}
	}
}
