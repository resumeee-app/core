package httpx_test

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/llm/internal/httpx"
)

func TestTransportUtilities_BoundsAndStatusError(t *testing.T) {
	b, err := httpx.ReadBoundedBody(nil)
	if err != nil || b != nil {
		t.Errorf("expected nil for nil reader, got %v, %v", b, err)
	}

	b, err = httpx.ReadBoundedBody(bytes.NewReader([]byte("hello")))
	if err != nil || string(b) != "hello" {
		t.Errorf("unexpected ReadBoundedBody output: %q, %v", string(b), err)
	}

	if err := httpx.CheckStatusError("openai", http.StatusBadRequest, []byte("bad")); !errors.Is(err, llm.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest, got %v", err)
	}
	if err := httpx.CheckStatusError("openai", http.StatusUnauthorized, []byte("unauth")); !errors.Is(err, llm.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized, got %v", err)
	}
	if err := httpx.CheckStatusError("openai", http.StatusForbidden, []byte("forbid")); !errors.Is(err, llm.ErrUnauthorized) {
		t.Errorf("expected ErrUnauthorized for 403, got %v", err)
	}
	if err := httpx.CheckStatusError("openai", http.StatusTooManyRequests, []byte("rate")); !errors.Is(err, llm.ErrRateLimited) {
		t.Errorf("expected ErrRateLimited, got %v", err)
	}
	if err := httpx.CheckStatusError("openai", http.StatusTooManyRequests, []byte(`{"error":{"code":"insufficient_quota","message":"You exceeded your current quota, please check your plan and billing details."}}`)); !errors.Is(err, llm.ErrUpstreamQuota) {
		t.Errorf("expected ErrUpstreamQuota for OpenAI insufficient_quota 429, got %v", err)
	}
	if err := httpx.CheckStatusError("anthropic", http.StatusBadRequest, []byte(`{"error":{"message":"Your credit balance is too low to access the Anthropic API."}}`)); !errors.Is(err, llm.ErrUpstreamQuota) {
		t.Errorf("expected ErrUpstreamQuota for Anthropic credit balance low, got %v", err)
	}
	if err := httpx.CheckStatusError("openai", http.StatusPaymentRequired, []byte("payment required")); !errors.Is(err, llm.ErrUpstreamQuota) {
		t.Errorf("expected ErrUpstreamQuota for 402 Payment Required, got %v", err)
	}
	if err := httpx.CheckStatusError("openai", http.StatusUnprocessableEntity, []byte("invalid model field")); !errors.Is(err, llm.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest for 422, got %v", err)
	}
	if err := httpx.CheckStatusError("openai", http.StatusNotFound, []byte("model not found")); !errors.Is(err, llm.ErrBadRequest) {
		t.Errorf("expected ErrBadRequest for 404, got %v", err)
	}
	if err := httpx.CheckStatusError("openai", http.StatusBadGateway, []byte("502")); !errors.Is(err, llm.ErrUpstream) {
		t.Errorf("expected ErrUpstream, got %v", err)
	}
	if err := httpx.CheckStatusError("openai", http.StatusTeapot, []byte("418")); err == nil {
		t.Error("expected error for status 418")
	}

	oversizedBody := strings.Repeat("A", 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(oversizedBody))
	}))
	defer srv.Close()

	wrappedClient := httpx.WrapBoundedClientWithLimit(srv.Client(), 16)
	resp, err := wrappedClient.Get(srv.URL)
	if err != nil {
		t.Fatalf("unexpected wrapped client error: %v", err)
	}
	got, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("unexpected io.ReadAll error: %v", err)
	}
	if len(got) != 16 || string(got) != strings.Repeat("A", 16) {
		t.Fatalf("expected bounded transport to truncate at 16 bytes, got len=%d (%q)", len(got), string(got))
	}
	if closeErr := resp.Body.Close(); closeErr != nil {
		t.Fatalf("unexpected Close error: %v", closeErr)
	}
}

func TestSettings_FinalizeTimeoutIsolation(t *testing.T) {
	callerClient := &http.Client{Timeout: 5 * time.Second}
	s := httpx.DefaultSettings("https://example.com", "model-1")
	s.SetHTTPClient(callerClient)
	s.SetTimeout(45 * time.Second)
	s.Finalize()

	if s.HTTPClient.Timeout != 45*time.Second {
		t.Fatalf("expected finalized HTTPClient timeout 45s, got %v", s.HTTPClient.Timeout)
	}
	if callerClient.Timeout != 5*time.Second {
		t.Fatalf("expected caller HTTPClient timeout to remain 5s, got %v", callerClient.Timeout)
	}

	defaultSettings := httpx.DefaultSettings("https://example.com", "model-2")
	origDefault := defaultSettings.HTTPClient
	defaultSettings.SetTimeout(30 * time.Second)
	defaultSettings.Finalize()
	if defaultSettings.HTTPClient.Timeout != 30*time.Second {
		t.Fatalf("expected custom timeout 30s, got %v", defaultSettings.HTTPClient.Timeout)
	}
	if origDefault.Timeout != httpx.DefaultTimeout {
		t.Fatalf("expected shared default client timeout %v unchanged, got %v", httpx.DefaultTimeout, origDefault.Timeout)
	}
}
