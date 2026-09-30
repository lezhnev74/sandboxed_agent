package archtest

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const m = "github.com/lezhnev74/sandboxed_agent"

func pkg(rel string, files ...File) Package {
	return Package{Path: m + "/" + rel, Files: files}
}

func file(name string, imports ...string) File {
	return File{Name: name, Imports: imports}
}

func TestViolations(t *testing.T) {
	tests := map[string]struct {
		pkgs []Package
		want []string
	}{
		"core importing stdlib and other core is fine": {
			pkgs: []Package{pkg("internal/events", file("events.go", "time", m+"/internal/clock", m+"/internal/events"))},
		},
		"core importing third party": {
			pkgs: []Package{pkg("internal/events", file("events.go", "github.com/spf13/cobra"))},
			want: []string{"internal/events/events.go: core package imports non-stdlib github.com/spf13/cobra"},
		},
		"core importing an adapter": {
			pkgs: []Package{pkg("internal/paths", file("paths.go", m+"/internal/box"))},
			want: []string{"internal/paths/paths.go: core package imports adapter " + m + "/internal/box"},
		},
		"core importing side-effect stdlib": {
			pkgs: []Package{pkg("internal/clock", file("clock.go", "net/http", "syscall"))},
			want: []string{
				"internal/clock/clock.go: core package imports side-effect package net/http",
				"internal/clock/clock.go: core package imports side-effect package syscall",
			},
		},
		"nested core under an adapter dir is core": {
			pkgs: []Package{pkg("internal/bundle/plan", file("plan.go", m+"/internal/bundle"))},
			want: []string{"internal/bundle/plan/plan.go: core package imports adapter " + m + "/internal/bundle"},
		},
		"adapter importing core, its own core child and third party is fine": {
			pkgs: []Package{pkg("internal/bundle", file("bundle.go", m+"/internal/bundle/plan", m+"/internal/paths", "gopkg.in/yaml.v3"))},
		},
		"adapter importing another adapter": {
			pkgs: []Package{pkg("internal/box", file("box.go", m+"/internal/image"))},
			want: []string{"internal/box/box.go: adapter imports another adapter " + m + "/internal/image"},
		},
		"a new unlisted package is an adapter": {
			pkgs: []Package{pkg("internal/newthing", file("n.go", m+"/internal/box"))},
			want: []string{"internal/newthing/n.go: adapter imports another adapter " + m + "/internal/box"},
		},
		"driver composes adapters": {
			pkgs: []Package{pkg("internal/driver", file("driver.go", m+"/internal/box", m+"/internal/runs", m+"/internal/events"))},
		},
		"cmd composes everything": {
			pkgs: []Package{pkg("cmd/sandboxed_agent", file("main.go", m+"/internal/driver", m+"/internal/box"))},
		},
		"os/exec outside an _exec.go file": {
			pkgs: []Package{
				pkg("internal/box", file("box.go", "os/exec")),
				pkg("cmd/sandboxed_agent", file("main.go", "os/exec")),
			},
			want: []string{
				"internal/box/box.go: os/exec is allowed only in *_exec.go files",
				"cmd/sandboxed_agent/main.go: os/exec is allowed only in *_exec.go files",
			},
		},
		"os/exec in an _exec.go file of an adapter is fine": {
			pkgs: []Package{pkg("internal/box", file("box_exec.go", "os/exec"))},
		},
		"os/exec in an _exec.go file of a core package still fails": {
			pkgs: []Package{pkg("internal/events", file("events_exec.go", "os/exec"))},
			want: []string{"internal/events/events_exec.go: core package imports side-effect package os/exec"},
		},
		"os/exec in a unit test": {
			pkgs: []Package{pkg("internal/box", file("box_test.go", "os/exec"))},
			want: []string{"internal/box/box_test.go: os/exec is allowed only in *_exec.go files"},
		},
		"os/exec in an integration test is fine": {
			pkgs: []Package{pkg("internal/box", file("box_integration_test.go", "os/exec"))},
		},
		"tools are unconstrained": {
			pkgs: []Package{pkg("tools/crap", file("main.go", "os/exec", m+"/internal/box"))},
		},
		"core test files may import test libraries": {
			pkgs: []Package{pkg("internal/events", file("events_test.go", "github.com/stretchr/testify/assert"))},
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tt.want, Violations(m, tt.pkgs))
		})
	}
}

func TestKind(t *testing.T) {
	tests := map[string]Kind{
		"internal/clock":        Core,
		"internal/runs/spec":    Core,
		"internal/runs":         Adapter,
		"internal/box/sub":      Adapter,
		"internal/driver":       Driver,
		"internal/archtest":     Root,
		"cmd/sandboxed_agent":   Root,
		"tools/gatesum":         Root,
		"internal/events/inner": Core,
	}

	for rel, want := range tests {
		assert.Equal(t, want, kindOf(rel), rel)
	}
}
