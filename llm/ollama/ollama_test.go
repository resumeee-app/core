package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/llm/ollama"
)

func boolPtr(v bool) *bool { return &v }

func sampleTestSchema() *llm.Schema {
	return &llm.Schema{
		Type: llm.SchemaTypes{llm.TypeObject},
		Properties: map[string]*llm.Schema{
			"summary": {
				Type:        llm.SchemaTypes{llm.TypeString},
				Description: "Tailored summary",
			},
			"notes": {
				Type:        llm.SchemaTypes{llm.TypeString},
				Description: "Optional notes",
			},
		},
		Required:             []string{"summary"},
		AdditionalProperties: boolPtr(false),
	}
}

func cloneJSONSnapshot(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("failed to snapshot JSON: %v", err)
	}
	return string(b)
}

func TestOllamaClient_Complete_StructuredAndPlainText(t *testing.T) {
	t.Run("structured response with stream=false, format schema, and Bearer auth", func(t *testing.T) {
		schema := sampleTestSchema()
		beforeSnapshot := cloneJSONSnapshot(t, schema)

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/chat" {
				t.Errorf("expected /api/chat, got %q", r.URL.Path)
			}
			if r.Header.Get("Authorization") != "Bearer ollama-token" {
				t.Errorf("expected Bearer ollama-token, got %q", r.Header.Get("Authorization"))
			}
			rawReqBytes, err := io.ReadAll(r.Body)
			if err != nil {
				t.Fatalf("failed to read request body: %v", err)
			}
			var body map[string]any
			if err := json.Unmarshal(rawReqBytes, &body); err != nil {
				t.Fatalf("failed to unmarshal ollama request: %v", err)
			}
			streamVal, hasStream := body["stream"]
			if !hasStream || streamVal != false {
				t.Errorf("expected explicit \"stream\": false in ollama payload, got has=%v val=%v (%s)", hasStream, streamVal, string(rawReqBytes))
			}
			fmtMap, ok := body["format"].(map[string]any)
			if !ok || fmtMap["type"] != "object" {
				t.Fatalf("expected format JSON schema object, got %v", body["format"])
			}
			if _, hasMeta := fmtMap["$schema"]; hasMeta {
				t.Errorf("expected $schema meta-keyword absent from ollama format")
			}

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"model": "llama3.1",
				"message": {"role": "assistant", "content": "{\"summary\":\"Ollama Structured\"}"},
				"done": true,
				"prompt_eval_count": 64,
				"eval_count": 36
			}`))
		}))
		defer srv.Close()

		client := ollama.New(srv.URL, "ollama-token")
		if client.Name() != llm.ProviderOllama {
			t.Errorf("expected %q, got %q", llm.ProviderOllama, client.Name())
		}
		if !client.SupportsStructuredOutput("") {
			t.Errorf("expected ollama.Client.SupportsStructuredOutput to be true")
		}
		defaultOllamaModel := llm.DefaultModelForProvider(llm.ProviderOllama)
		if len(client.Models()) != 1 || client.Models()[0].Model != defaultOllamaModel {
			t.Errorf("expected default model %q, got %+v", defaultOllamaModel, client.Models())
		}

		resp, err := client.Complete(context.Background(), llm.Request{
			SystemPrompt:   "sys",
			UserPrompt:     "user",
			ResponseSchema: schema,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if resp.Text != `{"summary":"Ollama Structured"}` {
			t.Errorf("unexpected text: %q", resp.Text)
		}
		if resp.Usage.PromptTokens != 64 || resp.Usage.CompletionTokens != 36 || resp.Usage.TotalTokens != 100 {
			t.Errorf("unexpected usage: %+v", resp.Usage)
		}
		if after := cloneJSONSnapshot(t, schema); after != beforeSnapshot {
			t.Errorf("ollama.Client mutated caller schema!\nbefore: %s\nafter:  %s", beforeSnapshot, after)
		}
	})

	t.Run("plain text omits format, keeps stream=false, omits Authorization when key empty, normalizes URLs", func(t *testing.T) {
		suffixes := []string{"", "/", "/api", "/api/chat"}
		for _, suffix := range suffixes {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/chat" {
					t.Errorf("suffix %q: expected /api/chat, got %q", suffix, r.URL.Path)
				}
				if auth := r.Header.Get("Authorization"); auth != "" {
					t.Errorf("expected empty Authorization header when no apiKey set, got %q", auth)
				}
				var body map[string]any
				_ = json.NewDecoder(r.Body).Decode(&body)
				if streamVal, ok := body["stream"]; !ok || streamVal != false {
					t.Errorf("expected explicit stream=false, got %v", body["stream"])
				}
				if _, hasFormat := body["format"]; hasFormat {
					t.Errorf("did not expect format on plain-text ollama request")
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"message":{"role":"assistant","content":"  plain ollama  "},"prompt_eval_count":10,"eval_count":5}`))
			}))

			client := ollama.New(srv.URL+suffix, "", ollama.WithDefaultModel("qwen2.5:14b"))
			resp, err := client.Complete(context.Background(), llm.Request{
				UserPrompt: "hello",
			})
			srv.Close()
			if err != nil {
				t.Fatalf("suffix %q: unexpected error: %v", suffix, err)
			}
			if resp.Text != "plain ollama" || resp.Usage.TotalTokens != 15 {
				t.Errorf("suffix %q: unexpected response %+v", suffix, resp)
			}
		}
	})

	t.Run("nil client guard returns ErrNoProviders", func(t *testing.T) {
		if _, err := (*ollama.Client)(nil).Complete(context.Background(), llm.Request{UserPrompt: "ping"}); !errors.Is(err, llm.ErrNoProviders) {
			t.Errorf("expected ErrNoProviders for nil ollama.Client, got %v", err)
		}
	})

	t.Run("non-200 HTTP errors, malformed JSON, and empty content", func(t *testing.T) {
		cases := []struct {
			name       string
			statusCode int
			body       string
			wantErr    error
		}{
			{"400 bad request", http.StatusBadRequest, `{"error":"invalid format schema"}`, llm.ErrBadRequest},
			{"401 unauthorized", http.StatusUnauthorized, `unauthorized`, llm.ErrUnauthorized},
			{"429 rate limited", http.StatusTooManyRequests, `too many requests`, llm.ErrRateLimited},
			{"500 internal error", http.StatusInternalServerError, `model runner crashed`, llm.ErrUpstream},
			{"200 malformed json", http.StatusOK, `{not-json`, llm.ErrInvalidResponse},
			{"200 empty message content", http.StatusOK, `{"message":{"role":"assistant","content":"   "},"prompt_eval_count":11,"eval_count":4}`, llm.ErrInvalidResponse},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				client := ollama.New(srv.URL, "")
				resp, err := client.Complete(context.Background(), llm.Request{UserPrompt: "hi"})
				if !errors.Is(err, tc.wantErr) {
					t.Errorf("expected %v, got %v", tc.wantErr, err)
				}
				if resp == nil || resp.StatusCode != tc.statusCode {
					t.Errorf("expected status %d in response, got %+v", tc.statusCode, resp)
				}
				if tc.name == "200 empty message content" && resp != nil && resp.Usage.TotalTokens != 15 {
					t.Errorf("expected empty-content error response to preserve usage TotalTokens=15, got %+v", resp.Usage)
				}
			})
		}
	})
}

type ollamaRoundTripFunc func(*http.Request) (*http.Response, error)

func (f ollamaRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

type ollamaFailingBody struct {
	delay time.Duration
	err   error
}

func (b ollamaFailingBody) Read([]byte) (int, error) {
	if b.delay > 0 {
		time.Sleep(b.delay)
	}
	return 0, b.err
}

func (b ollamaFailingBody) Close() error {
	return nil
}

func TestOllamaClient_Complete_TelemetryAndEdgeCases(t *testing.T) {
	t.Run("success populates provider, resolved model, status code, latency, and normalized usage", func(t *testing.T) {
		cases := []struct {
			name         string
			defaultModel string
			reqModel     string
			responseBody string
			wantModel    string
			wantText     string
			wantUsage    core.TokenUsage
		}{
			{
				name:         "default model with prompt_eval_count and eval_count",
				defaultModel: "",
				reqModel:     "",
				responseBody: `{"message":{"role":"assistant","content":"  hello ollama  "},"prompt_eval_count":64,"eval_count":36}`,
				wantModel:    "llama3.1",
				wantText:     "hello ollama",
				wantUsage:    core.TokenUsage{PromptTokens: 64, CompletionTokens: 36, TotalTokens: 100},
			},
			{
				name:         "explicit request model with partial prompt_eval_count only",
				defaultModel: "llama3.1",
				reqModel:     "  qwen2.5:14b  ",
				responseBody: `{"message":{"role":"assistant","content":"ok"},"prompt_eval_count":27,"eval_count":0}`,
				wantModel:    "qwen2.5:14b",
				wantText:     "ok",
				wantUsage:    core.TokenUsage{PromptTokens: 27, CompletionTokens: 0, TotalTokens: 27},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(tc.responseBody))
				}))
				defer srv.Close()

				var opts []ollama.Option
				if tc.defaultModel != "" {
					opts = append(opts, ollama.WithDefaultModel(tc.defaultModel))
				}
				client := ollama.New(srv.URL, "", opts...)
				resp, err := client.Complete(context.Background(), llm.Request{
					Model:      tc.reqModel,
					UserPrompt: "hi",
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp == nil {
					t.Fatal("expected non-nil response")
				}
				if resp.Provider != llm.ProviderOllama {
					t.Errorf("Provider = %q, want %q", resp.Provider, llm.ProviderOllama)
				}
				if resp.Model != tc.wantModel {
					t.Errorf("Model = %q, want %q", resp.Model, tc.wantModel)
				}
				if resp.StatusCode != http.StatusOK {
					t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
				}
				if resp.Latency <= 0 {
					t.Errorf("expected Latency > 0, got %v", resp.Latency)
				}
				if resp.Text != tc.wantText {
					t.Errorf("Text = %q, want %q", resp.Text, tc.wantText)
				}
				if resp.Usage != tc.wantUsage {
					t.Errorf("Usage = %+v, want %+v", resp.Usage, tc.wantUsage)
				}
			})
		}
	})

	t.Run("non-2xx HTTP errors preserve provider, model, status code, latency, and raw body", func(t *testing.T) {
		cases := []struct {
			name       string
			statusCode int
			body       string
			wantErr    error
		}{
			{"400 bad request", http.StatusBadRequest, `{"error":"invalid format schema"}`, llm.ErrBadRequest},
			{"401 unauthorized", http.StatusUnauthorized, `unauthorized`, llm.ErrUnauthorized},
			{"429 rate limited", http.StatusTooManyRequests, `too many requests`, llm.ErrRateLimited},
			{"500 internal error", http.StatusInternalServerError, `model runner crashed`, llm.ErrUpstream},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				client := ollama.New(srv.URL, "", ollama.WithDefaultModel("qwen2.5:14b"))
				resp, err := client.Complete(context.Background(), llm.Request{UserPrompt: "hi"})
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				if resp == nil {
					t.Fatal("expected non-nil response on HTTP error")
				}
				if resp.Provider != llm.ProviderOllama || resp.Model != "qwen2.5:14b" || resp.StatusCode != tc.statusCode {
					t.Errorf("unexpected telemetry metadata: %+v", resp)
				}
				if resp.Latency <= 0 {
					t.Errorf("expected Latency > 0, got %v", resp.Latency)
				}
				if resp.RawBody != tc.body {
					t.Errorf("RawBody = %q, want %q", resp.RawBody, tc.body)
				}
			})
		}
	})

	t.Run("200 malformed JSON and empty message content preserve telemetry and partial usage", func(t *testing.T) {
		cases := []struct {
			name      string
			body      string
			wantUsage core.TokenUsage
		}{
			{
				name:      "malformed json",
				body:      `{not-json`,
				wantUsage: core.TokenUsage{},
			},
			{
				name:      "empty message content preserves prompt and eval tokens",
				body:      `{"message":{"role":"assistant","content":"   "},"prompt_eval_count":11,"eval_count":4}`,
				wantUsage: core.TokenUsage{PromptTokens: 11, CompletionTokens: 4, TotalTokens: 15},
			},
			{
				name:      "empty message content normalizes prompt-only tokens",
				body:      `{"message":{"role":"assistant","content":""},"prompt_eval_count":9,"eval_count":0}`,
				wantUsage: core.TokenUsage{PromptTokens: 9, CompletionTokens: 0, TotalTokens: 9},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusOK)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				client := ollama.New(srv.URL, "")
				resp, err := client.Complete(context.Background(), llm.Request{
					Model:      "mistral:7b",
					UserPrompt: "hi",
				})
				if !errors.Is(err, llm.ErrInvalidResponse) {
					t.Fatalf("err = %v, want %v", err, llm.ErrInvalidResponse)
				}
				if resp == nil {
					t.Fatal("expected non-nil response on 200 payload error")
				}
				if resp.Provider != llm.ProviderOllama || resp.Model != "mistral:7b" || resp.StatusCode != http.StatusOK {
					t.Errorf("unexpected telemetry metadata: %+v", resp)
				}
				if resp.Latency <= 0 {
					t.Errorf("expected Latency > 0, got %v", resp.Latency)
				}
				if resp.RawBody != tc.body {
					t.Errorf("RawBody = %q, want %q", resp.RawBody, tc.body)
				}
				if resp.Usage != tc.wantUsage {
					t.Errorf("Usage = %+v, want %+v", resp.Usage, tc.wantUsage)
				}
			})
		}
	})

	t.Run("upstream timeout, connection failure, and body-read error return telemetry response", func(t *testing.T) {
		t.Run("context deadline exceeded", func(t *testing.T) {
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

			client := ollama.New(srv.URL, "")
			resp, err := client.Complete(ctx, llm.Request{
				Model:      "qwen2.5:14b",
				UserPrompt: "hi",
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("err = %v, want context.DeadlineExceeded", err)
			}
			if resp == nil {
				t.Fatal("expected non-nil response on timeout")
			}
			if resp.Provider != llm.ProviderOllama || resp.Model != "qwen2.5:14b" || resp.StatusCode != 0 {
				t.Errorf("unexpected timeout telemetry: %+v", resp)
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0 on timeout, got %v", resp.Latency)
			}
		})

		t.Run("connection failure", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			closedURL := srv.URL
			srv.Close()

			client := ollama.New(closedURL, "")
			resp, err := client.Complete(context.Background(), llm.Request{UserPrompt: "hi"})
			if err == nil {
				t.Fatal("expected connection error, got nil")
			}
			if resp == nil {
				t.Fatal("expected non-nil response on connection error")
			}
			if resp.Provider != llm.ProviderOllama || resp.Model != "llama3.1" || resp.StatusCode != 0 {
				t.Errorf("unexpected connection-error telemetry: %+v", resp)
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0 on connection error, got %v", resp.Latency)
			}
		})

		t.Run("body read error preserves status code", func(t *testing.T) {
			readErr := io.ErrUnexpectedEOF
			httpClient := &http.Client{
				Transport: ollamaRoundTripFunc(func(_ *http.Request) (*http.Response, error) {
					return &http.Response{
						StatusCode: http.StatusBadGateway,
						Body:       ollamaFailingBody{delay: 2 * time.Millisecond, err: readErr},
					}, nil
				}),
			}
			client := ollama.New("https://example.test", "", ollama.WithHTTPClient(httpClient))
			resp, err := client.Complete(context.Background(), llm.Request{UserPrompt: "hi"})
			if !errors.Is(err, readErr) {
				t.Fatalf("err = %v, want %v", err, readErr)
			}
			if resp == nil {
				t.Fatal("expected non-nil response on body-read error")
			}
			if resp.Provider != llm.ProviderOllama || resp.Model != "llama3.1" || resp.StatusCode != http.StatusBadGateway {
				t.Errorf("unexpected body-read telemetry: %+v", resp)
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0 on body-read error, got %v", resp.Latency)
			}
		})
	})

	t.Run("nil receiver guard returns nil response and ErrNoProviders", func(t *testing.T) {
		resp, err := (*ollama.Client)(nil).Complete(context.Background(), llm.Request{UserPrompt: "ping"})
		if !errors.Is(err, llm.ErrNoProviders) {
			t.Errorf("err = %v, want ErrNoProviders", err)
		}
		if resp != nil {
			t.Errorf("expected nil response for nil receiver, got %+v", resp)
		}
	})
}
