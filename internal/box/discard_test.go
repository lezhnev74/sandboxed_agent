package box

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageNameLabel(t *testing.T) {
	c := Config{Name: "n", Image: "cuda:1", Workspace: "/w", ImageName: "cuda:1"}

	assert.Contains(t, strings.Join(CreateArgv(c), " "), "--label sandboxed_agent.image=cuda:1")
	assert.NotEqual(t, MountHash(Config{Name: "n", Workspace: "/w"}), MountHash(c))
	assert.NotContains(t, strings.Join(CreateArgv(Config{Name: "n"}), " "), LabelImage)
}

func TestDiscard(t *testing.T) {
	f := &fake{out: map[string][]string{"docker ps": {"cid exited x"}}}

	require.NoError(t, Manager{Runner: f}.Discard(context.Background(), "sandboxed_agent-foo-1-r"))
	assert.Equal(t, []string{"docker", "volume", "rm", "-f", "sandboxed_agent-dind-foo-1-r"}, f.calls[len(f.calls)-1])

	f = &fake{fail: map[string]error{"docker rm": errDocker}}
	require.ErrorIs(t, Manager{Runner: f}.Discard(context.Background(), "b"), errDocker)
}
