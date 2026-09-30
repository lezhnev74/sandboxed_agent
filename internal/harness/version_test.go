package harness

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestVersion(t *testing.T) {
	a := Adapter{Command: []string{"claude", "-p"}}
	assert.Equal(t, []string{"claude", "--version"}, a.VersionArgv())
	assert.Nil(t, Adapter{}.VersionArgv())

	assert.Equal(t, "2.1.284", ParseVersion("2.1.284 (Claude Code)\n"))
	assert.Equal(t, "1.18.33", ParseVersion("\n1.18.33\n"))
	assert.Empty(t, ParseVersion("  \n"))
}
