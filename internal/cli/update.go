package cli

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const (
	defaultUpdateBaseURL    = "https://github.com/humeo/codex-switch/releases/latest/download"
	updateBaseURLEnv        = "CODEX_SWITCH_UPDATE_BASE_URL"
	updateExecutablePathEnv = "CODEX_SWITCH_UPDATE_EXECUTABLE_PATH"
	updateHomeDirEnv        = "CODEX_SWITCH_UPDATE_HOME_DIR"
	updateBinaryName        = "codex-switch"
)

func newUpdateCommand(deps Dependencies) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update codex-switch to the latest release",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runUpdate(cmd.Context(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}
	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}
	return cmd
}

func runUpdate(ctx context.Context, out, errOut io.Writer) error {
	exePath, err := currentExecutablePath()
	if err != nil {
		return err
	}
	homeDir, err := currentHomeDir()
	if err != nil {
		return err
	}
	assetName, err := updateAssetName(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "checking latest release for humeo/codex-switch")
	fmt.Fprintf(out, "downloading %s\n", assetName)

	tmpDir, err := os.MkdirTemp("", "codex-switch-update-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	extractedBinary := filepath.Join(tmpDir, updateBinaryName)
	if err := downloadAndExtractUpdate(ctx, updateBaseURL(), assetName, extractedBinary); err != nil {
		return err
	}
	if err := replaceExecutable(exePath, extractedBinary); err != nil {
		return err
	}
	fmt.Fprintf(out, "replaced %s\n", exePath)

	refreshCompletions(out, errOut, exePath, homeDir)
	fmt.Fprintln(out, "updated codex-switch successfully")
	return nil
}

func currentExecutablePath() (string, error) {
	if value := os.Getenv(updateExecutablePathEnv); value != "" {
		return value, nil
	}
	return os.Executable()
}

func currentHomeDir() (string, error) {
	if value := os.Getenv(updateHomeDirEnv); value != "" {
		return value, nil
	}
	return os.UserHomeDir()
}

func updateBaseURL() string {
	if value := os.Getenv(updateBaseURLEnv); value != "" {
		return value
	}
	return defaultUpdateBaseURL
}

func updateAssetName(goos, goarch string) (string, error) {
	switch goos {
	case "darwin", "linux":
	default:
		return "", fmt.Errorf("unsupported operating system: %s", goos)
	}

	switch goarch {
	case "amd64", "arm64":
	default:
		return "", fmt.Errorf("unsupported architecture: %s", goarch)
	}

	return fmt.Sprintf("%s_%s_%s.tar.gz", updateBinaryName, goos, goarch), nil
}

func downloadAndExtractUpdate(ctx context.Context, baseURL, assetName, outputPath string) error {
	reader, err := openUpdateAsset(ctx, baseURL, assetName)
	if err != nil {
		return err
	}
	defer reader.Close()

	gzr, err := gzip.NewReader(reader)
	if err != nil {
		return err
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != updateBinaryName {
			continue
		}
		out, err := os.OpenFile(outputPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			_ = out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return nil
	}

	return fmt.Errorf("archive did not contain %s", updateBinaryName)
}

func openUpdateAsset(ctx context.Context, baseURL, assetName string) (io.ReadCloser, error) {
	if strings.HasPrefix(baseURL, "file://") {
		path := filepath.Join(strings.TrimPrefix(baseURL, "file://"), assetName)
		return os.Open(path)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/"+assetName, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		defer resp.Body.Close()
		return nil, fmt.Errorf("download failed with status %d", resp.StatusCode)
	}
	return resp.Body, nil
}

func replaceExecutable(targetPath, sourcePath string) error {
	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer source.Close()

	dir := filepath.Dir(targetPath)
	tmp, err := os.CreateTemp(dir, "codex-switch-update-*")
	if err != nil {
		return fmt.Errorf("current executable is not writable: %s", targetPath)
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}

	if _, err := io.Copy(tmp, source); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, targetPath); err != nil {
		cleanup()
		return err
	}
	return nil
}

func refreshCompletions(out, errOut io.Writer, binaryPath, homeDir string) {
	targets := []struct {
		shell string
		path  string
	}{
		{shell: "zsh", path: filepath.Join(homeDir, ".zsh", "completions", "_"+updateBinaryName)},
		{shell: "bash", path: filepath.Join(homeDir, ".local", "share", "bash-completion", "completions", updateBinaryName)},
		{shell: "fish", path: filepath.Join(homeDir, ".config", "fish", "completions", updateBinaryName+".fish")},
	}

	for _, target := range targets {
		if err := refreshCompletion(binaryPath, target.shell, target.path); err != nil {
			fmt.Fprintf(errOut, "warning: failed to refresh %s completion: %v\n", target.shell, err)
			continue
		}
		fmt.Fprintf(out, "refreshed %s completion\n", target.shell)
	}
}

func refreshCompletion(binaryPath, shell, outputPath string) error {
	if err := os.MkdirAll(filepath.Dir(outputPath), 0o755); err != nil {
		return err
	}
	cmd := exec.Command(binaryPath, "completion", shell)
	data, err := cmd.Output()
	if err != nil {
		_ = os.Remove(outputPath)
		return err
	}
	return os.WriteFile(outputPath, data, 0o644)
}
