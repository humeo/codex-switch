package switcher

import (
	"os"
	"path/filepath"
	"strings"
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

func TestWriteAuthAtomicallyCleansUpTempFileOnRenameFailure(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "auth-dir")
	if err := os.Mkdir(dst, 0o755); err != nil {
		t.Fatal(err)
	}

	err := WriteAuthAtomically(dst, []byte("new"))
	if err == nil {
		t.Fatal("WriteAuthAtomically() error = nil, want rename failure")
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "auth-") && strings.HasSuffix(entry.Name(), ".tmp") {
			t.Fatalf("temporary file %q was not cleaned up", entry.Name())
		}
	}
}
