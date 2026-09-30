// Package archtest enforces the core/adapter layering from
// docs/quality-gates.md as a test, so the rule survives edits to the linter
// config. The rules restate the depguard rules in .golangci.yml:
//
//   - core (events, harness, bundle/plan, runs/spec, paths, clock and
//     anything below them) imports only the standard library and other core
//     packages, and no side-effect stdlib package (os/exec, net, syscall...);
//   - every other internal package is an adapter (membership by exclusion):
//     it may import core and third-party code but never another adapter;
//   - driver composes adapters, as does the root (cmd/, tools/);
//   - os/exec is imported only by *_exec.go files and integration tests
//     (tools/ and archtest itself excepted: they run the go tool).
package archtest

import (
	"path"
	"strings"
)

// Kind is a package's layer.
type Kind int

// The layers.
const (
	Root Kind = iota
	Core
	Adapter
	Driver
)

// corePackages are module-relative; their sub-packages are core too.
var corePackages = []string{
	"internal/events",
	"internal/harness",
	"internal/bundle/plan",
	"internal/runs/spec",
	"internal/paths",
	"internal/clock",
}

// sideEffectStdlib may not be imported by core, also by prefix ("net/http").
var sideEffectStdlib = []string{"os/exec", "os/signal", "net", "syscall", "plugin", "unsafe"}

// File is one Go file and its imports.
type File struct {
	Name    string
	Imports []string
}

// Package is one package of the module with every file, tests included.
type Package struct {
	Path  string
	Files []File
}

// Violations lists every broken layering rule as "rel/file.go: reason".
func Violations(module string, pkgs []Package) []string {
	var out []string

	for _, p := range pkgs {
		rel := strings.TrimPrefix(strings.TrimPrefix(p.Path, module), "/")
		kind := kindOf(rel)

		for _, f := range p.Files {
			where := path.Join(rel, f.Name) + ": "
			for _, imp := range f.Imports {
				if reason := check(module, kind, f.Name, rel, imp); reason != "" {
					out = append(out, where+reason)
				}
			}
		}
	}

	return out
}

func check(module string, kind Kind, name, rel, imp string) string {
	if kind == Core && !isTest(name) {
		return checkCore(module, imp)
	}

	if reason := checkExec(rel, name, imp); reason != "" {
		return reason
	}

	if kind == Adapter && !isTest(name) {
		return checkAdapter(module, rel, imp)
	}

	return ""
}

func checkCore(module, imp string) string {
	switch {
	case isSideEffect(imp):
		return "core package imports side-effect package " + imp
	case strings.HasPrefix(imp, module+"/"):
		if kindOf(strings.TrimPrefix(imp, module+"/")) != Core {
			return "core package imports adapter " + imp
		}
	case !isStdlib(imp):
		return "core package imports non-stdlib " + imp
	}

	return ""
}

// checkExec applies everywhere but tools/ and archtest itself, which run go.
func checkExec(rel, name, imp string) string {
	if imp != "os/exec" || strings.HasPrefix(rel, "tools/") || topDir(rel) == "internal/archtest" {
		return ""
	}

	if strings.HasSuffix(name, "_exec.go") || strings.HasSuffix(name, "_integration_test.go") {
		return ""
	}

	return "os/exec is allowed only in *_exec.go files"
}

func checkAdapter(module, rel, imp string) string {
	target, ok := strings.CutPrefix(imp, module+"/")
	if !ok || kindOf(target) != Adapter || topDir(target) == topDir(rel) {
		return ""
	}

	return "adapter imports another adapter " + imp
}

func kindOf(rel string) Kind {
	if !strings.HasPrefix(rel, "internal/") || topDir(rel) == "internal/archtest" {
		return Root
	}

	for _, c := range corePackages {
		if rel == c || strings.HasPrefix(rel, c+"/") {
			return Core
		}
	}

	if topDir(rel) == "internal/driver" {
		return Driver
	}

	return Adapter
}

// topDir is "internal/<name>" for any package below internal/.
func topDir(rel string) string {
	parts := strings.SplitN(rel, "/", 3)
	if len(parts) < 2 {
		return rel
	}

	return parts[0] + "/" + parts[1]
}

func isTest(name string) bool { return strings.HasSuffix(name, "_test.go") }

func isSideEffect(imp string) bool {
	for _, s := range sideEffectStdlib {
		if imp == s || strings.HasPrefix(imp, s+"/") {
			return true
		}
	}

	return false
}

// isStdlib: stdlib paths have no dot in their first element.
func isStdlib(imp string) bool {
	first, _, _ := strings.Cut(imp, "/")

	return !strings.Contains(first, ".")
}
