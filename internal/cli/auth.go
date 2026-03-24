package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"strings"

	"codex-switch/internal/config"
	"codex-switch/internal/profile"
	"codex-switch/internal/switcher"
	"github.com/spf13/cobra"
)

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
	RunStreaming(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, name string, args ...string) error {
	return exec.CommandContext(ctx, name, args...).Run()
}

func (execRunner) RunStreaming(ctx context.Context, stdout, stderr io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

var authRunnerFactory = func() Runner {
	return execRunner{}
}

var loginURLPattern = regexp.MustCompile(`https?://\S+`)

type authIdentity struct {
	Email string
}

type authProber interface {
	Probe(ctx context.Context, raw []byte, model string) (authIdentity, error)
}

type liveAuthProber struct {
	HTTP *http.Client
}

func (p liveAuthProber) Probe(ctx context.Context, raw []byte, model string) (authIdentity, error) {
	var stored profile.Profile
	if err := json.Unmarshal(raw, &stored); err != nil {
		return authIdentity{}, err
	}
	if stored.Tokens.AccessToken == "" {
		return authIdentity{}, errors.New("current auth is missing access token")
	}
	if stored.Tokens.AccountID == "" {
		return authIdentity{}, errors.New("current auth is missing account id")
	}
	if model == "" {
		model = config.Default().CheckModel
	}

	body, err := json.Marshal(map[string]any{
		"model": model,
		"input": []map[string]string{{
			"role":    "user",
			"content": "hi",
		}},
		"instructions": ".",
		"store":        false,
		"stream":       true,
		"reasoning": map[string]string{
			"effort": "none",
		},
	})
	if err != nil {
		return authIdentity{}, err
	}

	endpoint, err := url.JoinPath("https://chatgpt.com", "/backend-api/codex/responses")
	if err != nil {
		return authIdentity{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return authIdentity{}, err
	}
	req.Header.Set("Authorization", "Bearer "+stored.Tokens.AccessToken)
	req.Header.Set("ChatGPT-Account-Id", stored.Tokens.AccountID)
	req.Header.Set("Content-Type", "application/json")

	client := p.HTTP
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return authIdentity{}, err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode == http.StatusUnauthorized {
		return authIdentity{}, fmt.Errorf("auth probe failed with status %d", resp.StatusCode)
	}

	email := emailFromHeaders(resp.Header)
	if email == "" {
		email = emailFromIDToken(stored.Tokens.IDToken)
	}
	if email == "" {
		return authIdentity{}, errors.New("auth probe did not return a usable email")
	}
	return authIdentity{Email: email}, nil
}

var authProberFactory = func() authProber {
	return liveAuthProber{}
}

func newAuthCommand(deps Dependencies) *cobra.Command {
	var overwrite bool
	var login bool
	var nameFlag string

	cmd := &cobra.Command{
		Use:   "auth [name]",
		Short: "Save the current Codex auth profile or import one via login",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) (err error) {
			name, err := resolveRequestedProfileName(args, nameFlag)
			if err != nil {
				return err
			}
			store := profile.NewStore(deps.ProfilesDir)

			cfg, err := config.Load(deps.ConfigPath)
			if err != nil {
				return err
			}
			existingProfiles, err := store.List()
			if err != nil {
				return err
			}
			firstProfile := len(existingProfiles) == 0

			raw, err := os.ReadFile(deps.AuthPath)
			hasCurrentAuth := err == nil
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			prober := authProberFactory()
			identity := authIdentity{}

			switch {
			case login:
				raw, err = captureAuthViaLogin(cmd.Context(), cmd.ErrOrStderr(), deps.AuthPath)
				if err != nil {
					return err
				}
				identity, err = prober.Probe(cmd.Context(), raw, cfg.CheckModel)
				if err != nil {
					return err
				}
			case hasCurrentAuth:
				identity, err = prober.Probe(cmd.Context(), raw, cfg.CheckModel)
				if err != nil {
					return err
				}
			default:
				return fmt.Errorf("no current auth session found; use 'codex-switch auth --login' to add a new account")
			}

			if name == "" {
				name = identity.Email
			}
			if name == "" {
				return errors.New("could not determine profile name; pass --name")
			}

			if _, err := store.Load(name); err == nil && !overwrite {
				return fmt.Errorf("profile %q already exists; use --overwrite to replace it or 'codex-switch auth --login' to add a new account", name)
			} else if err != nil && !os.IsNotExist(err) {
				return err
			}

			if err := store.Save(name, raw); err != nil {
				return err
			}

			activeProfile := cfg.ActiveProfile
			if activeProfile != "" {
				if _, err := store.Load(activeProfile); err != nil {
					if !os.IsNotExist(err) {
						return err
					}
					activeProfile = ""
				}
			}
			if activeProfile == "" || firstProfile {
				activeProfile = name
			}

			activeRaw := raw
			if activeProfile != name {
				activeRaw, err = store.Load(activeProfile)
				if err != nil {
					return err
				}
			}
			if err := switcher.WriteAuthAtomically(deps.AuthPath, activeRaw); err != nil {
				return err
			}

			if cfg.ActiveProfile != activeProfile {
				cfg.ActiveProfile = activeProfile
				if err := config.Save(deps.ConfigPath, cfg); err != nil {
					return err
				}
			}

			fmt.Fprintf(cmd.OutOrStdout(), "saved profile: %s\n", name)
			return nil
		},
	}

	cmd.Flags().BoolVar(&login, "login", false, "run codex login to import a different account")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "replace an existing profile")
	cmd.Flags().StringVar(&nameFlag, "name", "", "set a local profile name")
	if deps.Stdout != nil {
		cmd.SetOut(deps.Stdout)
	}
	if deps.Stderr != nil {
		cmd.SetErr(deps.Stderr)
	}
	return cmd
}

func resolveRequestedProfileName(args []string, flagName string) (string, error) {
	if len(args) == 0 {
		return flagName, nil
	}
	if flagName == "" || flagName == args[0] {
		return args[0], nil
	}
	return "", errors.New("use either [name] or --name, not both")
}

func captureAuthViaLogin(ctx context.Context, stderr io.Writer, authPath string) (raw []byte, err error) {
	restore := func() error { return nil }
	if _, statErr := os.Stat(authPath); statErr == nil {
		backupPath := authPath + ".bak"
		_ = os.Remove(backupPath)
		if err := os.Rename(authPath, backupPath); err != nil {
			return nil, err
		}
		restore = func() error {
			_ = os.Remove(authPath)
			if err := os.Rename(backupPath, authPath); err != nil {
				return err
			}
			return nil
		}
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	} else {
		restore = func() error {
			_ = os.Remove(authPath)
			return nil
		}
	}

	defer func() {
		if restoreErr := restore(); restoreErr != nil && err == nil {
			err = restoreErr
		}
	}()

	runner := authRunnerFactory()
	fmt.Fprintln(stderr, "warning: codex logout and codex login will run now")
	if err := runner.Run(ctx, "codex", "logout"); err != nil {
		return nil, err
	}
	detector := newLoginURLHintWriter(stderr)
	stream := io.MultiWriter(stderr, detector)
	if err := runner.RunStreaming(ctx, stream, stream, "codex", "login"); err != nil {
		return nil, err
	}

	raw, err = os.ReadFile(authPath)
	if err != nil {
		return nil, err
	}
	return raw, nil
}

type loginURLHintWriter struct {
	dst     io.Writer
	buffer  string
	printed bool
}

func newLoginURLHintWriter(dst io.Writer) *loginURLHintWriter {
	return &loginURLHintWriter{dst: dst}
}

func (w *loginURLHintWriter) Write(p []byte) (int, error) {
	if w.printed {
		return len(p), nil
	}

	w.buffer += string(p)
	if match := loginURLPattern.FindString(w.buffer); match != "" {
		w.printed = true
		_, err := fmt.Fprintf(w.dst, "If the browser did not open automatically, open this link manually: %s\n", strings.TrimRight(match, ".,);]"))
		if err != nil {
			return 0, err
		}
	}

	if len(w.buffer) > 4096 {
		w.buffer = w.buffer[len(w.buffer)-4096:]
	}
	return len(p), nil
}

func emailFromHeaders(headers http.Header) string {
	for key, values := range headers {
		if !strings.Contains(strings.ToLower(key), "email") {
			continue
		}
		for _, value := range values {
			if looksLikeEmail(value) {
				return value
			}
		}
	}
	return ""
}

func emailFromIDToken(idToken string) string {
	parts := strings.Split(idToken, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}

	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		return ""
	}
	for _, key := range []string{"email", "preferred_username"} {
		if value, ok := claims[key].(string); ok && looksLikeEmail(value) {
			return value
		}
	}
	return ""
}

func looksLikeEmail(value string) bool {
	return strings.Count(value, "@") == 1 && !strings.ContainsAny(value, " \t\r\n")
}
