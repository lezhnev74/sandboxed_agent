package main

import (
	"context"
	"fmt"
	"io"

	"github.com/lezhnev74/sandboxed_agent/internal/clock"
	"github.com/lezhnev74/sandboxed_agent/internal/image"
)

// rebuildImage rebuilds the image with a fresh harness layer, so both
// harnesses are reinstalled at their latest versions.
func rebuildImage(out, log io.Writer, uid, gid int) func(context.Context) error {
	return func(ctx context.Context) error {
		o := image.Options{UID: uid, GID: gid, HarnessRev: clock.Real{}.Now().UTC().Format("20060102T150405Z")}

		v, err := image.Builder{Runner: image.Exec{}, Log: log}.Build(ctx, o)
		if err != nil {
			return err //nolint:wrapcheck // image errors already name the failing docker step
		}

		_, err = fmt.Fprintf(out, "built %s (claude %s, opencode %s)\n", image.Tag, v.Claude, v.OpenCode)

		return err //nolint:wrapcheck // a failed write to stdout needs no context
	}
}

// dumpImage writes the image's build context into dir and lists the files.
func dumpImage(out io.Writer, dir string) error {
	files, err := image.Dump(dir)
	if err != nil {
		return err //nolint:wrapcheck // names the file
	}

	for _, f := range files {
		if _, err := fmt.Fprintln(out, f); err != nil {
			return err //nolint:wrapcheck // a failed write to stdout needs no context
		}
	}

	return nil
}
