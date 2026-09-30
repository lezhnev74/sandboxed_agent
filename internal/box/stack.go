package box

import (
	"context"
	"strconv"
)

// statusRunning is docker's state of a running container.
const statusRunning = "running"

// OwnArgv hands every file under dir not owned by uid:gid back to them
// (inner containers write as root). Only those files are touched, so the
// rest keep their ctime.
func OwnArgv(dir string, uid, gid int) []string {
	u, g := strconv.Itoa(uid), strconv.Itoa(gid)

	return []string{
		"find", dir, "(", "!", "-user", u, "-o", "!", "-group", g, ")",
		"-exec", "chown", "-h", u + ":" + g, "{}", "+",
	}
}

// Own runs OwnArgv as root in the box.
func (m Manager) Own(ctx context.Context, name, dir string, uid, gid int) error {
	return m.Exec(ctx, name, ExecOpts{User: "0", Stderr: m.Log}, OwnArgv(dir, uid, gid))
}
