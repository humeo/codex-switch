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
	if err := writeTarGz(archivePath, "codex-switch", []byte("#!/bin/sh\necho installed\n"), 0o755); err != nil {
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
		if !strings.Contains(string(out), "Removed user data") {
			t.Fatalf("stdout = %q, want removed-data notice", out)
		}
	})
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
