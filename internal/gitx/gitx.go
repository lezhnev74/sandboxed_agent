// Package gitx reads the host git identity and writes the box's generated
// GIT_CONFIG_GLOBAL file.
// The git argv and file contents are decided here; git_exec.go runs git.
package gitx

import (
	"fmt"
	"os"
	"strings"
)

const git = "git"

// Identity is the host's git author.
type Identity struct {
	Name  string
	Email string
}

// IdentityArgv reads one identity key as git sees it in dir (repo-local
// config wins over the global one).
func IdentityArgv(dir, key string) []string {
	return []string{git, "-C", dir, "config", "--get", key}
}

// ConfigFile is the box's global git config: the host identity (when set)
// and safe.directory = *, since the workspace is owned by the host uid.
func ConfigFile(id Identity) string {
	var b strings.Builder
	if id.Name != "" || id.Email != "" {
		b.WriteString("[user]\n")
		writeKey(&b, "name", id.Name)
		writeKey(&b, "email", id.Email)
	}

	b.WriteString("[safe]\n\tdirectory = *\n")

	return b.String()
}

func writeKey(b *strings.Builder, key, val string) {
	if val != "" {
		b.WriteString("\t" + key + " = " + quote(val) + "\n")
	}
}

// quote makes val a git config string: quoted, with \ and " escaped.
func quote(val string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\t", `\t`)

	return `"` + r.Replace(val) + `"`
}

// WriteConfig writes ConfigFile(id) to path.
func WriteConfig(path string, id Identity) error {
	if err := os.WriteFile(path, []byte(ConfigFile(id)), 0o600); err != nil {
		return fmt.Errorf("gitx: %w", err)
	}

	return nil
}
