package main

import (
	"bufio"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
)

// Func is one function as reported by gocyclo, joined with its coverage.
type Func struct {
	File       string  `json:"file"`
	Line       int     `json:"line"`
	Package    string  `json:"package"`
	Name       string  `json:"name"`
	Complexity int     `json:"complexity"`
	Coverage   float64 `json:"coverage"` // fraction in [0,1]
	Covered    bool    `json:"covered"`  // false when absent from the coverage profile
	CRAP       float64 `json:"crap"`
}

// Location renders the "file:line" key used to join the two reports.
func (f Func) Location() string {
	return fmt.Sprintf("%s:%d", f.File, f.Line)
}

// crapScore is the Change Risk Anti-Patterns index:
//
//	CRAP(m) = comp(m)^2 * (1 - cov(m))^3 + comp(m)
//
// with cov expressed as a fraction in [0,1]. A fully covered function scores
// exactly its complexity; an uncovered one scores comp^2+comp, so risk grows
// quadratically as tests disappear.
func crapScore(complexity int, coverage float64) float64 {
	c := float64(complexity)
	uncovered := 1 - clamp(coverage)

	return c*c*uncovered*uncovered*uncovered + c
}

func clamp(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}

// parseGocyclo reads `gocyclo` output lines of the form
//
//	4 engine (*Engine).Run internal/engine/engine.go:26:1
func parseGocyclo(r io.Reader) ([]Func, error) {
	var funcs []Func

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "Average:") {
			continue
		}

		fn, err := parseGocycloLine(line)
		if err != nil {
			return nil, err
		}

		funcs = append(funcs, fn)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read gocyclo report: %w", err)
	}

	return funcs, nil
}

func parseGocycloLine(line string) (Func, error) {
	fields := strings.Fields(line)
	if len(fields) < 4 {
		return Func{}, fmt.Errorf("malformed gocyclo line %q: want 4 fields, got %d", line, len(fields))
	}

	complexity, err := strconv.Atoi(fields[0])
	if err != nil {
		return Func{}, fmt.Errorf("malformed gocyclo line %q: complexity: %w", line, err)
	}

	file, declLine, err := splitLocation(fields[3])
	if err != nil {
		return Func{}, fmt.Errorf("malformed gocyclo line %q: %w", line, err)
	}

	return Func{
		File:       file,
		Line:       declLine,
		Package:    fields[1],
		Name:       fields[2],
		Complexity: complexity,
	}, nil
}

// parseCoverFunc reads `go tool cover -func` output lines of the form
//
//	github.com/me/mod/internal/engine/engine.go:26:\tRun\t85.7%
//
// and returns coverage fractions keyed by "file:line". Paths are made relative
// to modulePath so they join against gocyclo's relative paths. The trailing
// "total:" summary line is ignored.
func parseCoverFunc(r io.Reader, modulePath string) (map[string]float64, error) {
	coverage := make(map[string]float64)

	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "total:") {
			continue
		}

		key, fraction, err := parseCoverFuncLine(line, modulePath)
		if err != nil {
			return nil, err
		}

		coverage[key] = fraction
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read cover report: %w", err)
	}

	return coverage, nil
}

// parseCoverFuncLine turns one cover line into a "file:line" key and a coverage
// fraction in [0,1].
func parseCoverFuncLine(line, modulePath string) (string, float64, error) {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return "", 0, fmt.Errorf("malformed cover line %q: want 3 fields, got %d", line, len(fields))
	}

	file, declLine, err := splitLocation(fields[0])
	if err != nil {
		return "", 0, fmt.Errorf("malformed cover line %q: %w", line, err)
	}

	pct, err := strconv.ParseFloat(strings.TrimSuffix(fields[len(fields)-1], "%"), 64)
	if err != nil {
		return "", 0, fmt.Errorf("malformed cover line %q: percentage: %w", line, err)
	}

	return fmt.Sprintf("%s:%d", relativeTo(file, modulePath), declLine), pct / 100, nil
}

// splitLocation splits "path/to/file.go:26" or "path/to/file.go:26:1" into its
// file and line parts. Windows-style drive letters are not supported.
func splitLocation(loc string) (string, int, error) {
	parts := strings.Split(strings.TrimSuffix(loc, ":"), ":")
	if len(parts) < 2 {
		return "", 0, fmt.Errorf("location %q: want file:line", loc)
	}

	line, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", 0, fmt.Errorf("location %q: line: %w", loc, err)
	}

	return parts[0], line, nil
}

// relativeTo strips the module path prefix that `go tool cover` prepends to
// file names, leaving a path comparable with gocyclo's output.
func relativeTo(file, modulePath string) string {
	if modulePath == "" {
		return file
	}

	return strings.TrimPrefix(file, modulePath+"/")
}

// join attaches coverage to each function. Functions missing from the coverage
// profile were never compiled into a tested package and count as uncovered.
func join(funcs []Func, coverage map[string]float64) []Func {
	joined := make([]Func, 0, len(funcs))

	for _, fn := range funcs {
		cov, ok := coverage[fn.Location()]
		fn.Coverage = cov
		fn.Covered = ok
		fn.CRAP = crapScore(fn.Complexity, cov)
		joined = append(joined, fn)
	}

	sort.Slice(joined, func(i, j int) bool {
		if joined[i].CRAP != joined[j].CRAP {
			return joined[i].CRAP > joined[j].CRAP
		}

		return joined[i].Location() < joined[j].Location()
	})

	return joined
}

// excludeTests drops functions declared in _test.go files: test code is never
// present in a coverage profile, so it would always score as uncovered.
func excludeTests(funcs []Func) []Func {
	kept := make([]Func, 0, len(funcs))

	for _, fn := range funcs {
		if !strings.HasSuffix(fn.File, "_test.go") {
			kept = append(kept, fn)
		}
	}

	return kept
}

func over(funcs []Func, threshold float64) []Func {
	var bad []Func

	for _, fn := range funcs {
		if fn.CRAP > threshold {
			bad = append(bad, fn)
		}
	}

	return bad
}

// moduleFromGoMod extracts the module path from a go.mod file. A missing file
// is not an error: the join then falls back to comparing paths verbatim.
func moduleFromGoMod(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	for _, line := range strings.Split(string(data), "\n") {
		if rest, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
			return strings.TrimSpace(rest)
		}
	}

	return ""
}
