package events

import (
	"encoding/json"
	"io"
	"strings"
)

// openCodeEvent is one line of `opencode run --format json`.
type openCodeEvent struct {
	Type string `json:"type"`
	Part struct {
		Text   string  `json:"text"`
		Reason string  `json:"reason"`
		Tool   string  `json:"tool"`
		Cost   float64 `json:"cost"`
		Tokens struct {
			Input     int `json:"input"`
			Output    int `json:"output"`
			Reasoning int `json:"reasoning"`
			Cache     struct {
				Read  int `json:"read"`
				Write int `json:"write"`
			} `json:"cache"`
		} `json:"tokens"`
		State struct {
			Input map[string]any `json:"input"`
		} `json:"state"`
	} `json:"part"`
	Error struct {
		Name string `json:"name"`
		Data struct {
			Message    string `json:"message"`
			StatusCode int    `json:"statusCode"`
		} `json:"data"`
	} `json:"error"`
}

// openCodeState folds the event stream into the final result: the text
// parts of the last step, how that step (or the run) ended, and the usage
// summed over every step.
type openCodeState struct {
	res   Result
	usage Usage
	texts []string
}

// OpenCodeResult returns the final result of an OpenCode JSON log. A step
// that finished for any reason but tool calls is the final answer; an
// `error` event is a failure. Lines that are not JSON are skipped.
func OpenCodeResult(r io.Reader) (Result, error) {
	var st openCodeState

	err := scan(r, st.add)
	st.res.Usage = st.usage

	return st.res, err
}

func (st *openCodeState) add(line []byte) {
	var e openCodeEvent
	if json.Unmarshal(line, &e) != nil {
		return
	}

	switch e.Type {
	case "step_start":
		st.texts = nil
	case typeText:
		st.texts = append(st.texts, e.Part.Text)
	case "step_finish":
		st.finish(e)
	case "error":
		st.res = Result{
			Found: true, IsError: true, Subtype: e.Error.Name, Text: e.Error.Data.Message,
			APIStatus: e.Error.Data.StatusCode,
		}
	}
}

func (st *openCodeState) finish(e openCodeEvent) {
	t := e.Part.Tokens
	st.usage.CostUSD += e.Part.Cost
	st.usage.InTokens += t.Input + t.Cache.Read + t.Cache.Write
	st.usage.OutTokens += t.Output + t.Reasoning

	st.res = Result{
		Found: e.Part.Reason != "tool-calls", Subtype: e.Part.Reason, Text: strings.Join(st.texts, "\n"),
	}
}
