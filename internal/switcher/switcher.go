package switcher

import (
	"fmt"
	"os"
	"path/filepath"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
)

func WriteAuthAtomically(path string, raw []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "auth-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		cleanup()
		return err
	}
	return nil
}

func SwitchProfile(cfgPath, authPath string, store profile.Store, name string) (config.Config, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return config.Config{}, err
	}

	raw, err := store.Load(name)
	if err != nil {
		return config.Config{}, err
	}

	if err := WriteAuthAtomically(authPath, raw); err != nil {
		return config.Config{}, err
	}

	cfg.ActiveProfile = name
	if err := config.Save(cfgPath, cfg); err != nil {
		return config.Config{}, err
	}

	return cfg, nil
}

func RemoveProfile(cfgPath string, store profile.Store, name string, force bool) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	if cfg.ActiveProfile == name && !force {
		return fmt.Errorf("profile %q is active; use --force to remove it", name)
	}

	if err := store.Remove(name); err != nil {
		return err
	}

	if cfg.ActiveProfile == name {
		cfg.ActiveProfile = ""
		if err := config.Save(cfgPath, cfg); err != nil {
			return err
		}
	}

	return nil
}
