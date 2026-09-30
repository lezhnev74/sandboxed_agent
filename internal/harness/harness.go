// Package harness holds the adapter table (embedded adapters.json) and turns
// a run's inputs into a harness argv, env and generated config files.
// It is pure: callers write the files and run the argv.
package harness

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

//go:embed adapters.json
var adaptersJSON []byte

// ErrUnknown is returned for a harness name that has no adapter row.
var ErrUnknown = errors.New("unknown harness")

// ErrBundle is returned for a bundle part a builder cannot convert.
var ErrBundle = errors.New("bad bundle part")

// ErrAdapter is returned for an adapter row that misses a required field.
var ErrAdapter = errors.New("bad adapter row")

// How a harness takes its prompt.
const (
	PromptStdin = "stdin"
	PromptArg   = "arg"
)

// Adapter is one row of the adapter table.
type Adapter struct {
	Name        string            `json:"name"`
	Command     []string          `json:"command"` // the one-shot command
	Prompt      string            `json:"prompt"`  // PromptStdin or PromptArg
	ModelFlag   string            `json:"model_flag"`
	VariantFlag string            `json:"variant_flag,omitempty"` // set: a model's "#variant" suffix goes here
	DirFlag     string            `json:"dir_flag,omitempty"`     // set: each extra dir is passed with it
	AutoApprove []string          `json:"auto_approve"`
	Output      []string          `json:"output"`
	BareArgs    []string          `json:"bare_args,omitempty"`
	ConfigEnv   string            `json:"config_env"` // env var naming the per-run config dir
	Env         map[string]string `json:"env,omitempty"`
	Interactive []string          `json:"interactive"` // the TUI command, with its auto-approve flags
}

// Validate reports the first required field a row misses.
func (a Adapter) Validate() error {
	missing := map[string]bool{
		"name":         a.Name == "",
		"command":      len(a.Command) == 0,
		"prompt":       a.Prompt != PromptStdin && a.Prompt != PromptArg,
		"model_flag":   a.ModelFlag == "",
		"auto_approve": len(a.AutoApprove) == 0,
		"output":       len(a.Output) == 0,
		"config_env":   a.ConfigEnv == "",
		"interactive":  len(a.Interactive) == 0,
	}

	for _, field := range slices.Sorted(maps.Keys(missing)) {
		if missing[field] {
			return fmt.Errorf("%w %q: %s", ErrAdapter, a.Name, field)
		}
	}

	return nil
}

// Table is the adapter table by harness name.
type Table map[string]Adapter

// Load parses the embedded adapter table.
func Load() (Table, error) { return parse(adaptersJSON) }

func parse(data []byte) (Table, error) {
	var t Table
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, fmt.Errorf("adapters.json: %w", err)
	}

	for key, a := range t {
		if err := a.Validate(); err != nil {
			return nil, err
		}

		if key != a.Name {
			return nil, fmt.Errorf("%w %q: key %q differs from name", ErrAdapter, a.Name, key)
		}
	}

	return t, nil
}

// Get returns the named row.
func (t Table) Get(name string) (Adapter, error) {
	a, ok := t[name]
	if !ok {
		return Adapter{}, fmt.Errorf("%w %q (known: %s)", ErrUnknown, name, strings.Join(slices.Sorted(maps.Keys(t)), ", "))
	}

	return a, nil
}

// File is a generated file to write, mode 0600.
type File struct {
	Path string `json:"path"`
	Data string `json:"data"`
}

// DirCopy is a directory to copy recursively.
type DirCopy struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Plan is how to start one harness process.
type Plan struct {
	Argv   []string  `json:"argv"`
	Env    []string  `json:"env"`             // K=V, sorted
	Stdin  string    `json:"stdin,omitempty"` // file to feed as stdin
	Files  []File    `json:"files,omitempty"`
	Copies []DirCopy `json:"copies,omitempty"`
}

// base is the argv shared by every harness: command, model, auto-approve,
// output format and the bare-run switches. The TUI has its own command and
// no output format.
func (a Adapter) base(in Input) []string {
	if in.Interactive {
		argv := slices.Clone(a.Interactive)
		argv = append(argv, a.modelArgs(in.Model)...)
		argv = append(argv, a.BareArgs...)

		return append(argv, a.dirArgs(in.Dirs)...)
	}

	argv := slices.Clone(a.Command)
	argv = append(argv, a.modelArgs(in.Model)...)
	argv = append(argv, a.AutoApprove...)
	argv = append(argv, a.Output...)
	argv = append(argv, a.BareArgs...)

	return append(argv, a.dirArgs(in.Dirs)...)
}

// dirArgs grants the extra dirs to a harness with a dir flag; one without
// (OpenCode) reaches them through its external_directory permission.
func (a Adapter) dirArgs(dirs []string) []string {
	if a.DirFlag == "" {
		return nil
	}

	out := make([]string, 0, 2*len(dirs))
	for _, d := range dirs {
		out = append(out, a.DirFlag, d)
	}

	return out
}

// modelArgs passes the model on verbatim, except that a row with a
// variant flag gets "provider/model#variant" split across both flags.
func (a Adapter) modelArgs(model string) []string {
	if model == "" {
		return nil
	}

	if m, v, ok := strings.Cut(model, "#"); ok && a.VariantFlag != "" {
		return []string{a.ModelFlag, m, a.VariantFlag, v}
	}

	return []string{a.ModelFlag, model}
}

// Build makes the plan for a's harness.
func Build(a Adapter, in Input) (Plan, error) {
	switch a.Name {
	case "claude":
		return Claude(a, in), nil
	case "opencode":
		return OpenCode(a, in)
	default:
		return Plan{}, fmt.Errorf("%w %q: no run builder", ErrUnknown, a.Name)
	}
}

// env is the row's env plus the config dir, as sorted K=V.
func (a Adapter) env(configDir string) []string {
	out := make([]string, 0, len(a.Env)+1)
	out = append(out, a.ConfigEnv+"="+configDir)

	for k, v := range a.Env {
		out = append(out, k+"="+v)
	}

	slices.Sort(out)

	return out
}
