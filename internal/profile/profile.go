package profile

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Store struct {
	dir string
}

func NewStore(dir string) Store {
	return Store{dir: dir}
}

func (s Store) Save(name string, raw []byte) error {
	if err := os.MkdirAll(s.dir, 0o755); err != nil {
		return err
	}

	return os.WriteFile(s.path(name), raw, 0o600)
}

func (s Store) Load(name string) ([]byte, error) {
	return os.ReadFile(s.path(name))
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
	return os.Remove(s.path(name))
}

func (s Store) path(name string) string {
	return filepath.Join(s.dir, name+".json")
}
