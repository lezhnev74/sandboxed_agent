package box

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// DindVolumePrefix starts the name of every box's dind volume.
const DindVolumePrefix = "sandboxed_agent-dind-"

// VolumesArgv lists the volumes whose name contains substr.
func VolumesArgv(substr string) []string {
	return []string{docker, "volume", "ls", "-q", flagFilter, "name=" + substr}
}

// NamesArgv lists the name of every container.
func NamesArgv() []string { return []string{docker, "ps", "-a", flagFormat, "{{.Names}}"} }

// VolumeRemoveArgv removes one volume; it fails while a container uses it.
func VolumeRemoveArgv(name string) []string { return []string{docker, "volume", "rm", name} }

// OrphanDind picks the dind volumes (names starting with prefix, itself
// starting with DindVolumePrefix) whose box and sidecar are both gone.
func OrphanDind(volumes, containers []string, prefix string) []string {
	var out []string

	for _, v := range volumes {
		if !strings.HasPrefix(v, prefix) || !strings.HasPrefix(v, DindVolumePrefix) {
			continue
		}

		b := "sandboxed_agent-" + strings.TrimPrefix(v, DindVolumePrefix)
		if !slices.Contains(containers, b) && !slices.Contains(containers, SidecarName(b)) {
			out = append(out, v)
		}
	}

	return out
}

// PruneDind removes the dind volumes named prefix* of boxes that no longer
// exist and returns their names. A volume that fails to go is reported in
// the error; the others are still removed.
func (m Manager) PruneDind(ctx context.Context, prefix string) ([]string, error) {
	vols, err := m.output(ctx, VolumesArgv(prefix))
	if err != nil {
		return nil, err
	}

	names, err := m.output(ctx, NamesArgv())
	if err != nil {
		return nil, err
	}

	var (
		removed []string
		errs    []error
	)

	for _, v := range OrphanDind(strings.Fields(vols), strings.Fields(names), prefix) {
		if _, err := m.output(ctx, VolumeRemoveArgv(v)); err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", v, err))

			continue
		}

		removed = append(removed, v)
	}

	return removed, errors.Join(errs...)
}
