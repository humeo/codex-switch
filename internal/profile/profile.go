package profile

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

var ErrInvalidName = errors.New("invalid profile name")

type Store struct {
	dir string
}

func NewStore(dir string) Store {
	return Store{dir: dir}
}

func (s Store) Save(name string, raw []byte) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}

	return os.WriteFile(path, raw, 0o600)
}

func (s Store) Load(name string) ([]byte, error) {
	path, err := s.path(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func (s Store) List() ([]string, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}

	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".json" {
			continue
		}
		names = append(names, strings.TrimSuffix(name, ".json"))
	}
	sort.Strings(names)
	return names, nil
}

func (s Store) Remove(name string) error {
	path, err := s.path(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

func (s Store) path(name string) (string, error) {
	if err := validateName(name); err != nil {
		return "", err
	}
	return filepath.Join(s.dir, name+".json"), nil
}

func validateName(name string) error {
	switch {
	case name == "", name == ".", name == "..":
		return ErrInvalidName
	case strings.Contains(name, "/"), strings.Contains(name, `\`):
		return ErrInvalidName
	}
	return nil
}
