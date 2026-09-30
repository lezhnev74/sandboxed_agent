package box

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOwnArgv(t *testing.T) {
	assert.Equal(t, []string{
		"find", "/ws", "(", "!", "-user", "1000", "-o", "!", "-group", "1001", ")",
		"-exec", "chown", "-h", "1000:1001", "{}", "+",
	}, OwnArgv("/ws", 1000, 1001))
}

func TestOwn(t *testing.T) {
	f := &fake{}

	require.NoError(t, Manager{Runner: f}.Own(context.Background(), "b", "/ws", 1000, 1001))
	assert.Equal(t, [][]string{ExecArgv("b", ExecOpts{User: "0"}, OwnArgv("/ws", 1000, 1001))}, f.calls)

	f = &fake{fail: map[string]error{"docker exec": errDocker}}
	require.ErrorIs(t, Manager{Runner: f}.Own(context.Background(), "b", "/ws", 1, 1), errDocker)
}
