package main

import (
	"github.com/spf13/cobra"
)

func newRunCmd(d deps) *cobra.Command {
	f := newRunFlags()

	cmd := &cobra.Command{
		Use:   "run HARNESS [flags] [-- harness args...]",
		Short: "Run the harness once",
		Long: "Run the harness once, headless, with a fresh context. The final message goes to\n" +
			"stdout (--json: one JSON object instead), progress and the harness's stderr to\n" +
			"stderr. Exit codes: 0 ok, 1 the tool itself failed, 2 the harness failed,\n" +
			"3 --timeout, 4 rate or usage limit, 5 overloaded, 130 interrupted.",
		RunE: func(cmd *cobra.Command, args []string) error {
			s, err := f.resolve(cmd, args, d)
			if err != nil {
				return err
			}

			return d.actions.run(cmd.Context(), s)
		},
	}
	f.bind(cmd.Flags())

	return cmd
}
