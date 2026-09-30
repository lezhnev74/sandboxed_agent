// Command gatesum guards the quality-gate files against silent edits.
//
//	go run ./tools/gatesum -write   # rewrite .gates.sha256 (make gates-bless, human only)
//	go run ./tools/gatesum -verify  # fail when a guarded file differs from .gates.sha256
//
// Guarded: .golangci.yml, .testcoverage.yml, Makefile, tools/** and
// internal/archtest/**.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
)

const sumsFile = ".gates.sha256"

type mode int

const (
	modeVerify mode = iota
	modeWrite
)

var errGatesChanged = errors.New("guarded gate files changed without `make gates-bless`")

func main() {
	write := flag.Bool("write", false, "rewrite "+sumsFile)
	flag.Bool("verify", true, "verify against "+sumsFile+" (default)")
	flag.Parse()

	if err := runFiles(*write); err != nil {
		fmt.Fprintln(os.Stderr, "gatesum:", err)
		os.Exit(1)
	}
}

func runFiles(write bool) error {
	fsys := os.DirFS(".")

	if write {
		f, err := os.Create(sumsFile)
		if err != nil {
			return err
		}
		defer f.Close()

		return run(fsys, modeWrite, nil, f, os.Stdout)
	}

	f, err := os.Open(sumsFile)
	if err != nil {
		return fmt.Errorf("%w (run `make gates-bless` once, human only)", err)
	}
	defer f.Close()

	return run(fsys, modeVerify, f, nil, os.Stderr)
}

// run writes the current sums to sumsOut (modeWrite) or compares them with
// sumsIn and lists every difference on out (modeVerify).
func run(fsys fs.FS, m mode, sumsIn io.Reader, sumsOut, out io.Writer) error {
	got, err := Sum(fsys)
	if err != nil {
		return err
	}

	if m == modeWrite {
		return Format(sumsOut, got)
	}

	want, err := Parse(sumsIn)
	if err != nil {
		return err
	}

	diffs := Diff(want, got)
	for _, d := range diffs {
		fmt.Fprintln(out, d)
	}

	if len(diffs) > 0 {
		return errGatesChanged
	}

	return nil
}
