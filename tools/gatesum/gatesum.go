package main

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"strings"
)

// guardedFiles are single files; guardedDirs are walked recursively.
var (
	guardedFiles = []string{".golangci.yml", ".testcoverage.yml", "Makefile"}
	guardedDirs  = []string{"tools", "internal/archtest"}
)

// Entry is one guarded file and the hex sha256 of its content.
type Entry struct {
	Path string
	Hash string
}

// Sum hashes every guarded file in fsys, sorted by path.
func Sum(fsys fs.FS) ([]Entry, error) {
	paths, err := guardedPaths(fsys)
	if err != nil {
		return nil, err
	}

	entries := make([]Entry, 0, len(paths))
	for _, p := range paths {
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", p, err)
		}

		h := sha256.Sum256(data)
		entries = append(entries, Entry{Path: p, Hash: hex.EncodeToString(h[:])})
	}

	return entries, nil
}

func guardedPaths(fsys fs.FS) ([]string, error) {
	var paths []string

	for _, f := range guardedFiles {
		if _, err := fs.Stat(fsys, f); err == nil {
			paths = append(paths, f)
		}
	}

	for _, dir := range guardedDirs {
		found, err := filesUnder(fsys, dir)
		if err != nil {
			return nil, err
		}

		paths = append(paths, found...)
	}

	sort.Strings(paths)

	return paths, nil
}

func filesUnder(fsys fs.FS, dir string) ([]string, error) {
	if _, err := fs.Stat(fsys, dir); err != nil {
		return nil, nil //nolint:nilerr // an absent guarded dir has nothing to guard
	}

	var paths []string

	err := fs.WalkDir(fsys, dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if !d.IsDir() {
			paths = append(paths, p)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk %s: %w", dir, err)
	}

	return paths, nil
}

// Format writes entries in `sha256sum` format.
func Format(w io.Writer, entries []Entry) error {
	for _, e := range entries {
		if _, err := fmt.Fprintf(w, "%s  %s\n", e.Hash, e.Path); err != nil {
			return fmt.Errorf("write sums: %w", err)
		}
	}

	return nil
}

// Parse reads entries written by Format.
func Parse(r io.Reader) ([]Entry, error) {
	var entries []Entry

	sc := bufio.NewScanner(r)
	for sc.Scan() {
		hash, path, ok := strings.Cut(sc.Text(), "  ")
		if !ok {
			return nil, fmt.Errorf("%w: %q", errMalformed, sc.Text())
		}

		entries = append(entries, Entry{Path: path, Hash: hash})
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("read sums: %w", err)
	}

	return entries, nil
}

var errMalformed = errors.New("malformed sums line")

// Diff lists what changed from want to got: changed and removed files in
// want's order, then added files in got's order.
func Diff(want, got []Entry) []string {
	gotBy := index(got)
	wantBy := index(want)

	var diffs []string

	for _, e := range want {
		h, ok := gotBy[e.Path]

		switch {
		case !ok:
			diffs = append(diffs, "removed: "+e.Path)
		case h != e.Hash:
			diffs = append(diffs, "changed: "+e.Path)
		}
	}

	for _, e := range got {
		if _, ok := wantBy[e.Path]; !ok {
			diffs = append(diffs, "added: "+e.Path)
		}
	}

	return diffs
}

func index(entries []Entry) map[string]string {
	m := make(map[string]string, len(entries))
	for _, e := range entries {
		m[e.Path] = e.Hash
	}

	return m
}
