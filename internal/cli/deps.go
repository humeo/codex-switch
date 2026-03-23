package cli

import (
	"io"
	"os"
	"path/filepath"
)

type Dependencies struct {
	ConfigPath  string
	ProfilesDir string
	AuthPath    string
	Stdin       *os.File
	Stdout      io.Writer
	Stderr      io.Writer
}

func DefaultDependencies() (Dependencies, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dependencies{}, err
	}

	base := filepath.Join(home, ".codex-switch")
	return Dependencies{
		ConfigPath:  filepath.Join(base, "config.toml"),
		ProfilesDir: filepath.Join(base, "profiles"),
		AuthPath:    filepath.Join(home, ".codex", "auth.json"),
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
	}, nil
}
