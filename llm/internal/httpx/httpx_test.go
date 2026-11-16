package httpx_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	core "github.com/resumeee-app/core"
	"github.com/resumeee-app/core/llm"
	"github.com/resumeee-app/core/llm/internal/httpx"
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
	const secretBody = "SENSITIVE_UPSTREAM_BODY_9911"
	if err := httpx.CheckStatusError("openai", http.StatusBadRequest, []byte(secretBody)); err == nil || strings.Contains(err.Error(), secretBody) {
		t.Errorf("expected CheckStatusError to redact upstream body from error string, got %v", err)
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

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type failingReadCloser struct {
	err error
}

func (f failingReadCloser) Read([]byte) (int, error) {
	return 0, f.err
}

func (f failingReadCloser) Close() error {
	return nil
}

func TestPostJSON_StatusAndErrorPreservation(t *testing.T) {
	t.Run("success encodes JSON payload, headers, and returns 200 body", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			if ct := r.Header.Get("Content-Type"); ct != "application/json" {
				t.Errorf("expected application/json Content-Type, got %q", ct)
			}
			if h := r.Header.Get("X-Custom"); h != "val" {
				t.Errorf("expected X-Custom=val, got %q", h)
			}
			var got map[string]string
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Fatalf("decode request body: %v", err)
			}
			if got["hello"] != "world" {
				t.Errorf("unexpected payload: %+v", got)
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer srv.Close()

		body, status, err := httpx.PostJSON(
			context.Background(),
			srv.Client(),
			srv.URL,
			map[string]string{"hello": "world"},
			map[string]string{"X-Custom": "val"},
			"test-provider",
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if status != http.StatusOK || string(body) != `{"ok":true}` {
			t.Fatalf("got status=%d body=%q, want 200 `{\"ok\":true}`", status, string(body))
		}
	})

	t.Run("non-200 responses return body, status code, and mapped error", func(t *testing.T) {
		cases := []struct {
			name       string
			statusCode int
			body       string
			wantErr    error
		}{
			{"400 bad request", http.StatusBadRequest, `{"err":"bad"}`, llm.ErrBadRequest},
			{"401 unauthorized", http.StatusUnauthorized, `unauth`, llm.ErrUnauthorized},
			{"403 forbidden", http.StatusForbidden, `forbid`, llm.ErrUnauthorized},
			{"429 rate limited", http.StatusTooManyRequests, `slow down`, llm.ErrRateLimited},
			{"500 upstream", http.StatusInternalServerError, `boom`, llm.ErrUpstream},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				body, status, err := httpx.PostJSON(context.Background(), srv.Client(), srv.URL, map[string]string{"a": "b"}, nil, "test-provider")
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("expected %v, got %v", tc.wantErr, err)
				}
				if status != tc.statusCode || string(body) != tc.body {
					t.Fatalf("got status=%d body=%q, want status=%d body=%q", status, string(body), tc.statusCode, tc.body)
				}
			})
		}
	})

	t.Run("body read error preserves non-zero resp.StatusCode", func(t *testing.T) {
		readErr := errors.New("stream reset")
		cases := []int{http.StatusOK, http.StatusBadGateway}
		for _, code := range cases {
			client := &http.Client{
				Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: code,
						Body:       failingReadCloser{err: readErr},
					}, nil
				}),
			}
			body, status, err := httpx.PostJSON(context.Background(), client, "https://example.test/v1", map[string]string{"k": "v"}, nil, "test-provider")
			if !errors.Is(err, readErr) {
				t.Fatalf("status %d: expected wrapped read error %v, got %v", code, readErr, err)
			}
			if body != nil {
				t.Fatalf("status %d: expected nil body on read error, got %q", code, string(body))
			}
			if status != code {
				t.Fatalf("status %d: expected preserved StatusCode %d, got %d", code, code, status)
			}
		}
	})

	t.Run("transport failure and timeout return nil body and status 0", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case <-r.Context().Done():
			case <-time.After(150 * time.Millisecond):
			}
			w.WriteHeader(http.StatusOK)
		}))
		defer srv.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		body, status, err := httpx.PostJSON(ctx, srv.Client(), srv.URL, map[string]string{"k": "v"}, nil, "test-provider")
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", err)
		}
		if body != nil || status != 0 {
			t.Fatalf("expected (nil, 0) on transport timeout, got (%q, %d)", string(body), status)
		}

		nilRespClient := &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return nil, nil
			}),
		}
		body, status, err = httpx.PostJSON(context.Background(), nilRespClient, "https://example.test/v1", map[string]string{"k": "v"}, nil, "test-provider")
		if err == nil || body != nil || status != 0 {
			t.Fatalf("expected error with (nil, 0) for nil http.Response, got (%q, %d, %v)", string(body), status, err)
		}
	})

	t.Run("nil response body preserves status code without panicking", func(t *testing.T) {
		nilBodyClient := &http.Client{
			Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusServiceUnavailable, Body: nil}, nil
			}),
		}
		body, status, err := httpx.PostJSON(context.Background(), nilBodyClient, "https://example.test/v1", map[string]string{"k": "v"}, nil, "test-provider")
		if !errors.Is(err, llm.ErrUpstream) || len(body) != 0 || status != http.StatusServiceUnavailable {
			t.Fatalf("expected ErrUpstream with status 503 and empty body for nil Body, got (%q, %d, %v)", string(body), status, err)
		}
	})
}

type stubHTTPXClient struct {
	body []byte
}

func (c *stubHTTPXClient) Name() string                           { return llm.ProviderOpenAI }
func (c *stubHTTPXClient) DefaultModel() string                   { return "gpt-4o" }
func (c *stubHTTPXClient) Models() []core.ModelOption             { return nil }
func (c *stubHTTPXClient) SupportsStructuredOutput(_ string) bool { return true }
func (c *stubHTTPXClient) Complete(_ context.Context, _ llm.Request) (*llm.Response, error) {
	return &llm.Response{
		RawBody:    string(c.body),
		StatusCode: http.StatusBadRequest,
	}, httpx.CheckStatusError(llm.ProviderOpenAI, http.StatusBadRequest, c.body)
}

func TestRegistry_Complete_RedactsHTTPXBodyFromLogs(t *testing.T) {
	const secretPII = "SECRET_PROMPT_LEAK_TOKEN_7788"
	body := []byte(`{"error":{"message":"echoed prompt: ` + secretPII + `"}}`)

	var logBuf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logBuf, &slog.HandlerOptions{Level: slog.LevelDebug}))

	client := &stubHTTPXClient{body: body}
	reg := llm.NewRegistry(llm.WithLogger(logger))
	reg.Register(client)

	_, _, err := reg.CompleteStructured(context.Background(), client, "GenerateGapAnalysis", llm.Request{
		Model:        "gpt-4o",
		SystemPrompt: "sys",
		UserPrompt:   secretPII,
	})
	if !errors.Is(err, llm.ErrBadRequest) {
		t.Fatalf("expected ErrBadRequest, got %v", err)
	}
	if strings.Contains(err.Error(), secretPII) {
		t.Errorf("expected sanitized error without response body, got %q", err.Error())
	}
	if strings.Contains(logBuf.String(), secretPII) {
		t.Errorf("structured logs leaked upstream error body:\n%s", logBuf.String())
	}
}
