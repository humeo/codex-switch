package main

import (
	"os"

	"codex-switch/internal/cli"
)

func main() {
	if err := cli.NewRootCommand(cli.Dependencies{}).Execute(); err != nil {
		os.Exit(1)
	}
}
