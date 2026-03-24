package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestUpdateAssetName(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		goos    string
		goarch  string
		want    string
		wantErr string
	}{
		{name: "darwin arm64", goos: "darwin", goarch: "arm64", want: "codex-switch_darwin_arm64.tar.gz"},
		{name: "darwin amd64", goos: "darwin", goarch: "amd64", want: "codex-switch_darwin_amd64.tar.gz"},
		{name: "linux arm64", goos: "linux", goarch: "arm64", want: "codex-switch_linux_arm64.tar.gz"},
		{name: "linux amd64", goos: "linux", goarch: "amd64", want: "codex-switch_linux_amd64.tar.gz"},
		{name: "unsupported os", goos: "windows", goarch: "amd64", wantErr: "unsupported operating system"},
		{name: "unsupported arch", goos: "linux", goarch: "386", wantErr: "unsupported architecture"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := updateAssetName(tt.goos, tt.goarch)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("updateAssetName(%q, %q) error = %v, want substring %q", tt.goos, tt.goarch, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("updateAssetName(%q, %q) error = %v", tt.goos, tt.goarch, err)
			}
			if got != tt.want {
				t.Fatalf("updateAssetName(%q, %q) = %q, want %q", tt.goos, tt.goarch, got, tt.want)
			}
		})
	}
}

func TestUpdateCommandReplacesBinaryAndRefreshesCompletions(t *testing.T) {
	tmp := t.TempDir()
	installDir := filepath.Join(tmp, "bin")
	homeDir := filepath.Join(tmp, "home")
	installedPath := filepath.Join(installDir, "codex-switch")
	if err := os.MkdirAll(installDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(installDir) error = %v", err)
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(homeDir) error = %v", err)
	}
	if err := os.WriteFile(installedPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("WriteFile(installedPath) error = %v", err)
	}

	assetName := "codex-switch_" + updateTestOS() + "_" + updateTestArch() + ".tar.gz"
	archivePath := filepath.Join(tmp, assetName)
	if err := writeUpdateTarGz(archivePath, "codex-switch", []byte(`#!/bin/sh
if [ "$1" = "completion" ]; then
  case "$2" in
    zsh)
      printf '%s\n' '#compdef codex-switch'
      ;;
    bash)
      printf '%s\n' '# bash completion for codex-switch'
      ;;
    fish)
      printf '%s\n' '# fish completion for codex-switch'
      ;;
    *)
      exit 1
      ;;
  esac
  exit 0
fi
echo updated
`), 0o755); err != nil {
		t.Fatalf("writeUpdateTarGz() error = %v", err)
	}

	t.Setenv("CODEX_SWITCH_UPDATE_BASE_URL", "file://"+tmp)
	t.Setenv("CODEX_SWITCH_UPDATE_EXECUTABLE_PATH", installedPath)
	t.Setenv("CODEX_SWITCH_UPDATE_HOME_DIR", homeDir)

	stdout := &bytes.Buffer{}
	stderr := &bytes.Buffer{}
	cmd := NewRootCommand(Dependencies{
		Stdout: stdout,
		Stderr: stderr,
	})
	cmd.SetArgs([]string{"update"})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v\nstdout=%s\nstderr=%s", err, stdout.String(), stderr.String())
	}
	if got, err := os.ReadFile(installedPath); err != nil {
		t.Fatalf("ReadFile(installedPath) error = %v", err)
	} else if !strings.Contains(string(got), "updated") {
		t.Fatalf("installed binary = %q, want updated content", got)
	}

	assertUpdateFileContains(t, filepath.Join(homeDir, ".zsh", "completions", "_codex-switch"), "#compdef codex-switch")
	assertUpdateFileContains(t, filepath.Join(homeDir, ".local", "share", "bash-completion", "completions", "codex-switch"), "# bash completion for codex-switch")
	assertUpdateFileContains(t, filepath.Join(homeDir, ".config", "fish", "completions", "codex-switch.fish"), "# fish completion for codex-switch")
	if !strings.Contains(stdout.String(), "updated codex-switch successfully") {
		t.Fatalf("stdout = %q, want success output", stdout.String())
	}
}

func updateTestOS() string {
	switch runtime.GOOS {
	case "darwin", "linux":
		return runtime.GOOS
	default:
		return runtime.GOOS
	}
}

func updateTestArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func writeUpdateTarGz(path, name string, data []byte, mode int64) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	gzw := gzip.NewWriter(f)
	defer gzw.Close()

	tw := tar.NewWriter(gzw)
	defer tw.Close()

	if err := tw.WriteHeader(&tar.Header{
		Name: name,
		Mode: mode,
		Size: int64(len(data)),
	}); err != nil {
		return err
	}
	if _, err := tw.Write(data); err != nil {
		return err
	}
	return nil
}

func assertUpdateFileContains(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("file %q = %q, want to contain %q", path, data, want)
	}
}
