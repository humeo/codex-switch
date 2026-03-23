package profile

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestSaveAndLoadProfile(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "profiles"))
	raw := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"abc"}}`)

	if err := store.Save("work", raw); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	got, err := store.Load("work")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if string(got) != string(raw) {
		t.Fatalf("Load() mismatch = %s", got)
	}
}

func TestListAndRemoveProfiles(t *testing.T) {
	store := NewStore(filepath.Join(t.TempDir(), "profiles"))

	if err := store.Save("b", []byte("b")); err != nil {
		t.Fatalf("Save(b) error = %v", err)
	}
	if err := store.Save("a", []byte("a")); err != nil {
		t.Fatalf("Save(a) error = %v", err)
	}

	got, err := store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("List() = %v, want [a b]", got)
	}

	if err := store.Remove("a"); err != nil {
		t.Fatalf("Remove(a) error = %v", err)
	}

	got, err = store.List()
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !reflect.DeepEqual(got, []string{"b"}) {
		t.Fatalf("List() after remove = %v, want [b]", got)
	}
}
