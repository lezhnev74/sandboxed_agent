package main

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrapScore(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		complexity int
		coverage   float64
		want       float64
	}{
		"fully covered scores its complexity": {complexity: 10, coverage: 1, want: 10},
		"uncovered scores comp^2+comp":        {complexity: 10, coverage: 0, want: 110},
		"half covered":                        {complexity: 10, coverage: 0.5, want: 22.5},
		"trivial and uncovered stays low":     {complexity: 1, coverage: 0, want: 2},
		"coverage above 1 is clamped":         {complexity: 5, coverage: 1.5, want: 5},
		"negative coverage is clamped":        {complexity: 5, coverage: -1, want: 30},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			assert.InDelta(t, tt.want, crapScore(tt.complexity, tt.coverage), 1e-9)
		})
	}
}

func TestCrapScoreCrossesThresholdWhereExpected(t *testing.T) {
	t.Parallel()

	// At a threshold of 7 an untested function may reach complexity 2 (6);
	// beyond that it needs tests, and complexity 7 is the ceiling even when
	// fully covered, because CRAP is never lower than the complexity itself.
	assert.Less(t, crapScore(2, 0), defaultThreshold)
	assert.Greater(t, crapScore(3, 0), defaultThreshold)
	assert.Less(t, crapScore(3, 1), defaultThreshold)
	assert.InDelta(t, defaultThreshold, crapScore(7, 1), 1e-9)
	assert.Greater(t, crapScore(8, 1), defaultThreshold)
}

func TestParseGocyclo(t *testing.T) {
	t.Parallel()

	got, err := parseGocyclo(strings.NewReader(strings.Join([]string{
		"4 engine (*Engine).Run internal/engine/engine.go:26:1",
		"",
		"2 config Load internal/config/config.go:32:1",
		"Average: 3.00",
	}, "\n")))
	require.NoError(t, err)

	want := []Func{
		{File: "internal/engine/engine.go", Line: 26, Package: "engine", Name: "(*Engine).Run", Complexity: 4},
		{File: "internal/config/config.go", Line: 32, Package: "config", Name: "Load", Complexity: 2},
	}
	assert.Empty(t, cmp.Diff(want, got))
}

func TestParseGocycloRejectsGarbage(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"too few fields":          "4 engine Run",
		"complexity not a number": "many engine Run internal/engine/engine.go:26:1",
		"line not a number":       "4 engine Run internal/engine/engine.go:xx:1",
		"location without line":   "4 engine Run internal/engine/engine.go",
	}

	for name, line := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := parseGocyclo(strings.NewReader(line))
			require.Error(t, err)
		})
	}
}

func TestParseCoverFuncStripsModulePrefix(t *testing.T) {
	t.Parallel()

	got, err := parseCoverFunc(strings.NewReader(strings.Join([]string{
		"github.com/me/mod/internal/engine/engine.go:26:\tRun\t85.7%",
		"github.com/me/mod/internal/config/config.go:32:\tLoad\t100.0%",
		"total:\t\t(statements)\t59.4%",
	}, "\n")), "github.com/me/mod")
	require.NoError(t, err)

	assert.InDelta(t, 0.857, got["internal/engine/engine.go:26"], 1e-9)
	assert.InDelta(t, 1.0, got["internal/config/config.go:32"], 1e-9)
	assert.Len(t, got, 2, "the total line must be skipped")
}

func TestParseCoverFuncWithoutModulePathKeepsPaths(t *testing.T) {
	t.Parallel()

	got, err := parseCoverFunc(strings.NewReader("engine.go:26:\tRun\t50.0%"), "")
	require.NoError(t, err)

	assert.InDelta(t, 0.5, got["engine.go:26"], 1e-9)
}

func TestParseCoverFuncRejectsGarbage(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"too few fields": "engine.go:26:\tRun",
		"bad percentage": "engine.go:26:\tRun\tlots%",
		"bad location":   "engine.go\tRun\t50.0%",
	}

	for name, line := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, err := parseCoverFunc(strings.NewReader(line), "")
			require.Error(t, err)
		})
	}
}

func TestJoinMatchesOnFileAndLine(t *testing.T) {
	t.Parallel()

	funcs := []Func{
		{File: "a.go", Line: 10, Name: "Covered", Complexity: 4},
		{File: "a.go", Line: 20, Name: "Missing", Complexity: 4},
	}

	got := join(funcs, map[string]float64{"a.go:10": 1})

	require.Len(t, got, 2)
	// Sorted worst-first: the function absent from the profile counts as 0%.
	assert.Equal(t, "Missing", got[0].Name)
	assert.False(t, got[0].Covered)
	assert.InDelta(t, 20.0, got[0].CRAP, 1e-9)

	assert.Equal(t, "Covered", got[1].Name)
	assert.True(t, got[1].Covered)
	assert.InDelta(t, 4.0, got[1].CRAP, 1e-9)
}

func TestJoinBreaksTiesByLocation(t *testing.T) {
	t.Parallel()

	got := join([]Func{
		{File: "b.go", Line: 1, Complexity: 3},
		{File: "a.go", Line: 1, Complexity: 3},
	}, nil)

	assert.Equal(t, "a.go:1", got[0].Location())
	assert.Equal(t, "b.go:1", got[1].Location())
}

func TestExcludeTests(t *testing.T) {
	t.Parallel()

	got := excludeTests([]Func{
		{File: "a.go"},
		{File: "a_test.go"},
		{File: "sub/b_test.go"},
	})

	require.Len(t, got, 1)
	assert.Equal(t, "a.go", got[0].File)
}

func TestOver(t *testing.T) {
	t.Parallel()

	funcs := []Func{{CRAP: 31}, {CRAP: 30}, {CRAP: 29}}

	assert.Len(t, over(funcs, 30), 1)
	assert.Empty(t, over(funcs, 100))
}

func TestModuleFromGoMod(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "github.com/lezhnev74/sandboxed_agent", moduleFromGoMod("../../go.mod"))
	assert.Empty(t, moduleFromGoMod("does-not-exist.mod"), "a missing go.mod must not be fatal")
}

func TestRunEndToEnd(t *testing.T) {
	dir := t.TempDir()
	gocyclo := writeFile(t, dir, "gocyclo.txt", strings.Join([]string{
		"8 engine Complex internal/engine/engine.go:10:1",
		"2 engine Simple internal/engine/engine.go:40:1",
		"9 engine_test TestBig internal/engine/engine_test.go:5:1",
	}, "\n"))
	coverFunc := writeFile(t, dir, "coverfunc.txt", strings.Join([]string{
		"mod/internal/engine/engine.go:10:\tComplex\t0.0%",
		"mod/internal/engine/engine.go:40:\tSimple\t100.0%",
		"total:\t\t(statements)\t50.0%",
	}, "\n"))
	goMod := writeFile(t, dir, "go.mod", "module mod\n\ngo 1.26\n")

	opts := options{
		gocyclo: gocyclo, coverFunc: coverFunc, goMod: goMod,
		threshold: defaultThreshold, top: 10,
	}

	var out bytes.Buffer
	err := run(opts, nil, &out)

	// Complex: 8^2*1+8 = 72, over the threshold. Simple: fully covered, 2.
	require.ErrorContains(t, err, "1 function(s) above the CRAP threshold")
	assert.Contains(t, out.String(), "72.0")
	assert.Contains(t, out.String(), "engine.Complex")
	assert.NotContains(t, out.String(), "TestBig", "test functions are excluded by default")
	assert.Contains(t, out.String(), "2 function(s) scored, 1 above threshold 7")
}

func TestRunPassesWhenEverythingIsBelowThreshold(t *testing.T) {
	dir := t.TempDir()
	opts := options{
		gocyclo:   writeFile(t, dir, "gocyclo.txt", "2 engine Simple engine.go:40:1"),
		coverFunc: writeFile(t, dir, "coverfunc.txt", "engine.go:40:\tSimple\t100.0%"),
		goMod:     "missing",
		threshold: defaultThreshold, top: 0, jsonOut: true,
	}

	var out bytes.Buffer
	require.NoError(t, run(opts, nil, &out))
	assert.Contains(t, out.String(), `"crap": 2`)
}

func TestRunReportsMissingFiles(t *testing.T) {
	t.Parallel()

	err := run(options{gocyclo: "nope.txt"}, nil, &bytes.Buffer{})
	require.ErrorContains(t, err, "open gocyclo report")

	err = run(options{coverFunc: "nope.txt"}, strings.NewReader(""), &bytes.Buffer{})
	require.ErrorContains(t, err, "open cover report")
}

func TestRunReadsGocycloFromStdin(t *testing.T) {
	dir := t.TempDir()
	opts := options{
		coverFunc: writeFile(t, dir, "coverfunc.txt", "engine.go:40:\tSimple\t100.0%"),
		goMod:     "missing",
		threshold: defaultThreshold, top: 10,
	}

	var out bytes.Buffer
	require.NoError(t, run(opts, strings.NewReader("2 engine Simple engine.go:40:1"), &out))
	assert.Contains(t, out.String(), "engine.Simple")
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()

	path := dir + "/" + name
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))

	return path
}
