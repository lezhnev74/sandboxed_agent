package box

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGCArgvs(t *testing.T) {
	assert.Equal(t, []string{"docker", "volume", "ls", "-q", "--filter", "name=sandboxed_agent-dind-"},
		VolumesArgv(DindVolumePrefix))
	assert.Equal(t, []string{"docker", "ps", "-a", "--format", "{{.Names}}"}, NamesArgv())
	assert.Equal(t, []string{"docker", "volume", "rm", "v"}, VolumeRemoveArgv("v"))
}

func TestOrphanDind(t *testing.T) {
	vols := []string{
		"sandboxed_agent-dind-a-1", // box and sidecar exist
		"sandboxed_agent-dind-b-2", // only the sidecar exists
		"sandboxed_agent-dind-c-3", // both gone
		"sandboxed_agent-mise",     // not a dind volume
		"x-sandboxed_agent-dind-d", // contains the prefix, doesn't start with it
	}
	names := []string{"sandboxed_agent-a-1", "sandboxed_agent-a-1-dind", "sandboxed_agent-b-2-dind", "sandboxed_agent-c-30"}

	assert.Equal(t, []string{"sandboxed_agent-dind-c-3"}, OrphanDind(vols, names, DindVolumePrefix))
	assert.Empty(t, OrphanDind(vols, names, DindVolumePrefix+"b-"))
	assert.Empty(t, OrphanDind([]string{"other-c-3"}, nil, "other-"), "only dind volumes")
}

func TestPruneDind(t *testing.T) {
	f := &fake{
		out: map[string][]string{
			"docker volume": {"sandboxed_agent-dind-a\nsandboxed_agent-dind-b\nsandboxed_agent-dind-c\n"},
			"docker ps":     {"sandboxed_agent-a\n"},
		},
		failAt: map[string][]int{"docker volume": {3}}, // ls, rm b, rm c
	}
	m := Manager{Runner: f}

	removed, err := m.PruneDind(context.Background(), DindVolumePrefix)
	require.ErrorIs(t, err, errDocker)
	assert.Contains(t, err.Error(), "sandboxed_agent-dind-c")
	assert.Equal(t, []string{"sandboxed_agent-dind-b"}, removed)
	assert.Equal(t, VolumeRemoveArgv("sandboxed_agent-dind-b"), f.calls[2])
}

func TestPruneDindListFails(t *testing.T) {
	for _, verb := range []string{"docker volume", "docker ps"} {
		f := &fake{fail: map[string]error{verb: errDocker}}

		_, err := Manager{Runner: f}.PruneDind(context.Background(), DindVolumePrefix)
		require.ErrorIs(t, err, errDocker, verb)
	}
}
