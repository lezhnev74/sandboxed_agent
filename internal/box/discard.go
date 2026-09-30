package box

import "context"

// Discard removes a throwaway (--fresh) box with everything it had: its
// sidecar, its socket and dind volumes.
func (m Manager) Discard(ctx context.Context, name string) error {
	if err := m.Down(ctx, name); err != nil {
		return err
	}

	_, err := m.output(ctx, VolumeForceRemoveArgv(DindVolume(name)))

	return err
}

// VolumeForceRemoveArgv removes a volume; an absent one is no error.
func VolumeForceRemoveArgv(name string) []string { return append(VolumeRemoveArgv("-f"), name) }
