package archtest

import (
	"encoding/json"
	"errors"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// listed is the subset of `go list -json` this test reads.
type listed struct {
	ImportPath   string
	Dir          string
	GoFiles      []string
	TestGoFiles  []string
	XTestGoFiles []string
}

// TestModuleLayering runs the rules over the real module, integration tests
// included.
func TestModuleLayering(t *testing.T) {
	module := goOutput(t, "list", "-m")
	raw := goOutput(t, "list", "-tags", "integration", "-json", "./...")

	pkgs := decode(t, raw)
	require.NotEmpty(t, pkgs)

	assert.Empty(t, Violations(module, pkgs),
		"layering broken - see docs/quality-gates.md \"Architecture guard\"")
}

func goOutput(t *testing.T, args ...string) string {
	t.Helper()

	cmd := exec.Command("go", args...)
	cmd.Dir = moduleRoot(t)
	out, err := cmd.Output()
	require.NoError(t, err, "go %v", args)

	return strings.TrimSpace(string(out))
}

func moduleRoot(t *testing.T) string {
	t.Helper()

	out, err := exec.Command("go", "env", "GOMOD").Output()
	require.NoError(t, err)

	return filepath.Dir(strings.TrimSpace(string(out)))
}

func decode(t *testing.T, raw string) []Package {
	t.Helper()

	var pkgs []Package

	dec := json.NewDecoder(strings.NewReader(raw))
	for {
		var l listed

		err := dec.Decode(&l)
		if errors.Is(err, io.EOF) {
			return pkgs
		}

		require.NoError(t, err)
		pkgs = append(pkgs, Package{Path: l.ImportPath, Files: parseFiles(t, l)})
	}
}

func parseFiles(t *testing.T, l listed) []File {
	t.Helper()

	names := append(append(append([]string{}, l.GoFiles...), l.TestGoFiles...), l.XTestGoFiles...)
	files := make([]File, 0, len(names))

	for _, name := range names {
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(l.Dir, name), nil, parser.ImportsOnly)
		require.NoError(t, err)

		var imports []string

		for _, spec := range f.Imports {
			p, err := strconv.Unquote(spec.Path.Value)
			require.NoError(t, err)

			imports = append(imports, p)
		}

		files = append(files, File{Name: name, Imports: imports})
	}

	return files
}
