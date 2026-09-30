package image

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// CustomRepo is the repository of the images built from a --dockerfile.
const CustomRepo = "sandboxed_agent-custom"

// CustomTag is the tag of the image built from a Dockerfile with this
// content: a changed Dockerfile is another image.
func CustomTag(dockerfile []byte) string {
	sum := sha256.Sum256(dockerfile)

	return CustomRepo + ":" + hex.EncodeToString(sum[:8])
}

// FileBuildArgv builds the Dockerfile at file, with its dir as the context.
func FileBuildArgv(o Options, file string) []string {
	argv := append([]string{docker, verbBuild, "-t", o.tag(), "-f", file}, o.userArgs()...)

	return append(argv, filepath.Dir(file))
}

// EnsureFile builds the image of the Dockerfile at file unless it is built
// already, and returns its tag. o.Tag is ignored: the content names it.
func (b Builder) EnsureFile(ctx context.Context, o Options, file string) (string, error) {
	body, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("image: %w", err)
	}

	o.Tag = CustomTag(body)
	if b.Exists(ctx, o.Tag) == nil {
		return o.Tag, nil
	}

	if err := b.run(ctx, FileBuildArgv(o, file), nil, b.Log); err != nil {
		return "", err
	}

	return o.Tag, nil
}

// Dump writes the build context (the Dockerfile and its scripts) into dir,
// creating it. It never overwrites: when any file exists, nothing is
// written. It returns the files written.
func Dump(dir string) ([]string, error) {
	files, err := dumpTargets(dir)
	if err != nil {
		return nil, err
	}

	if err := refuseExisting(files); err != nil {
		return nil, err
	}

	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("image dump: %w", err)
	}

	for _, f := range files {
		if err := dumpFile(f); err != nil {
			return nil, err
		}
	}

	return files, nil
}

// dumpTargets are the paths the embedded assets are dumped to.
func dumpTargets(dir string) ([]string, error) {
	entries, err := fs.ReadDir(assets, "assets")
	if err != nil {
		return nil, fmt.Errorf("image dump: %w", err)
	}

	files := make([]string, 0, len(entries))
	for _, e := range entries {
		files = append(files, filepath.Join(dir, e.Name()))
	}

	return files, nil
}

func refuseExisting(files []string) error {
	for _, f := range files {
		_, err := os.Lstat(f)
		if err == nil {
			return fmt.Errorf("image dump: %s: %w", f, fs.ErrExist)
		}

		if !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("image dump: %w", err)
		}
	}

	return nil
}

func dumpFile(dst string) error {
	name := filepath.Base(dst)

	body, err := assets.ReadFile("assets/" + name)
	if err != nil {
		return fmt.Errorf("image dump: %w", err)
	}

	f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, assetMode(name))
	if err != nil {
		return fmt.Errorf("image dump: %w", err)
	}

	_, err = f.Write(body)
	if err == nil {
		err = f.Chmod(assetMode(name)) // past the umask
	}

	if cerr := f.Close(); err == nil {
		err = cerr
	}

	if err != nil {
		return fmt.Errorf("image dump: %w", err)
	}

	return nil
}
