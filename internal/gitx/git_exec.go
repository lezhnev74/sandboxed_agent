package gitx

import (
	"context"
	"os/exec"
	"strings"
)

// ReadIdentity reads user.name and user.email as git sees them in dir;
// unset keys stay empty.
func ReadIdentity(ctx context.Context, dir string) Identity {
	return Identity{Name: gitOut(ctx, IdentityArgv(dir, "user.name")), Email: gitOut(ctx, IdentityArgv(dir, "user.email"))}
}

func gitOut(ctx context.Context, argv []string) string {
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).Output() //nolint:gosec // argv is built by this package's pure functions
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(out))
}
