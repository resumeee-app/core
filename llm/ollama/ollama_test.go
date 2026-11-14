package ollama_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

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
