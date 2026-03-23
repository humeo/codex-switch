package scripts_test

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInstallScriptInstallsBinaryFromReleaseArchive(t *testing.T) {
	root := repoRoot(t)
	tmp := t.TempDir()
	assetsDir := filepath.Join(tmp, "assets")
	installDir := filepath.Join(tmp, "bin")
	homeDir := filepath.Join(tmp, "home")

	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(assetsDir) error = %v", err)
	}
	if err := os.MkdirAll(homeDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(homeDir) error = %v", err)
	}

	archivePath := filepath.Join(assetsDir, releaseArchiveName())
	if err := writeTarGz(archivePath, "codex-switch", []byte(`#!/bin/sh
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
echo installed
`), 0o755); err != nil {
		t.Fatalf("writeTarGz() error = %v", err)
	}

	cmd := exec.Command("bash", filepath.Join(root, "scripts", "install.sh"))
	cmd.Env = append(os.Environ(),
		"HOME="+homeDir,
		"INSTALL_DIR="+installDir,
		"VERSION=v1.2.3",
		"RELEASE_BASE_URL=file://"+assetsDir,
		"REPO=example/codex-switch",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("install.sh error = %v\n%s", err, out)
	}

	installedPath := filepath.Join(installDir, "codex-switch")
	info, err := os.Stat(installedPath)
	if err != nil {
		t.Fatalf("Stat(installedPath) error = %v", err)
	}
	if info.Mode().Perm() != 0o755 {
		t.Fatalf("installed mode = %o, want 755", info.Mode().Perm())
	}
	if !strings.Contains(string(out), "Installed codex-switch to") {
		t.Fatalf("stdout = %q, want install confirmation", out)
	}
	if !strings.Contains(string(out), "Installed zsh completion to") {
		t.Fatalf("stdout = %q, want zsh completion confirmation", out)
	}
	if !strings.Contains(string(out), "Installed bash completion to") {
		t.Fatalf("stdout = %q, want bash completion confirmation", out)
	}
	if !strings.Contains(string(out), "Installed fish completion to") {
		t.Fatalf("stdout = %q, want fish completion confirmation", out)
	}

	assertFileContains(t, filepath.Join(homeDir, ".zsh", "completions", "_codex-switch"), "#compdef codex-switch")
	assertFileContains(t, filepath.Join(homeDir, ".local", "share", "bash-completion", "completions", "codex-switch"), "# bash completion for codex-switch")
	assertFileContains(t, filepath.Join(homeDir, ".config", "fish", "completions", "codex-switch.fish"), "# fish completion for codex-switch")
}

func TestUninstallScriptRemovesBinaryAndOptionallyData(t *testing.T) {
	root := repoRoot(t)

	t.Run("preserves data by default", func(t *testing.T) {
		tmp := t.TempDir()
		installDir := filepath.Join(tmp, "bin")
		homeDir := filepath.Join(tmp, "home")
		if err := os.MkdirAll(installDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(installDir) error = %v", err)
		}
		dataDir := filepath.Join(homeDir, ".codex-switch")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(dataDir) error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(installDir, "codex-switch"), []byte("bin"), 0o755); err != nil {
			t.Fatalf("WriteFile(binary) error = %v", err)
		}
		writeCompletionFixtures(t, homeDir)

		cmd := exec.Command("bash", filepath.Join(root, "scripts", "uninstall.sh"))
		cmd.Env = append(os.Environ(),
			"HOME="+homeDir,
			"INSTALL_DIR="+installDir,
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("uninstall.sh error = %v\n%s", err, out)
		}

		if _, err := os.Stat(filepath.Join(installDir, "codex-switch")); !os.IsNotExist(err) {
			t.Fatalf("binary still exists, stat err = %v", err)
		}
		assertNotExists(t, filepath.Join(homeDir, ".zsh", "completions", "_codex-switch"))
		assertNotExists(t, filepath.Join(homeDir, ".local", "share", "bash-completion", "completions", "codex-switch"))
		assertNotExists(t, filepath.Join(homeDir, ".config", "fish", "completions", "codex-switch.fish"))
		if _, err := os.Stat(dataDir); err != nil {
			t.Fatalf("data dir stat error = %v, want preserved", err)
		}
		if !strings.Contains(string(out), "Preserved user data") {
			t.Fatalf("stdout = %q, want preserved-data notice", out)
		}
	})

	t.Run("removes data when requested", func(t *testing.T) {
		tmp := t.TempDir()
		installDir := filepath.Join(tmp, "bin")
		homeDir := filepath.Join(tmp, "home")
		if err := os.MkdirAll(installDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(installDir) error = %v", err)
		}
		dataDir := filepath.Join(homeDir, ".codex-switch")
		if err := os.MkdirAll(dataDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(dataDir) error = %v", err)
		}
		if err := os.WriteFile(filepath.Join(installDir, "codex-switch"), []byte("bin"), 0o755); err != nil {
			t.Fatalf("WriteFile(binary) error = %v", err)
		}
		writeCompletionFixtures(t, homeDir)

		cmd := exec.Command("bash", filepath.Join(root, "scripts", "uninstall.sh"))
		cmd.Env = append(os.Environ(),
			"HOME="+homeDir,
			"INSTALL_DIR="+installDir,
			"REMOVE_DATA=1",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("uninstall.sh error = %v\n%s", err, out)
		}

		if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
			t.Fatalf("data dir still exists, stat err = %v", err)
		}
		assertNotExists(t, filepath.Join(homeDir, ".zsh", "completions", "_codex-switch"))
		assertNotExists(t, filepath.Join(homeDir, ".local", "share", "bash-completion", "completions", "codex-switch"))
		assertNotExists(t, filepath.Join(homeDir, ".config", "fish", "completions", "codex-switch.fish"))
		if !strings.Contains(string(out), "Removed user data") {
			t.Fatalf("stdout = %q, want removed-data notice", out)
		}
	})
}

func writeCompletionFixtures(t *testing.T, homeDir string) {
	t.Helper()

	files := map[string]string{
		filepath.Join(homeDir, ".zsh", "completions", "_codex-switch"):                                    "#compdef codex-switch\n",
		filepath.Join(homeDir, ".local", "share", "bash-completion", "completions", "codex-switch"):      "# bash completion for codex-switch\n",
		filepath.Join(homeDir, ".config", "fish", "completions", "codex-switch.fish"):                    "# fish completion for codex-switch\n",
	}
	for path, content := range files {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) error = %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("WriteFile(%q) error = %v", path, err)
		}
	}
}

func assertFileContains(t *testing.T, path, want string) {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", path, err)
	}
	if !strings.Contains(string(data), want) {
		t.Fatalf("file %q = %q, want to contain %q", path, data, want)
	}
}

func assertNotExists(t *testing.T, path string) {
	t.Helper()

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%q still exists, stat err = %v", path, err)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	return filepath.Dir(wd)
}

func releaseArchiveName() string {
	return "codex-switch_" + releaseOS() + "_" + releaseArch() + ".tar.gz"
}

func releaseOS() string {
	switch runtime.GOOS {
	case "darwin", "linux":
		return runtime.GOOS
	default:
		return runtime.GOOS
	}
}

func releaseArch() string {
	switch runtime.GOARCH {
	case "amd64":
		return "amd64"
	case "arm64":
		return "arm64"
	default:
		return runtime.GOARCH
	}
}

func writeTarGz(path, name string, data []byte, mode int64) error {
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
	_, err = tw.Write(data)
	return err
}
