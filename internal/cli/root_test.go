package cli

import "testing"

func TestRootCommandIncludesCoreSubcommands(t *testing.T) {
	cmd := NewRootCommand(Dependencies{})
	names := map[string]bool{}
	for _, child := range cmd.Commands() {
		names[child.Name()] = true
	}

	for _, want := range []string{"auth", "list", "use", "status", "watch", "remove"} {
		if !names[want] {
			t.Fatalf("missing subcommand %q", want)
		}
	}
}
