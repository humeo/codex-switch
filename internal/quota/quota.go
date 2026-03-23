package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Tokens struct {
	AccessToken string
	AccountID   string
}

type Snapshot struct {
	Plan                 string
	PrimaryUsedPercent   int
	SecondaryUsedPercent int
	PrimaryResetAfter    time.Duration
	SecondaryResetAfter  time.Duration
	PrimaryResetAt       time.Time
	SecondaryResetAt     time.Time
	HasCredits           bool
	CreditsBalance       string
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func ParseHeaders(headers http.Header) (Snapshot, error) {
	var snapshot Snapshot

	if value := headers.Get("x-codex-plan-type"); value != "" {
		snapshot.Plan = value
	}
	if value := headers.Get("x-codex-primary-used-percent"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse x-codex-primary-used-percent: %w", err)
		}
		snapshot.PrimaryUsedPercent = n
	}
	if value := headers.Get("x-codex-secondary-used-percent"); value != "" {
		n, err := strconv.Atoi(value)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse x-codex-secondary-used-percent: %w", err)
		}
		snapshot.SecondaryUsedPercent = n
	}
	if value := headers.Get("x-codex-primary-reset-after-seconds"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse x-codex-primary-reset-after-seconds: %w", err)
		}
		snapshot.PrimaryResetAfter = time.Duration(n) * time.Second
	}
	if value := headers.Get("x-codex-secondary-reset-after-seconds"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse x-codex-secondary-reset-after-seconds: %w", err)
		}
		snapshot.SecondaryResetAfter = time.Duration(n) * time.Second
	}
	if value := headers.Get("x-codex-primary-reset-at"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse x-codex-primary-reset-at: %w", err)
		}
		snapshot.PrimaryResetAt = time.Unix(n, 0)
	}
	if value := headers.Get("x-codex-secondary-reset-at"); value != "" {
		n, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse x-codex-secondary-reset-at: %w", err)
		}
		snapshot.SecondaryResetAt = time.Unix(n, 0)
	}
	if value := headers.Get("x-codex-credits-has-credits"); value != "" {
		b, err := strconv.ParseBool(value)
		if err != nil {
			return Snapshot{}, fmt.Errorf("parse x-codex-credits-has-credits: %w", err)
		}
		snapshot.HasCredits = b
	}
	if value := headers.Get("x-codex-credits-balance"); value != "" {
		snapshot.CreditsBalance = value
	}

	return snapshot, nil
}

func (c Client) Check(ctx context.Context, tokens Tokens, model string) (Snapshot, error) {
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = "https://chatgpt.com"
	}

	client := c.HTTP
	if client == nil {
		client = http.DefaultClient
	}

	requestBody := quotaRequest{
		Model: model,
		Input: []quotaMessage{{
			Role:    "user",
			Content: "hi",
		}},
		Instructions: ".",
		Store:        false,
		Stream:       true,
		Reasoning: quotaReasoning{
			Effort: "none",
		},
	}
	bodyBytes, err := json.Marshal(requestBody)
	if err != nil {
		return Snapshot{}, err
	}

	endpoint, err := url.JoinPath(baseURL, "/backend-api/codex/responses")
	if err != nil {
		return Snapshot{}, err
	}

	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(bodyBytes))
		if err != nil {
			return Snapshot{}, err
		}
		req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
		req.Header.Set("ChatGPT-Account-Id", tokens.AccountID)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			if !isRetryableError(err) || attempt == 2 {
				return Snapshot{}, err
			}
			lastErr = err
			sleepForAttempt(ctx, attempt)
			continue
		}

		snapshot, retryable, err := snapshotFromResponse(resp)
		if err != nil {
			if !retryable || attempt == 2 {
				return Snapshot{}, err
			}
			lastErr = err
			sleepForAttempt(ctx, attempt)
			continue
		}
		if retryable {
			lastErr = fmt.Errorf("retryable response status %d", resp.StatusCode)
			if attempt == 2 {
				return Snapshot{}, lastErr
			}
			sleepForAttempt(ctx, attempt)
			continue
		}
		return snapshot, nil
	}

	if lastErr != nil {
		return Snapshot{}, lastErr
	}
	return Snapshot{}, errors.New("quota check failed")
}

type quotaRequest struct {
	Model        string         `json:"model"`
	Input        []quotaMessage `json:"input"`
	Instructions string         `json:"instructions"`
	Store        bool           `json:"store"`
	Stream       bool           `json:"stream"`
	Reasoning    quotaReasoning `json:"reasoning"`
}

type quotaMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type quotaReasoning struct {
	Effort string `json:"effort"`
}

func snapshotFromResponse(resp *http.Response) (Snapshot, bool, error) {
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return Snapshot{}, false, err
	}

	switch resp.StatusCode {
	case http.StatusOK:
		snapshot, err := ParseHeaders(resp.Header)
		return snapshot, false, err
	case http.StatusTooManyRequests:
		return Snapshot{PrimaryUsedPercent: 100, SecondaryUsedPercent: 100}, false, nil
	case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
		return Snapshot{}, true, fmt.Errorf("retryable response status %d", resp.StatusCode)
	case http.StatusForbidden:
		if looksLikeHTMLChallenge(resp.Header, body) {
			return Snapshot{}, true, fmt.Errorf("retryable response status %d", resp.StatusCode)
		}
		return Snapshot{}, false, fmt.Errorf("unexpected 403 response")
	default:
		return Snapshot{}, false, fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
}

func looksLikeHTMLChallenge(headers http.Header, body []byte) bool {
	contentType := strings.ToLower(headers.Get("Content-Type"))
	if strings.Contains(contentType, "text/html") {
		return true
	}

	normalized := strings.ToLower(string(body))
	return strings.Contains(normalized, "<html") ||
		strings.Contains(normalized, "cf-chl") ||
		strings.Contains(normalized, "attention required") ||
		strings.Contains(normalized, "challenge")
}

func isRetryableError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return netErr.Timeout() || netErr.Temporary()
	}
	return false
}

func sleepForAttempt(ctx context.Context, attempt int) {
	delay := time.Duration(10*(1<<attempt)) * time.Millisecond
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
