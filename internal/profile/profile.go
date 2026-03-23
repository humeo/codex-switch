package profile

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var ErrInvalidName = errors.New("invalid profile name")

type Tokens struct {
	AccessToken  string `json:"access_token,omitempty"`
	IDToken      string `json:"id_token,omitempty"`
	RefreshToken string `json:"refresh_token,omitempty"`
	AccountID    string `json:"account_id,omitempty"`
}

type Profile struct {
	AuthMode     string    `json:"auth_mode,omitempty"`
	OpenAIAPIKey any       `json:"OPENAI_API_KEY,omitempty"`
	Tokens       Tokens    `json:"tokens"`
	LastRefresh  time.Time `json:"last_refresh,omitempty"`
}

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

func UpdateTokens(raw []byte, tokens Tokens, refreshedAt time.Time) ([]byte, error) {
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(raw, &stored); err != nil {
		return nil, err
	}

	merged := Tokens{}
	if tokenRaw, ok := stored["tokens"]; ok && len(tokenRaw) > 0 && string(tokenRaw) != "null" {
		if err := json.Unmarshal(tokenRaw, &merged); err != nil {
			return nil, err
		}
	}

	merged.AccessToken = tokens.AccessToken
	merged.IDToken = tokens.IDToken
	merged.RefreshToken = tokens.RefreshToken
	if tokens.AccountID != "" {
		merged.AccountID = tokens.AccountID
	}

	updatedTokens, err := json.Marshal(merged)
	if err != nil {
		return nil, err
	}
	stored["tokens"] = updatedTokens

	updatedRefresh, err := json.Marshal(refreshedAt.UTC())
	if err != nil {
		return nil, err
	}
	stored["last_refresh"] = updatedRefresh

	return json.Marshal(stored)
}
