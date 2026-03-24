package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionCommandPrintsVersion(t *testing.T) {
	orig := Version
	Version = "v1.2.3"
	t.Cleanup(func() {
		Version = orig
	})

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		Stdout: stdout,
		Stderr: stderr,
	})
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := stdout.String(); got != "codex-switch v1.2.3\n" {
		t.Fatalf("stdout = %q, want %q", got, "codex-switch v1.2.3\n")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestVersionCommandDefaultsToDev(t *testing.T) {
	orig := Version
	Version = ""
	t.Cleanup(func() {
		Version = orig
	})

	stdout := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		Stdout: stdout,
	})
	cmd.SetArgs([]string{"version"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if got := strings.TrimSpace(stdout.String()); got != "codex-switch dev" {
		t.Fatalf("stdout = %q, want %q", got, "codex-switch dev")
	}
}
