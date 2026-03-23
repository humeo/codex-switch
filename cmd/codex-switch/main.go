package main

import (
	"os"

	"codex-switch/internal/cli"
)

func main() {
	deps, err := cli.DefaultDependencies()
	if err != nil {
		os.Exit(1)
	}
	if err := cli.NewRootCommand(deps).Execute(); err != nil {
		os.Exit(1)
	}
}
