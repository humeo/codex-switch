package quota

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestParseHeadersMapsCodexQuotaFields(t *testing.T) {
	headers := http.Header{}
	headers.Set("x-codex-plan-type", "plus")
	headers.Set("x-codex-primary-used-percent", "12")
	headers.Set("x-codex-secondary-used-percent", "34")
	headers.Set("x-codex-primary-reset-after-seconds", "60")
	headers.Set("x-codex-secondary-reset-after-seconds", "120")
	headers.Set("x-codex-primary-reset-at", "1700000000")
	headers.Set("x-codex-secondary-reset-at", "1700000123")
	headers.Set("x-codex-credits-has-credits", "true")
	headers.Set("x-codex-credits-balance", "42.50")

	got, err := ParseHeaders(headers)
	if err != nil {
		t.Fatalf("ParseHeaders() error = %v", err)
	}

	if got.Plan != "plus" {
		t.Fatalf("Plan = %q, want %q", got.Plan, "plus")
	}
	if got.PrimaryUsedPercent != 12 {
		t.Fatalf("PrimaryUsedPercent = %d, want 12", got.PrimaryUsedPercent)
	}
	if got.SecondaryUsedPercent != 34 {
		t.Fatalf("SecondaryUsedPercent = %d, want 34", got.SecondaryUsedPercent)
	}
	if got.PrimaryResetAfter != time.Minute {
		t.Fatalf("PrimaryResetAfter = %s, want %s", got.PrimaryResetAfter, time.Minute)
	}
	if got.SecondaryResetAfter != 2*time.Minute {
		t.Fatalf("SecondaryResetAfter = %s, want %s", got.SecondaryResetAfter, 2*time.Minute)
	}
	if got.PrimaryResetAt.Unix() != 1700000000 {
		t.Fatalf("PrimaryResetAt = %d, want 1700000000", got.PrimaryResetAt.Unix())
	}
	if got.SecondaryResetAt.Unix() != 1700000123 {
		t.Fatalf("SecondaryResetAt = %d, want 1700000123", got.SecondaryResetAt.Unix())
	}
	if !got.HasCredits {
		t.Fatal("HasCredits = false, want true")
	}
	if got.CreditsBalance != "42.50" {
		t.Fatalf("CreditsBalance = %q, want %q", got.CreditsBalance, "42.50")
	}
}

func TestCheckSendsCodexResponsesRequest(t *testing.T) {
	var (
		mu       sync.Mutex
		attempts int
	)

	client := Client{
		BaseURL: "https://example.test",
		HTTP: &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				mu.Lock()
				attempts++
				mu.Unlock()

				if req.Method != http.MethodPost {
					t.Fatalf("method = %s, want POST", req.Method)
				}
				if req.URL.Path != "/backend-api/codex/responses" {
					t.Fatalf("path = %s, want /backend-api/codex/responses", req.URL.Path)
				}
				if got := req.Header.Get("Authorization"); got != "Bearer access-token" {
					t.Fatalf("Authorization = %q, want Bearer access-token", got)
				}
				if got := req.Header.Get("ChatGPT-Account-Id"); got != "account-123" {
					t.Fatalf("ChatGPT-Account-Id = %q, want account-123", got)
				}
				if got := req.Header.Get("Content-Type"); got != "application/json" {
					t.Fatalf("Content-Type = %q, want application/json", got)
				}

				body, err := io.ReadAll(req.Body)
				if err != nil {
					t.Fatalf("reading body: %v", err)
				}
				gotBody := string(body)
				for _, want := range []string{
					`"model":"gpt-4.1"`,
					`"input":[{"role":"user","content":"hi"}]`,
					`"instructions":"."`,
					`"store":false`,
					`"reasoning":{"effort":"none"}`,
				} {
					if !strings.Contains(gotBody, want) {
						t.Fatalf("request body %s missing %s", gotBody, want)
					}
				}

				headers := http.Header{}
				headers.Set("x-codex-plan-type", "plus")
				headers.Set("x-codex-primary-used-percent", "7")
				headers.Set("x-codex-secondary-used-percent", "9")
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     headers,
					Body:       io.NopCloser(strings.NewReader("{}")),
				}, nil
			}),
		},
	}

	got, err := client.Check(context.Background(), Tokens{
		AccessToken: "access-token",
		AccountID:   "account-123",
	}, "gpt-4.1")
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
	if got.Plan != "plus" || got.PrimaryUsedPercent != 7 || got.SecondaryUsedPercent != 9 {
		t.Fatalf("snapshot = %+v, want parsed headers", got)
	}
}

func TestCheckTreats429AsFullyUsed(t *testing.T) {
	client := Client{
		HTTP: &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusTooManyRequests,
					Header:     http.Header{},
					Body:       io.NopCloser(strings.NewReader("too many requests")),
				}, nil
			}),
		},
	}

	got, err := client.Check(context.Background(), Tokens{}, "gpt-4.1")
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if got.PrimaryUsedPercent != 100 || got.SecondaryUsedPercent != 100 {
		t.Fatalf("snapshot = %+v, want both used to 100", got)
	}
}

func TestCheckRetriesTransientErrors(t *testing.T) {
	var attempts int
	client := Client{
		HTTP: &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				switch attempts {
				case 1:
					return nil, tempNetErr{}
				case 2:
					return &http.Response{
						StatusCode: http.StatusBadGateway,
						Header:     http.Header{},
						Body:       io.NopCloser(strings.NewReader("bad gateway")),
					}, nil
				default:
					headers := http.Header{}
					headers.Set("x-codex-primary-used-percent", "1")
					return &http.Response{
						StatusCode: http.StatusOK,
						Header:     headers,
						Body:       io.NopCloser(strings.NewReader("{}")),
					}, nil
				}
			}),
		},
	}

	got, err := client.Check(context.Background(), Tokens{}, "gpt-4.1")
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want 3", attempts)
	}
	if got.PrimaryUsedPercent != 1 {
		t.Fatalf("PrimaryUsedPercent = %d, want 1", got.PrimaryUsedPercent)
	}
}

func TestCheckDoesNotRetryNonRetryableParseErrors(t *testing.T) {
	var attempts int
	client := Client{
		HTTP: &http.Client{
			Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
				attempts++
				headers := http.Header{}
				headers.Set("x-codex-primary-used-percent", "not-a-number")
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     headers,
					Body:       io.NopCloser(strings.NewReader("{}")),
				}, nil
			}),
		},
	}

	if _, err := client.Check(context.Background(), Tokens{}, "gpt-4.1"); err == nil {
		t.Fatal("Check() error = nil, want parse failure")
	}
	if attempts != 1 {
		t.Fatalf("attempts = %d, want 1", attempts)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (fn roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

type tempNetErr struct{}

func (tempNetErr) Error() string   { return "temporary network error" }
func (tempNetErr) Timeout() bool   { return false }
func (tempNetErr) Temporary() bool { return true }

func (tempNetErr) Unwrap() error { return errors.New("temporary network error") }
