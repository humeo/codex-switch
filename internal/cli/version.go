package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// Version is overridden in release builds via -ldflags.
var Version = "dev"

func newVersionCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print codex-switch version",
		Args:  cobra.NoArgs,
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Fprintf(cmd.OutOrStdout(), "codex-switch %s\n", currentVersion())
		},
	}
	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}
	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}
	return cmd
}

func currentVersion() string {
	if strings.TrimSpace(Version) == "" {
		return "dev"
	}
	return strings.TrimSpace(Version)
}
