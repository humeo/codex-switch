package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"codex-switch/internal/profile"
)

const (
	defaultBaseURL  = "https://auth.openai.com"
	defaultClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
)

type Refresher struct {
	BaseURL  string
	ClientID string
	HTTP     *http.Client
}

func (r Refresher) Refresh(ctx context.Context, refreshToken string) (profile.Tokens, error) {
	if refreshToken == "" {
		return profile.Tokens{}, errors.New("refresh token is empty")
	}

	baseURL := r.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}

	clientID := r.ClientID
	if clientID == "" {
		clientID = defaultClientID
	}

	client := r.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	endpoint, err := url.JoinPath(baseURL, "oauth", "token")
	if err != nil {
		return profile.Tokens{}, err
	}

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", clientID)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return profile.Tokens{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return profile.Tokens{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return profile.Tokens{}, err
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		snippet := strings.TrimSpace(string(body))
		if snippet != "" {
			return profile.Tokens{}, fmt.Errorf("refresh token request failed: status %d: %s", resp.StatusCode, snippet)
		}
		return profile.Tokens{}, fmt.Errorf("refresh token request failed: status %d", resp.StatusCode)
	}

	var payload struct {
		AccessToken  string `json:"access_token"`
		IDToken      string `json:"id_token"`
		RefreshToken string `json:"refresh_token"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return profile.Tokens{}, fmt.Errorf("decode refresh response: %w", err)
	}
	if payload.AccessToken == "" || payload.IDToken == "" || payload.RefreshToken == "" {
		return profile.Tokens{}, errors.New("refresh response missing token fields")
	}

	return profile.Tokens{
		AccessToken:  payload.AccessToken,
		IDToken:      payload.IDToken,
		RefreshToken: payload.RefreshToken,
	}, nil
}
