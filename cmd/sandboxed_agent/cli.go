package main

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func execCLI(ctx context.Context, d deps, args []string) error {
	root := newRoot(d)
	root.SetArgs(args)

	return root.ExecuteContext(ctx) //nolint:wrapcheck // the CLI's own errors, printed as they are
}

func newRoot(d deps) *cobra.Command {
	var rebuild bool

	root := &cobra.Command{
		Use:   "sandboxed_agent",
		Short: "Run a coding-agent harness once, headless, in a per-directory Docker box",
		Long: "sandboxed_agent runs a coding-agent harness (claude, opencode) once, headless, in a Docker\n" +
			"box scoped to the current directory. Loops live in the calling script.",
		SilenceUsage:  true,
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if rebuild {
				return d.actions.rebuild(cmd.Context())
			}

			return cmd.Help()
		},
	}
	root.SetIn(d.stdin)
	root.SetOut(d.stdout)
	root.SetErr(d.stderr)
	root.CompletionOptions.DisableDefaultCmd = true
	root.Flags().BoolVar(&rebuild, "rebuild", false, "rebuild the image with the latest harness versions")

	root.AddCommand(newRunCmd(d))
	root.AddCommand(boxCommands(d)...)

	return root
}

func boxCommands(d deps) []*cobra.Command {
	return []*cobra.Command{
		shellCmd(d), downCmd(d), loginCmd(d), gcCmd(d), dockerfileCmd(d),
		tuiCmd(d, harnessClaude), tuiCmd(d, harnessOpenCode),
	}
}

func loginCmd(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "login [claude|opencode]",
		Short: "First-time auth for a harness (default claude)",
		Long: "Log a harness in once; every later run reuses the login.\n" +
			"claude: runs `claude auth login` in this repo's box: open the URL it prints,\n" +
			"sign in and paste the code back. The credential is stored in\n" +
			"~/.local/share/sandboxed_agent/claude-auth/ and copied into each run.\n" +
			"opencode: runs `opencode auth login` in this repo's box. Its auth.json lives in\n" +
			"~/.local/share/sandboxed_agent/opencode/, mounted into every box. Provider API keys can\n" +
			"instead go in ~/.config/sandboxed_agent/env (K=V lines), passed to the box as --env-file.",
		Args:      cobra.MaximumNArgs(1),
		ValidArgs: []string{"claude", "opencode"},
		RunE: func(cmd *cobra.Command, args []string) error {
			h := harnessClaude
			if len(args) == 1 {
				h = args[0]
			}

			return d.actions.login(cmd.Context(), h)
		},
	}
}

func shellCmd(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "shell",
		Short: "Open bash in this repo's box (created with defaults if absent)",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return d.actions.shell(cmd.Context()) },
	}
}

func downCmd(d deps) *cobra.Command {
	var all bool

	c := &cobra.Command{
		Use:   "down",
		Short: "Remove this repo's box and its sidecar",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, _ []string) error { return d.actions.down(cmd.Context(), all) },
	}
	c.Flags().BoolVar(&all, "all", false, "remove every sandboxed_agent box")

	return c
}

func gcCmd(d deps) *cobra.Command {
	var older time.Duration

	c := &cobra.Command{
		Use:   "gc",
		Short: "Prune cfg dirs of crashed runs and dind volumes of removed boxes",
		Long: "Prune, in every repo not running a run now, the run cfg dirs older than --older\n" +
			"(a run removes its own when it ends; crashed runs leave theirs). Then remove the\n" +
			"dind volumes (sandboxed_agent-dind-*) of boxes that no longer exist.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error { return d.actions.gc(cmd.Context(), older) },
	}
	c.Flags().DurationVar(&older, "older", 0, "prune only what is older than `D`, e.g. 720h (0: everything prunable)")

	return c
}

func tuiCmd(d deps, harnessName string) *cobra.Command {
	var model string

	c := &cobra.Command{
		Use:   harnessName + " [-- harness args...]",
		Short: fmt.Sprintf("Interactive %s TUI in this repo's box", harnessName),
		Long: fmt.Sprintf("Start the interactive %s TUI in this repo's box with a fresh bare config\n"+
			"(as runs get, all permissions granted), removed when it exits.", harnessName),
		RunE: func(cmd *cobra.Command, args []string) error {
			before, after := splitDash(cmd, args)
			if len(before) > 0 {
				return fmt.Errorf("%w: harness args go after --", errUsage)
			}

			return d.actions.tui(cmd.Context(), harnessName, model, after)
		},
	}
	c.Flags().StringVarP(&model, "model", "m", "", "`MODEL` id, passed to the harness verbatim")

	return c
}

func dockerfileCmd(d deps) *cobra.Command {
	return &cobra.Command{
		Use:   "dockerfile DIR",
		Short: "Write the image's Dockerfile and helper scripts into DIR, to extend it",
		Long: "Write the Dockerfile the default image is built from, and the scripts it\n" +
			"copies, into DIR (created; existing files are never overwritten). Edit the\n" +
			"Dockerfile, then `run --dockerfile DIR/Dockerfile`: the image is built once, with\n" +
			"DIR as the build context, and rebuilt only when the Dockerfile changes.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return d.actions.dump(cmd.Context(), absPath(d.cwd, args[0]))
		},
	}
}
