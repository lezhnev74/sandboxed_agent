package box

import (
	"context"
	"fmt"
	"os/exec"
)

// Exec runs docker commands for real.
type Exec struct{}

// Run runs c and fails on a non-zero exit; the error wraps *exec.ExitError.
func (Exec) Run(ctx context.Context, c Cmd) error {
	cmd := exec.CommandContext(ctx, c.Argv[0], c.Argv[1:]...) //nolint:gosec // argv is built by this package's pure functions
	cmd.Stdin, cmd.Stdout, cmd.Stderr = c.Stdin, c.Stdout, c.Stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %w", c.Argv[0], err)
	}

	return nil
}
