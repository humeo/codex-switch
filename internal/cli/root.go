package cli

import "github.com/spf13/cobra"

func NewRootCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "codex-switch",
		Short: "Manage multiple Codex OAuth profiles",
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

func newAuthCommand(Dependencies) *cobra.Command {
	return &cobra.Command{Use: "auth"}
}

func newListCommand(Dependencies) *cobra.Command {
	return &cobra.Command{Use: "list"}
}

func newUseCommand(Dependencies) *cobra.Command {
	return &cobra.Command{Use: "use"}
}

func newStatusCommand(Dependencies) *cobra.Command {
	return &cobra.Command{Use: "status"}
}

func newWatchCommand(Dependencies) *cobra.Command {
	return &cobra.Command{Use: "watch"}
}

func newRemoveCommand(Dependencies) *cobra.Command {
	return &cobra.Command{Use: "remove"}
}
