package switcher

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteAuthAtomicallyReplacesDestination(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	if err := os.WriteFile(path, []byte(`old`), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := WriteAuthAtomically(path, []byte(`new`)); err != nil {
		t.Fatalf("WriteAuthAtomically() error = %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != "new" {
		t.Fatalf("auth contents = %q, want new", got)
	}
}
