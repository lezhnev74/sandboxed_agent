package image

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteContext(t *testing.T) {
	var buf bytes.Buffer
	require.NoError(t, WriteContext(&buf))

	modes := map[string]int64{}
	bodies := map[string]string{}
	tr := tar.NewReader(&buf)
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		require.NoError(t, err)
		body, err := io.ReadAll(tr)
		require.NoError(t, err)
		modes[h.Name] = h.Mode
		bodies[h.Name] = string(body)
	}

	assert.Equal(t, map[string]int64{
		"Dockerfile":           0o644,
		"sandboxed_agent-exec": 0o755,
		"sandboxed_agent-idle": 0o755,
	}, modes)
	assert.Contains(t, bodies["Dockerfile"], "COPY sandboxed_agent-exec sandboxed_agent-idle /usr/local/bin/")
	assert.Contains(t, bodies["sandboxed_agent-exec"], "setsid")
	assert.Contains(t, bodies["sandboxed_agent-idle"], "ps -e -o ppid=")
}

func TestBuildArgv(t *testing.T) {
	got := BuildArgv(Options{UID: 1000, GID: 1001, HarnessRev: "r1"})

	assert.Equal(t, []string{
		"docker", "build", "-t", "sandboxed_agent:latest",
		"--build-arg", "UID=1000", "--build-arg", "GID=1001", "--build-arg", "HARNESS_REV=r1",
		"-",
	}, got)
}

func TestBuildArgvCustomTagNoRev(t *testing.T) {
	got := BuildArgv(Options{Tag: "x:1", UID: 5, GID: 6})

	assert.Equal(t, []string{
		"docker", "build", "-t", "x:1", "--build-arg", "UID=5", "--build-arg", "GID=6", "-",
	}, got)
}

func TestVersionsArgv(t *testing.T) {
	assert.Equal(t, []string{
		"docker", "run", "--rm", "--entrypoint", "sh", "sandboxed_agent:latest",
		"-c", "claude --version && opencode --version",
	}, VersionsArgv(""))
}

func TestLabelArgv(t *testing.T) {
	assert.Equal(t, []string{
		"docker", "build", "-t", "sandboxed_agent:latest",
		"--label", "sandboxed_agent.claude.version=2.1.284",
		"--label", "sandboxed_agent.opencode.version=2.0.10",
		"--label", "sandboxed_agent.assets=" + AssetsHash(),
		"-",
	}, LabelArgv("", Versions{Claude: "2.1.284", OpenCode: "2.0.10"}))
}

func TestExistsArgv(t *testing.T) {
	assert.Equal(t, []string{"docker", "image", "inspect", "--format", `{{index .Config.Labels "sandboxed_agent.assets"}}`, "x:1"},
		ExistsArgv("x:1"))
}

func TestParseVersions(t *testing.T) {
	tests := []struct {
		name string
		out  string
		want Versions
		err  bool
	}{
		{"real", "2.1.284 (Claude Code)\nopencode v2.0.10\n", Versions{"2.1.284", "2.0.10"}, false},
		{"bare", "\n2.1.284\n\n2.0.10\n", Versions{"2.1.284", "2.0.10"}, false},
		{"one line", "2.1.284 (Claude Code)\n", Versions{}, true},
		{"empty", "", Versions{}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseVersions(tc.out)
			if tc.err {
				require.ErrorIs(t, err, ErrVersions)

				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// fakeRunner records every command and answers from a script.
type fakeRunner struct {
	calls []string
	stdin []string
	reply func(argv []string) (string, error)
}

func (f *fakeRunner) Run(_ context.Context, c Cmd) error {
	f.calls = append(f.calls, strings.Join(c.Argv, " "))
	in := ""
	if c.Stdin != nil {
		b, err := io.ReadAll(c.Stdin)
		if err != nil {
			return err
		}
		in = string(b)
	}
	f.stdin = append(f.stdin, in)

	out, err := f.reply(c.Argv)
	if c.Stdout != nil {
		_, _ = io.WriteString(c.Stdout, out)
	}

	return err
}

var errBoom = errors.New("boom")

func okReply(argv []string) (string, error) {
	if argv[1] == "run" {
		return "2.1.284 (Claude Code)\nopencode v2.0.10\n", nil
	}

	return "", nil
}

func TestBuild(t *testing.T) {
	r := &fakeRunner{reply: okReply}
	var log bytes.Buffer

	v, err := Builder{Runner: r, Log: &log}.Build(context.Background(), Options{UID: 1, GID: 2, HarnessRev: "z"})

	require.NoError(t, err)
	assert.Equal(t, Versions{"2.1.284", "2.0.10"}, v)
	require.Len(t, r.calls, 3)
	assert.Equal(t, strings.Join(BuildArgv(Options{UID: 1, GID: 2, HarnessRev: "z"}), " "), r.calls[0])
	assert.Equal(t, strings.Join(VersionsArgv(""), " "), r.calls[1])
	assert.Equal(t, strings.Join(LabelArgv("", v), " "), r.calls[2])
	assert.NotEmpty(t, r.stdin[0], "the build context goes on stdin")
	assert.Equal(t, "FROM sandboxed_agent:latest\n", r.stdin[2])
}

func TestBuildErrors(t *testing.T) {
	for _, step := range []string{"build", "run", "label"} {
		t.Run(step, func(t *testing.T) {
			calls := 0
			r := &fakeRunner{reply: func(argv []string) (string, error) {
				calls++
				names := map[int]string{1: "build", 2: "run", 3: "label"}
				if names[calls] == step {
					return "", errBoom
				}

				return okReply(argv)
			}}

			_, err := Builder{Runner: r, Log: io.Discard}.Build(context.Background(), Options{})

			require.ErrorIs(t, err, errBoom)
		})
	}
}

func TestBuildBadVersions(t *testing.T) {
	r := &fakeRunner{reply: func([]string) (string, error) { return "", nil }}

	_, err := Builder{Runner: r, Log: io.Discard}.Build(context.Background(), Options{})

	require.ErrorIs(t, err, ErrVersions)
	assert.Len(t, r.calls, 2, "no label step without versions")
}

func TestEnsure(t *testing.T) {
	tests := []struct {
		name    string
		exists  bool
		label   string
		wantLen int
	}{
		{"current: nothing to do", true, AssetsHash() + "\n", 1},
		{"stale: build", true, "old\n", 4},
		{"unlabeled: build", true, "\n", 4},
		{"missing: build", false, "", 4},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &fakeRunner{reply: func(argv []string) (string, error) {
				if argv[1] == "image" && !tc.exists {
					return "", fmt.Errorf("no such image: %w", errBoom)
				}

				if argv[1] == "image" {
					return tc.label, nil
				}

				return okReply(argv)
			}}

			err := Builder{Runner: r, Log: io.Discard}.Ensure(context.Background(), Options{})

			require.NoError(t, err)
			assert.Len(t, r.calls, tc.wantLen)
		})
	}
}

func TestAssetsHash(t *testing.T) {
	assert.Len(t, AssetsHash(), 16)
	first, second := AssetsHash(), AssetsHash()
	assert.Equal(t, first, second, "stable")
}

// The image always installs the newest harnesses: Claude Code's `latest`
// channel and OpenCode's default (latest) release, never a pinned version.
func TestDockerfileInstallsLatest(t *testing.T) {
	b, err := assets.ReadFile("assets/Dockerfile")
	require.NoError(t, err)

	df := string(b)
	assert.Contains(t, df, "install.sh | bash -s latest")
	assert.NotContains(t, df, "bash -s stable")
	assert.NotContains(t, df, "--version ", "opencode is not pinned")
}

// mise is on PATH through its shims, trusts every config and reads go.mod.
func TestDockerfileInstallsMise(t *testing.T) {
	b, err := assets.ReadFile("assets/Dockerfile")
	require.NoError(t, err)

	df := string(b)
	assert.Contains(t, df, "MISE_INSTALL_PATH=/usr/local/bin/mise")
	assert.Contains(t, df, "PATH=/home/agent/.local/share/mise/shims:$PATH")
	assert.Contains(t, df, "MISE_TRUSTED_CONFIG_PATHS=/")
	assert.Contains(t, df, "MISE_IDIOMATIC_VERSION_FILE_ENABLE_TOOLS=go")
}

func TestExists(t *testing.T) {
	r := &fakeRunner{reply: okReply}
	require.NoError(t, Builder{Runner: r, Log: io.Discard}.Exists(context.Background(), "cuda:1"))
	assert.Equal(t, []string{strings.Join(ExistsArgv("cuda:1"), " ")}, r.calls)

	r = &fakeRunner{reply: func([]string) (string, error) { return "", errBoom }}
	err := Builder{Runner: r, Log: io.Discard}.Exists(context.Background(), "cuda:1")
	require.ErrorIs(t, err, ErrNoImage)
	assert.Contains(t, err.Error(), "cuda:1")
}

func TestCustomTag(t *testing.T) {
	a := CustomTag([]byte("FROM x\n"))

	assert.Regexp(t, `^sandboxed_agent-custom:[0-9a-f]{16}$`, a)
	assert.Equal(t, a, CustomTag([]byte("FROM x\n")), "stable")
	assert.NotEqual(t, a, CustomTag([]byte("FROM y\n")), "a changed Dockerfile is another image")
}

func TestFileBuildArgv(t *testing.T) {
	assert.Equal(t, []string{
		"docker", "build", "-t", "c:1", "-f", "/img/Dockerfile",
		"--build-arg", "UID=5", "--build-arg", "GID=6", "/img",
	}, FileBuildArgv(Options{Tag: "c:1", UID: 5, GID: 6}, "/img/Dockerfile"))
}

func writeDockerfile(t *testing.T, body string) string {
	t.Helper()

	file := filepath.Join(t.TempDir(), "Dockerfile")
	require.NoError(t, os.WriteFile(file, []byte(body), 0o600))

	return file
}

func TestEnsureFileBuildsOnce(t *testing.T) {
	file := writeDockerfile(t, "FROM x\n")
	tag := CustomTag([]byte("FROM x\n"))

	r := &fakeRunner{reply: func(argv []string) (string, error) {
		if argv[1] == "image" {
			return "", errBoom // missing
		}

		return "", nil
	}}

	got, err := Builder{Runner: r, Log: io.Discard}.EnsureFile(context.Background(), Options{UID: 5, GID: 6}, file)

	require.NoError(t, err)
	assert.Equal(t, tag, got)
	assert.Equal(t, []string{
		strings.Join(ExistsArgv(tag), " "),
		strings.Join(FileBuildArgv(Options{Tag: tag, UID: 5, GID: 6}, file), " "),
	}, r.calls)

	r = &fakeRunner{reply: okReply}
	got, err = Builder{Runner: r, Log: io.Discard}.EnsureFile(context.Background(), Options{}, file)

	require.NoError(t, err)
	assert.Equal(t, tag, got)
	assert.Len(t, r.calls, 1, "built already: no build")
}

func TestEnsureFileErrors(t *testing.T) {
	r := &fakeRunner{reply: func([]string) (string, error) { return "", errBoom }}
	b := Builder{Runner: r, Log: io.Discard}

	_, err := b.EnsureFile(context.Background(), Options{}, filepath.Join(t.TempDir(), "missing"))
	require.ErrorIs(t, err, os.ErrNotExist)

	_, err = b.EnsureFile(context.Background(), Options{}, writeDockerfile(t, "FROM x\n"))
	require.ErrorIs(t, err, errBoom)
}

func TestDump(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "img")

	files, err := Dump(dir)

	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join(dir, "Dockerfile"),
		filepath.Join(dir, "sandboxed_agent-exec"),
		filepath.Join(dir, "sandboxed_agent-idle"),
	}, files)

	want, err := assets.ReadFile("assets/Dockerfile")
	require.NoError(t, err)
	got, err := os.ReadFile(files[0])
	require.NoError(t, err)
	assert.Equal(t, string(want), string(got))

	fi, err := os.Stat(files[1])
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o755), fi.Mode().Perm(), "scripts stay executable")
}

func TestDumpNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sandboxed_agent-idle"), []byte("mine"), 0o600))

	_, err := Dump(dir)

	require.ErrorIs(t, err, os.ErrExist)
	_, err = os.Stat(filepath.Join(dir, "Dockerfile"))
	require.ErrorIs(t, err, os.ErrNotExist, "nothing is written when any file exists")
}
