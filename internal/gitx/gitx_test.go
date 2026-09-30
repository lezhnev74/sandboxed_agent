package gitx

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestArgv(t *testing.T) {
	assert.Equal(t, []string{"git", "-C", "/w", "config", "--get", "user.name"}, IdentityArgv("/w", "user.name"))
}

func TestConfigFile(t *testing.T) {
	tests := []struct {
		name string
		id   Identity
		want string
	}{
		{"none", Identity{}, "[safe]\n\tdirectory = *\n"},
		{
			"both", Identity{Name: "Ann Lee", Email: "a@x.io"},
			"[user]\n\tname = \"Ann Lee\"\n\temail = \"a@x.io\"\n[safe]\n\tdirectory = *\n",
		},
		{
			"escaped", Identity{Name: `A "B" \ ;#`},
			"[user]\n\tname = \"A \\\"B\\\" \\\\ ;#\"\n[safe]\n\tdirectory = *\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ConfigFile(tc.id))
		})
	}
}

func TestWriteConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gitconfig")

	require.NoError(t, WriteConfig(path, Identity{Name: "n"}))

	b, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, ConfigFile(Identity{Name: "n"}), string(b))

	require.Error(t, WriteConfig(filepath.Join(path, "x"), Identity{}))
}
