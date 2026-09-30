// Command crap computes the CRAP index (Change Risk Anti-Patterns) for every
// function in the module and fails when any of them exceeds a threshold.
//
// It joins two existing reports rather than re-analysing the source:
//
//	go tool gocyclo -over 0 . > gocyclo.txt
//	go tool cover -func=coverage.out > coverfunc.txt
//	go run ./tools/crap -gocyclo gocyclo.txt -coverfunc coverfunc.txt
//
// With -gocyclo omitted the gocyclo report is read from stdin.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"
)

const (
	// defaultThreshold is the CRAP limit: a function scoring above it is either
	// too complex, too untested, or both.
	//
	// Because CRAP is never lower than the complexity itself, a limit of 7
	// also caps cyclomatic complexity at 7 regardless of coverage — the same
	// number gocyclo enforces in .golangci.yml. The two gates agree by design.
	defaultThreshold = 7.0
	exitFailure      = 1
)

type options struct {
	gocyclo      string
	coverFunc    string
	goMod        string
	threshold    float64
	top          int
	jsonOut      bool
	includeTests bool
}

func main() {
	if err := run(parseFlags(), os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "crap:", err)
		os.Exit(exitFailure)
	}
}

func parseFlags() options {
	var opts options

	flag.StringVar(&opts.gocyclo, "gocyclo", "", "gocyclo report file (default: stdin)")
	flag.StringVar(&opts.coverFunc, "coverfunc", "coverfunc.txt", "`go tool cover -func` report file")
	flag.StringVar(&opts.goMod, "gomod", "go.mod", "go.mod used to strip the module prefix from coverage paths")
	flag.Float64Var(&opts.threshold, "threshold", defaultThreshold, "fail when any function scores above this")
	flag.IntVar(&opts.top, "top", 10, "how many worst offenders to print (0 = all)")
	flag.BoolVar(&opts.jsonOut, "json", false, "emit JSON instead of a table")
	flag.BoolVar(&opts.includeTests, "include-tests", false, "score functions declared in _test.go files too")
	flag.Parse()

	return opts
}

func run(opts options, stdin io.Reader, stdout io.Writer) error {
	funcs, err := readGocyclo(opts, stdin)
	if err != nil {
		return err
	}

	if !opts.includeTests {
		funcs = excludeTests(funcs)
	}

	coverage, err := readCoverFunc(opts)
	if err != nil {
		return err
	}

	scored := join(funcs, coverage)
	if err := report(stdout, scored, opts); err != nil {
		return err
	}

	if bad := over(scored, opts.threshold); len(bad) > 0 {
		return fmt.Errorf("%d function(s) above the CRAP threshold of %.0f", len(bad), opts.threshold)
	}

	return nil
}

func readGocyclo(opts options, stdin io.Reader) ([]Func, error) {
	if opts.gocyclo == "" {
		return parseGocyclo(stdin)
	}

	f, err := os.Open(opts.gocyclo)
	if err != nil {
		return nil, fmt.Errorf("open gocyclo report: %w", err)
	}
	defer f.Close()

	return parseGocyclo(f)
}

func readCoverFunc(opts options) (map[string]float64, error) {
	f, err := os.Open(opts.coverFunc)
	if err != nil {
		return nil, fmt.Errorf("open cover report: %w", err)
	}
	defer f.Close()

	return parseCoverFunc(f, moduleFromGoMod(opts.goMod))
}

func report(w io.Writer, funcs []Func, opts options) error {
	if opts.jsonOut {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")

		return enc.Encode(funcs)
	}

	return writeTable(w, funcs, opts)
}

func writeTable(w io.Writer, funcs []Func, opts options) error {
	shown := funcs
	if opts.top > 0 && len(shown) > opts.top {
		shown = shown[:opts.top]
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "CRAP\tCOMPLEXITY\tCOVERAGE\tFUNCTION\tLOCATION")

	for _, fn := range shown {
		fmt.Fprintf(tw, "%.1f\t%d\t%s\t%s.%s\t%s\n",
			fn.CRAP, fn.Complexity, coverageLabel(fn), fn.Package, fn.Name, fn.Location())
	}

	if err := tw.Flush(); err != nil {
		return fmt.Errorf("write table: %w", err)
	}

	fmt.Fprintf(w, "\n%d function(s) scored, %d above threshold %.0f (showing %d)\n",
		len(funcs), len(over(funcs, opts.threshold)), opts.threshold, len(shown))

	return nil
}

func coverageLabel(fn Func) string {
	if !fn.Covered {
		return "n/a"
	}

	return fmt.Sprintf("%.1f%%", fn.Coverage*100)
}
