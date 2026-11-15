package openaicompat_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/llm"
	"github.com/resumeee-app/resumeee-core/llm/openaicompat"
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
		},
		Required:             []string{"summary"},
		AdditionalProperties: boolPtr(false),
	}
}

func TestOpenAICompatClient_Complete_FallbackAndNoDoubleSchema(t *testing.T) {
	schema := sampleTestSchema()
	var capturedMessages []map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("expected /v1/chat/completions, got %q", r.URL.Path)
		}
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		capturedMessages = body.Messages
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"choices": [{"message": {"content": "{\"summary\":\"Compat Tailored\"}"}}],
			"usage": {"prompt_tokens": 30, "completion_tokens": 10}
		}`))
	}))
	defer srv.Close()

	client := openaicompat.New(srv.URL+"/v1", "optional-key", openaicompat.WithDefaultModel("llama3.3"))
	if client.Name() != llm.ProviderCompat {
		t.Errorf("expected %q, got %q", llm.ProviderCompat, client.Name())
	}
	if client.SupportsStructuredOutput("") {
		t.Errorf("expected openaicompat.Client.SupportsStructuredOutput to return false")
	}

	// 1. Direct call without <response_schema> embeds <json_output_schema>
	resp, err := client.Complete(context.Background(), llm.Request{
		SystemPrompt:   "sys",
		UserPrompt:     "user",
		ResponseSchema: schema,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sysMsg, _ := capturedMessages[0]["content"].(string)
	if !strings.Contains(sysMsg, "<json_output_schema>") {
		t.Errorf("expected <json_output_schema> fallback on direct call, got %q", sysMsg)
	}
	if resp.Text != `{"summary":"Compat Tailored"}` || resp.Usage.TotalTokens != 40 {
		t.Errorf("unexpected compat response: %+v", resp)
	}

	// 2. Call when <response_schema> is already present in SystemPrompt does NOT double-append
	preCompiledSys := "sys\n\n<response_schema>\n{\"type\":\"object\"}\n</response_schema>"
	_, err = client.Complete(context.Background(), llm.Request{
		SystemPrompt:   preCompiledSys,
		UserPrompt:     "user",
		ResponseSchema: schema,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sysMsgAfter, _ := capturedMessages[0]["content"].(string)
	if strings.Contains(sysMsgAfter, "<json_output_schema>") {
		t.Errorf("expected no duplicate <json_output_schema> when <response_schema> already present, got %q", sysMsgAfter)
	}
	if !reflect.DeepEqual(sysMsgAfter, preCompiledSys) {
		t.Errorf("expected system prompt unchanged, got %q", sysMsgAfter)
	}
}

func TestOpenAICompatClient_NilGuard(t *testing.T) {
	if resp, err := (*openaicompat.Client)(nil).Complete(context.Background(), llm.Request{UserPrompt: "ping"}); !errors.Is(err, llm.ErrNoProviders) || resp != nil {
		t.Errorf("expected (nil, ErrNoProviders) for nil openaicompat.Client, got (%+v, %v)", resp, err)
	}
}

func TestOpenAICompatClient_Complete_Telemetry(t *testing.T) {
	t.Run("usage extraction and model resolution", func(t *testing.T) {
		cases := []struct {
			name         string
			defaultModel string
			reqModel     string
			body         string
			wantModel    string
			wantUsage    core.TokenUsage
		}{
			{
				name:      "explicit total tokens preserved with provider default model",
				body:      `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":30,"completion_tokens":10,"total_tokens":55}}`,
				wantModel: llm.DefaultModelForProvider(llm.ProviderCompat),
				wantUsage: core.TokenUsage{PromptTokens: 30, CompletionTokens: 10, TotalTokens: 55},
			},
			{
				name:      "omitted total tokens backfilled with explicit request model",
				reqModel:  "  qwen-2.5-coder  ",
				body:      `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":30,"completion_tokens":10}}`,
				wantModel: "qwen-2.5-coder",
				wantUsage: core.TokenUsage{PromptTokens: 30, CompletionTokens: 10, TotalTokens: 40},
			},
			{
				name:         "prompt-only usage backfilled with custom default model",
				defaultModel: "llama3.3",
				body:         `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":16}}`,
				wantModel:    "llama3.3",
				wantUsage:    core.TokenUsage{PromptTokens: 16, TotalTokens: 16},
			},
			{
				name:      "omitted usage stays zero",
				body:      `{"choices":[{"message":{"content":"ok"}}]}`,
				wantModel: llm.DefaultModelForProvider(llm.ProviderCompat),
				wantUsage: core.TokenUsage{},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				opts := []openaicompat.Option{openaicompat.WithHTTPClient(srv.Client())}
				if tc.defaultModel != "" {
					opts = append(opts, openaicompat.WithDefaultModel(tc.defaultModel))
				}
				client := openaicompat.New(srv.URL+"/v1", "", opts...)

				resp, err := client.Complete(context.Background(), llm.Request{
					Model:      tc.reqModel,
					UserPrompt: "hello",
				})
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if resp == nil {
					t.Fatalf("expected non-nil response")
				}
				if resp.Provider != llm.ProviderCompat || resp.Model != tc.wantModel || resp.StatusCode != http.StatusOK {
					t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 200)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderCompat, tc.wantModel)
				}
				if resp.Latency <= 0 {
					t.Errorf("expected Latency > 0, got %v", resp.Latency)
				}
				if resp.Usage != tc.wantUsage {
					t.Errorf("Usage = %+v, want %+v", resp.Usage, tc.wantUsage)
				}
			})
		}
	})

	t.Run("HTTP errors and 200 invalid responses populate telemetry and partial usage", func(t *testing.T) {
		cases := []struct {
			name       string
			reqModel   string
			statusCode int
			body       string
			wantErr    error
			wantModel  string
			wantUsage  core.TokenUsage
		}{
			{
				name:       "400 bad request",
				statusCode: http.StatusBadRequest,
				body:       `{"error":"bad request"}`,
				wantErr:    llm.ErrBadRequest,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderCompat),
			},
			{
				name:       "401 unauthorized",
				reqModel:   "llama3.3",
				statusCode: http.StatusUnauthorized,
				body:       `{"error":"unauthorized"}`,
				wantErr:    llm.ErrUnauthorized,
				wantModel:  "llama3.3",
			},
			{
				name:       "429 rate limited",
				statusCode: http.StatusTooManyRequests,
				body:       `{"error":"rate limited"}`,
				wantErr:    llm.ErrRateLimited,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderCompat),
			},
			{
				name:       "500 upstream error",
				statusCode: http.StatusInternalServerError,
				body:       `{"error":"server error"}`,
				wantErr:    llm.ErrUpstream,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderCompat),
			},
			{
				name:       "200 malformed json",
				statusCode: http.StatusOK,
				body:       `{broken`,
				wantErr:    llm.ErrInvalidResponse,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderCompat),
			},
			{
				name:       "200 empty choices preserves partial usage with backfill",
				reqModel:   "mistral-7b",
				statusCode: http.StatusOK,
				body:       `{"choices":[],"usage":{"prompt_tokens":21,"completion_tokens":9}}`,
				wantErr:    llm.ErrInvalidResponse,
				wantModel:  "mistral-7b",
				wantUsage:  core.TokenUsage{PromptTokens: 21, CompletionTokens: 9, TotalTokens: 30},
			},
			{
				name:       "200 empty choices preserves explicit total usage",
				statusCode: http.StatusOK,
				body:       `{"choices":[],"usage":{"prompt_tokens":20,"completion_tokens":5,"total_tokens":28}}`,
				wantErr:    llm.ErrInvalidResponse,
				wantModel:  llm.DefaultModelForProvider(llm.ProviderCompat),
				wantUsage:  core.TokenUsage{PromptTokens: 20, CompletionTokens: 5, TotalTokens: 28},
			},
		}

		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					w.WriteHeader(tc.statusCode)
					_, _ = w.Write([]byte(tc.body))
				}))
				defer srv.Close()

				client := openaicompat.New(srv.URL+"/v1", "optional-key", openaicompat.WithHTTPClient(srv.Client()))
				resp, err := client.Complete(context.Background(), llm.Request{
					Model:      tc.reqModel,
					UserPrompt: "hi",
				})
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("error = %v, want %v", err, tc.wantErr)
				}
				if resp == nil {
					t.Fatalf("expected non-nil response on error")
				}
				if resp.Provider != llm.ProviderCompat || resp.Model != tc.wantModel || resp.StatusCode != tc.statusCode {
					t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, %d)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderCompat, tc.wantModel, tc.statusCode)
				}
				if resp.Latency <= 0 {
					t.Errorf("expected Latency > 0, got %v", resp.Latency)
				}
				if resp.Usage != tc.wantUsage {
					t.Errorf("Usage = %+v, want %+v", resp.Usage, tc.wantUsage)
				}
			})
		}
	})

	t.Run("timeout and transport errors return non-nil telemetry with zero status", func(t *testing.T) {
		t.Run("context deadline exceeded", func(t *testing.T) {
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-done:
				case <-r.Context().Done():
				case <-time.After(500 * time.Millisecond):
				}
			}))
			defer srv.Close()
			defer close(done)

			client := openaicompat.New(srv.URL+"/v1", "", openaicompat.WithHTTPClient(srv.Client()))
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
			defer cancel()

			resp, err := client.Complete(ctx, llm.Request{
				Model:      "llama3.3",
				UserPrompt: "ping",
			})
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("expected context.DeadlineExceeded, got %v", err)
			}
			if resp == nil {
				t.Fatalf("expected non-nil response on timeout")
			}
			if resp.Provider != llm.ProviderCompat || resp.Model != "llama3.3" || resp.StatusCode != 0 {
				t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 0)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderCompat, "llama3.3")
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0, got %v", resp.Latency)
			}
		})

		t.Run("client WithTimeout exceeded", func(t *testing.T) {
			done := make(chan struct{})
			srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
				select {
				case <-done:
				case <-r.Context().Done():
				case <-time.After(500 * time.Millisecond):
				}
			}))
			defer srv.Close()
			defer close(done)

			client := openaicompat.New(
				srv.URL+"/v1",
				"",
				openaicompat.WithHTTPClient(srv.Client()),
				openaicompat.WithTimeout(15*time.Millisecond),
			)
			resp, err := client.Complete(context.Background(), llm.Request{
				Model:      "qwen-2.5",
				UserPrompt: "ping",
			})
			if err == nil {
				t.Fatalf("expected timeout error from WithTimeout")
			}
			if resp == nil {
				t.Fatalf("expected non-nil response on client timeout")
			}
			if resp.Provider != llm.ProviderCompat || resp.Model != "qwen-2.5" || resp.StatusCode != 0 {
				t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 0)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderCompat, "qwen-2.5")
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0, got %v", resp.Latency)
			}
		})

		t.Run("closed server transport failure", func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			srvURL := srv.URL
			httpClient := srv.Client()
			srv.Close()

			client := openaicompat.New(srvURL+"/v1", "", openaicompat.WithHTTPClient(httpClient))
			resp, err := client.Complete(context.Background(), llm.Request{UserPrompt: "ping"})
			if err == nil {
				t.Fatalf("expected transport error against closed server")
			}
			if resp == nil {
				t.Fatalf("expected non-nil response on transport error")
			}
			wantModel := llm.DefaultModelForProvider(llm.ProviderCompat)
			if resp.Provider != llm.ProviderCompat || resp.Model != wantModel || resp.StatusCode != 0 {
				t.Errorf("metadata = (provider=%q, model=%q, status=%d), want (%q, %q, 0)", resp.Provider, resp.Model, resp.StatusCode, llm.ProviderCompat, wantModel)
			}
			if resp.Latency <= 0 {
				t.Errorf("expected Latency > 0, got %v", resp.Latency)
			}
		})
	})
}
