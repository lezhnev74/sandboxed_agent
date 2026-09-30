package harness

import "strings"

// VersionArgv prints the harness's version; nil for an adapter without a
// command.
func (a Adapter) VersionArgv() []string {
	if len(a.Command) == 0 {
		return nil
	}

	return []string{a.Command[0], "--version"}
}

// ParseVersion keeps the first word of `--version` output: "2.1.284" of
// "2.1.284 (Claude Code)".
func ParseVersion(out string) string {
	f := strings.Fields(out)
	if len(f) == 0 {
		return ""
	}

	return f[0]
}
