package auth

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"codex-switch/internal/profile"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRefresherRefreshUpdatesTokens(t *testing.T) {
	t.Parallel()

	var gotMethod string
	var gotContentType string
	var gotForm url.Values
	var handlerErr error
	var gotURL string

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		gotURL = r.URL.String()

		body, err := io.ReadAll(r.Body)
		handlerErr = err
		if handlerErr != nil {
			return nil, handlerErr
		}

		gotForm, err = url.ParseQuery(string(body))
		handlerErr = err
		if handlerErr != nil {
			return nil, handlerErr
		}

		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body: io.NopCloser(strings.NewReader(`{
			"access_token":"new-access",
			"id_token":"new-id",
			"refresh_token":"new-refresh",
			"expires_in":3600,
			"scope":"openid profile",
			"token_type":"Bearer"
		}`)),
			Request: r,
		}
		resp.Header.Set("Content-Type", "application/json")
		return resp, nil
	})}

	got, err := Refresher{HTTP: client}.Refresh(context.Background(), "old-refresh")
	if err != nil {
		t.Fatalf("Refresh() error = %v", err)
	}
	if handlerErr != nil {
		t.Fatalf("handler error = %v", handlerErr)
	}

	want := profile.Tokens{
		AccessToken:  "new-access",
		IDToken:      "new-id",
		RefreshToken: "new-refresh",
	}
	if got != want {
		t.Fatalf("Refresh() = %#v, want %#v", got, want)
	}

	if gotMethod != http.MethodPost {
		t.Fatalf("request method = %q, want %q", gotMethod, http.MethodPost)
	}
	if ct := gotContentType; !strings.HasPrefix(ct, "application/x-www-form-urlencoded") {
		t.Fatalf("content-type = %q, want application/x-www-form-urlencoded", ct)
	}
	if gotForm.Get("grant_type") != "refresh_token" {
		t.Fatalf("grant_type = %q, want refresh_token", gotForm.Get("grant_type"))
	}
	if gotForm.Get("refresh_token") != "old-refresh" {
		t.Fatalf("refresh_token = %q, want old-refresh", gotForm.Get("refresh_token"))
	}
	if gotForm.Get("client_id") != defaultClientID {
		t.Fatalf("client_id = %q, want %q", gotForm.Get("client_id"), defaultClientID)
	}
	if gotURL != defaultBaseURL+"/oauth/token" {
		t.Fatalf("request URL = %q, want %q", gotURL, defaultBaseURL+"/oauth/token")
	}
}

func TestRefresherRefreshReturnsErrorForNon2xx(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		resp := &http.Response{
			StatusCode: http.StatusUnauthorized,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader("expired")),
		}
		return resp, nil
	})}

	_, err := Refresher{HTTP: client}.Refresh(context.Background(), "old-refresh")
	if err == nil {
		t.Fatal("Refresh() error = nil, want non-nil")
	}
}

func TestRefresherRefreshReturnsErrorForMalformedPayload(t *testing.T) {
	t.Parallel()

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		resp := &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"access_token":`)),
		}
		return resp, nil
	})}

	_, err := Refresher{HTTP: client}.Refresh(context.Background(), "old-refresh")
	if err == nil {
		t.Fatal("Refresh() error = nil, want non-nil")
	}
}

func TestUpdateTokensRewritesProfileJSON(t *testing.T) {
	t.Parallel()

	raw := []byte(`{
		"auth_mode":"chatgpt",
		"OPENAI_API_KEY":null,
		"tokens":{
			"access_token":"old-access",
			"id_token":"old-id",
			"refresh_token":"old-refresh",
			"account_id":"acct-1"
		},
		"last_refresh":"2026-03-22T17:03:55.366292Z"
	}`)

	refreshedAt := time.Date(2026, 3, 23, 10, 11, 12, 345678000, time.FixedZone("UTC+8", 8*60*60))
	tokens := profile.Tokens{
		AccessToken:  "new-access",
		IDToken:      "new-id",
		RefreshToken: "new-refresh",
		AccountID:    "acct-1",
	}

	got, err := profile.UpdateTokens(raw, tokens, refreshedAt)
	if err != nil {
		t.Fatalf("UpdateTokens() error = %v", err)
	}

	var decoded struct {
		AuthMode    string `json:"auth_mode"`
		OpenAIAPI   any    `json:"OPENAI_API_KEY"`
		LastRefresh string `json:"last_refresh"`
		Tokens      struct {
			AccessToken  string `json:"access_token"`
			IDToken      string `json:"id_token"`
			RefreshToken string `json:"refresh_token"`
			AccountID    string `json:"account_id"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if decoded.AuthMode != "chatgpt" {
		t.Fatalf("auth_mode = %q, want chatgpt", decoded.AuthMode)
	}
	if decoded.Tokens.AccessToken != "new-access" || decoded.Tokens.IDToken != "new-id" || decoded.Tokens.RefreshToken != "new-refresh" {
		t.Fatalf("tokens = %#v, want updated token values", decoded.Tokens)
	}
	if decoded.Tokens.AccountID != "acct-1" {
		t.Fatalf("account_id = %q, want acct-1", decoded.Tokens.AccountID)
	}
	if decoded.LastRefresh != refreshedAt.UTC().Format(time.RFC3339Nano) {
		t.Fatalf("last_refresh = %q, want %q", decoded.LastRefresh, refreshedAt.UTC().Format(time.RFC3339Nano))
	}
	if decoded.OpenAIAPI != nil {
		t.Fatalf("OPENAI_API_KEY = %#v, want nil", decoded.OpenAIAPI)
	}
}

func TestUpdateTokensRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := profile.UpdateTokens([]byte("{"), profile.Tokens{}, time.Now())
	if err == nil {
		t.Fatal("UpdateTokens() error = nil, want non-nil")
	}
}

func TestRefresherRefreshRejectsEmptyRefreshToken(t *testing.T) {
	t.Parallel()

	_, err := Refresher{}.Refresh(context.Background(), "")
	if err == nil {
		t.Fatal("Refresh() error = nil, want non-nil")
	}
}

func TestUpdateTokensPreservesUnknownFields(t *testing.T) {
	t.Parallel()

	raw := []byte(`{"auth_mode":"chatgpt","tokens":{"access_token":"old"},"custom":{"enabled":true}}`)
	got, err := profile.UpdateTokens(raw, profile.Tokens{AccessToken: "new"}, time.Unix(0, 0).UTC())
	if err != nil {
		t.Fatalf("UpdateTokens() error = %v", err)
	}

	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	custom, ok := decoded["custom"].(map[string]any)
	if !ok {
		t.Fatalf("custom = %#v, want object", decoded["custom"])
	}
	if enabled, ok := custom["enabled"].(bool); !ok || !enabled {
		t.Fatalf("custom.enabled = %#v, want true", custom["enabled"])
	}
}
