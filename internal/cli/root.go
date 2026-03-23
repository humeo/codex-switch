package cli

import "github.com/spf13/cobra"

func NewRootCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codex-switch",
		Short: "Manage multiple Codex OAuth profiles",
	}
	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}
	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}

	cmd.AddCommand(
		newAuthCommand(deps),
		newListCommand(deps),
		newUseCommand(deps),
		newStatusCommand(deps),
		newWatchCommand(deps),
		newRemoveCommand(deps),
	)

	return cmd
}

func newListCommand(Dependencies) *cobra.Command {
	return &cobra.Command{Use: "list"}
}

func newStatusCommand(Dependencies) *cobra.Command {
	return &cobra.Command{Use: "status"}
}

func newWatchCommand(Dependencies) *cobra.Command {
	return &cobra.Command{Use: "watch"}
}
