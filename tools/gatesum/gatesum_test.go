package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tree() fstest.MapFS {
	return fstest.MapFS{
		".golangci.yml":                  {Data: []byte("lint")},
		".testcoverage.yml":              {Data: []byte("cover")},
		"Makefile":                       {Data: []byte("make")},
		"tools/crap/main.go":             {Data: []byte("crap")},
		"internal/archtest/arch_test.go": {Data: []byte("arch")},
		"internal/clock/clock.go":        {Data: []byte("not guarded")},
		"README.md":                      {Data: []byte("not guarded")},
	}
}

func TestSumCoversOnlyGuardedFilesSorted(t *testing.T) {
	sums, err := Sum(tree())
	require.NoError(t, err)

	paths := make([]string, 0, len(sums))
	for _, s := range sums {
		paths = append(paths, s.Path)
	}

	assert.Equal(t, []string{
		".golangci.yml",
		".testcoverage.yml",
		"Makefile",
		"internal/archtest/arch_test.go",
		"tools/crap/main.go",
	}, paths)
}

func TestSumMissingTopLevelFileIsSkipped(t *testing.T) {
	fsys := tree()
	delete(fsys, "Makefile")

	sums, err := Sum(fsys)
	require.NoError(t, err)
	assert.Len(t, sums, 4)
}

func TestFormatParseRoundTrip(t *testing.T) {
	sums, err := Sum(tree())
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, Format(&buf, sums))

	back, err := Parse(&buf)
	require.NoError(t, err)
	assert.Equal(t, sums, back)
}

func TestFormatIsSha256sumCompatible(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, Format(&buf, []Entry{{Path: "Makefile", Hash: "abc"}}))

	assert.Equal(t, "abc  Makefile\n", buf.String())
}

func TestParseRejectsMalformedLine(t *testing.T) {
	_, err := Parse(strings.NewReader("onlyonefield\n"))

	require.Error(t, err)
}

func TestDiff(t *testing.T) {
	base, err := Sum(tree())
	require.NoError(t, err)

	edited := tree()
	edited[".golangci.yml"] = &fstest.MapFile{Data: []byte("weakened")}
	edited["tools/new/main.go"] = &fstest.MapFile{Data: []byte("new")}
	delete(edited, "Makefile")

	now, err := Sum(edited)
	require.NoError(t, err)

	assert.Equal(t, []string{
		"changed: .golangci.yml",
		"removed: Makefile",
		"added: tools/new/main.go",
	}, Diff(base, now))
	assert.Empty(t, Diff(base, base))
}

func TestRunVerifyAndWrite(t *testing.T) {
	fsys := tree()
	var out bytes.Buffer

	var sums bytes.Buffer
	require.NoError(t, run(fsys, modeWrite, nil, &sums, &out))

	require.NoError(t, run(fsys, modeVerify, bytes.NewReader(sums.Bytes()), nil, &out))

	fsys["Makefile"] = &fstest.MapFile{Data: []byte("edited")}
	out.Reset()
	err := run(fsys, modeVerify, bytes.NewReader(sums.Bytes()), nil, &out)

	require.ErrorIs(t, err, errGatesChanged)
	assert.Contains(t, out.String(), "changed: Makefile")
}

func TestRunFilesWritesThenVerifiesOnDisk(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	require.Error(t, runFiles(false), "verify without a sums file must fail")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("make"), 0o600))
	require.NoError(t, runFiles(true))
	require.NoError(t, runFiles(false))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("edited"), 0o600))
	require.ErrorIs(t, runFiles(false), errGatesChanged)
}
