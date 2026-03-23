package cli

import (
	"io"
	"os"
	"path/filepath"

	"codex-switch/internal/watcher"
)

type Dependencies struct {
	ConfigPath         string
	ProfilesDir        string
	AuthPath           string
	CodexSessionsDir   string
	WatchStatePath     string
	WatchChecksPath    string
	WatchLogPath       string
	WatchTriggerEvents <-chan watcher.TokenCountEvent
	Stdout             io.Writer
	Stderr             io.Writer
}

func DefaultDependencies() (Dependencies, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return Dependencies{}, err
	}

	base := filepath.Join(home, ".codex-switch")
	return Dependencies{
		ConfigPath:       filepath.Join(base, "config.toml"),
		ProfilesDir:      filepath.Join(base, "profiles"),
		AuthPath:         filepath.Join(home, ".codex", "auth.json"),
		CodexSessionsDir: filepath.Join(home, ".codex", "sessions"),
		WatchStatePath:   filepath.Join(base, "watch-state.toml"),
		WatchChecksPath:  filepath.Join(base, "watch-checks.jsonl"),
		WatchLogPath:     filepath.Join(base, "watch.log"),
		Stdout:           os.Stdout,
		Stderr:           os.Stderr,
	}, nil
}
