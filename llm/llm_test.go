package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"resumeee/internal/config"
	"resumeee/internal/domain"
)

var sampleResponse = domain.GenerationResponse{
	TailoredContent: domain.TailoredContent{
		Summary: "Staff Systems Engineer specializing in low-latency distributed storage engines.",
		SkillGroups: []domain.TailoredSkillGroup{
			{
				Category:       "Systems Programming",
				Skills:         []string{"Go", "Rust", "C++"},
				SourceSkillIDs: []string{"skill-uuid-1", "skill-uuid-2"},
			},
		},
		Experiences: []domain.TailoredExperience{
			{
				SourceEntityID: "exp-uuid-1",
				Company:        "Acme Corp",
				RoleTitle:      "Staff Engineer",
				StartDate:      domain.NewDate(2021, 3, 1),
				Bullets: []domain.TailoredBullet{
					{
						Text:           "Reduced p99 replication latency by 45% via raft batch pipelining.",
						SourceFactIDs:  []string{"fact-uuid-101", "fact-uuid-102"},
						SourceSkillIDs: []string{"skill-uuid-1"},
					},
				},
			},
		},
		Projects: []domain.TailoredProject{
			{
				SourceEntityID: "proj-uuid-1",
				Name:           "QuantumFS",
				Bullets: []domain.TailoredBullet{
					{
						Text:          "Authored append-only distributed log in Go with deterministic fault testing.",
						SourceFactIDs: []string{"fact-uuid-201"},
					},
				},
			},
		},
		Education: []domain.TailoredEducation{
			{
				SourceEntityID: "edu-uuid-1",
				Institution:    "Carnegie Mellon University",
				Degree:         "B.S.",
				Field:          "Computer Science",
				StartDate:      domain.DatePtr(domain.NewDate(2013, 9, 1)),
			},
		},
		Certifications: []domain.TailoredCert{
			{
				SourceEntityID: "cert-uuid-1",
				Name:           "Certified Kubernetes Administrator",
				Issuer:         "CNCF",
				Date:           domain.DatePtr(domain.NewDate(2022, 6, 15)),
			},
		},
	},
	GapReport: domain.GapReport{
		OverallMatchSummary: "Exceptional alignment with distributed systems core requirements.",
		MissingRequirements: []domain.GapItem{
			{
				Requirement:      "Kafka Streams operational production experience",
				Importance:       domain.GapImportancePreferred,
				ClarifyingPrompt: "Have you operated Kafka Streams applications at high partition counts?",
			},
		},
	},
}

func sampleRequest() domain.GenerationRequest {
	return domain.GenerationRequest{
		Model: "test-model-override",
		MasterProfile: &domain.MasterProfileSnapshot{
			Profile: domain.Profile{
				FullName:           "Ada Lovelace",
				GlobalWritingRules: "Rule 1: Always highlight algorithmic complexity.",
			},
			Skills: []domain.Skill{
				{ID: "skill-uuid-1", Name: "Go"},
			},
			Experiences: []domain.WorkExperience{
				{ID: "exp-uuid-1", Company: "Acme Corp", RoleTitle: "Staff Engineer"},
			},
			Facts: []domain.Fact{
				{ID: "fact-uuid-101", Statement: "Implemented batching in Raft layer."},
			},
			ExportedAt: time.Now(),
		},
		GlobalRules:        "Never use passive voice.",
		PresetInstructions: "Maintain strict 1-page formatting constraints.",
		BranchContext: domain.Branch{
			ID:                 "branch-uuid-1",
			Name:               "Stripe Backend",
			TargetRole:         "Staff Distributed Systems Engineer",
			TargetCompany:      "Stripe",
			TargetRequirements: "Strong experience with Raft consensus and consensus algorithms.",
			BranchInstructions: "Emphasize fault injection testing experience.",
			PinnedEntityIDs:    []string{"exp-uuid-1"},
			ExcludedEntityIDs:  []string{"proj-uuid-99"},
		},
		PriorVersion: &domain.TailoredContent{
			Summary: "Prior refined summary by user.",
		},
	}
}

// -----------------------------------------------------------------------------
// 1. Schema Tests
// -----------------------------------------------------------------------------

func TestGenerationResponseSchema_Integrity(t *testing.T) {
	schema := GenerationResponseSchema()

	if schema["type"] != "object" {
		t.Fatalf("expected schema type to be object, got %v", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties map in schema")
	}

	// Verify tailored_content and gap_report existence
	if _, ok := props["tailored_content"]; !ok {
		t.Errorf("missing tailored_content property")
	}
	gapReportProp, ok := props["gap_report"].(map[string]any)
	if !ok {
		t.Fatalf("missing or invalid gap_report property")
	}
	gapProps, ok := gapReportProp["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties in gap_report schema")
	}
	if _, ok := gapProps["strategic_recommendations"]; !ok {
		t.Errorf("missing strategic_recommendations in gap_report schema")
	}
	missingReqs, ok := gapProps["missing_requirements"].(map[string]any)
	if !ok {
		t.Fatalf("missing missing_requirements in gap_report schema")
	}
	items, ok := missingReqs["items"].(map[string]any)
	if !ok {
		t.Fatalf("missing items in missing_requirements schema")
	}
	itemProps, ok := items["properties"].(map[string]any)
	if !ok {
		t.Fatalf("missing properties in missing_requirements item schema")
	}
	if _, ok := itemProps["missing_area"]; !ok {
		t.Errorf("missing missing_area in gap item schema")
	}
	if _, ok := itemProps["recommended_action"]; !ok {
		t.Errorf("missing recommended_action in gap item schema")
	}

	reqs, ok := schema["required"].([]string)
	if !ok || len(reqs) != 2 {
		t.Fatalf("expected 2 required root properties, got %v", reqs)
	}

	// Verify serializable to JSON
	bytes, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("failed to marshal schema: %v", err)
	}
	if len(bytes) == 0 {
		t.Errorf("empty schema JSON")
	}
}

// -----------------------------------------------------------------------------
// 2. Prompt Composition Tests
// -----------------------------------------------------------------------------

func TestPromptBuilder_Composition(t *testing.T) {
	req := sampleRequest()

	prompts, err := BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error building prompt: %v", err)
	}

	// Check System Prompt Grounding Invariants
	if !strings.Contains(prompts.SystemPrompt, "Anti-Hallucination (\"No Lies\" Invariant)") {
		t.Errorf("system prompt missing Anti-Hallucination header")
	}
	if !strings.Contains(prompts.SystemPrompt, "source_fact_ids") {
		t.Errorf("system prompt missing source_fact_ids citation directive")
	}
	if !strings.Contains(prompts.SystemPrompt, "Do NOT flag a qualification as missing if it is present or directly implied") {
		t.Errorf("system prompt missing gap grounding constraint forbidding false positives for documented facts")
	}
	if !strings.Contains(prompts.SystemPrompt, "Do NOT invent or extrapolate requirements") {
		t.Errorf("system prompt missing gap grounding constraint forbidding fabricated job requirements")
	}

	// Check 3-Tier Hierarchy in User Prompt
	if !strings.Contains(prompts.UserPrompt, "Tier 1: Global Writing Rules:\nNever use passive voice.") {
		t.Errorf("user prompt missing Tier 1 rules")
	}
	if !strings.Contains(prompts.UserPrompt, "Tier 2: Preset Formatting Instructions:\nMaintain strict 1-page formatting constraints.") {
		t.Errorf("user prompt missing Tier 2 rules")
	}
	if !strings.Contains(prompts.UserPrompt, "Target Role: Staff Distributed Systems Engineer") {
		t.Errorf("user prompt missing target role")
	}
	if !strings.Contains(prompts.UserPrompt, "Target Company: Stripe") {
		t.Errorf("user prompt missing target company")
	}
	if !strings.Contains(prompts.UserPrompt, "Pinned Entity IDs (Must Include): exp-uuid-1") {
		t.Errorf("user prompt missing pinned entity IDs")
	}
	if !strings.Contains(prompts.UserPrompt, "Excluded Entity IDs (Must Omit): proj-uuid-99") {
		t.Errorf("user prompt missing excluded entity IDs")
	}

	// Check Prior Active Version Preservation (ADR-002)
	if !strings.Contains(prompts.UserPrompt, "<prior_active_version>") {
		t.Errorf("user prompt missing prior_active_version tag")
	}
	if !strings.Contains(prompts.UserPrompt, "Prior refined summary by user.") {
		t.Errorf("user prompt missing prior tailored content JSON")
	}
	if !strings.Contains(prompts.UserPrompt, "preserve the phrasing, tone, and specific wordings") {
		t.Errorf("user prompt missing continuity instructions")
	}

	// Check Master Profile Graph presence
	if !strings.Contains(prompts.UserPrompt, "Ada Lovelace") {
		t.Errorf("user prompt missing master profile snapshot data")
	}
}

func TestPromptBuilder_NilPriorVersionFallback(t *testing.T) {
	req := sampleRequest()
	req.PriorVersion = nil

	prompts, err := BuildPrompt(req)
	if err != nil {
		t.Fatalf("unexpected error building prompt: %v", err)
	}

	if !strings.Contains(prompts.UserPrompt, "None (Initial generation)") {
		t.Errorf("expected 'None (Initial generation)' fallback for nil prior version, got prompt:\n%s", prompts.UserPrompt)
	}
}

// -----------------------------------------------------------------------------
// 3. Google Gemini Adapter Tests
// -----------------------------------------------------------------------------

func TestGeminiProvider_Success(t *testing.T) {
	responseJSON, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}

	var capturedKey string
	var capturedPath string
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedKey = r.Header.Get("x-goog-api-key")
		capturedPath = r.URL.Path

		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &capturedBody)

		geminiResp := map[string]any{
			"candidates": []map[string]any{
				{
					"content": map[string]any{
						"parts": []map[string]any{
							{"text": string(responseJSON)},
						},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(geminiResp)
	}))
	defer server.Close()

	provider := NewGeminiProvider("test-gemini-key",
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)

	if provider.Name() != ProviderGemini {
		t.Errorf("expected provider name %s, got %s", ProviderGemini, provider.Name())
	}

	req := sampleRequest()
	resp, err := provider.GenerateTailoredResume(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error from gemini provider: %v", err)
	}

	// Verify authentication header
	if capturedKey != "test-gemini-key" {
		t.Errorf("expected x-goog-api-key 'test-gemini-key', got %q", capturedKey)
	}

	// Verify URL path includes model override
	expectedPath := "/v1beta/models/test-model-override:generateContent"
	if capturedPath != expectedPath {
		t.Errorf("expected path %s, got %s", expectedPath, capturedPath)
	}

	// Verify schema presence in generationConfig
	genConfig, ok := capturedBody["generationConfig"].(map[string]any)
	if !ok || genConfig["responseMimeType"] != "application/json" {
		t.Errorf("expected responseMimeType application/json in generationConfig")
	}
	if _, ok := genConfig["responseSchema"]; !ok {
		t.Errorf("expected responseSchema in generationConfig")
	}

	// Verify unmarshaled content
	if resp.TailoredContent.Summary != sampleResponse.TailoredContent.Summary {
		t.Errorf("tailored summary mismatch: got %q, want %q", resp.TailoredContent.Summary, sampleResponse.TailoredContent.Summary)
	}
	if len(resp.GapReport.MissingRequirements) != 1 {
		t.Errorf("expected 1 gap item, got %d", len(resp.GapReport.MissingRequirements))
	}
}

func TestGeminiProvider_Errors(t *testing.T) {
	testCases := []struct {
		name          string
		statusCode    int
		responseBody  string
		expectedError error
	}{
		{"Unauthorized", http.StatusUnauthorized, `{"error": "invalid API key"}`, ErrUnauthorized},
		{"RateLimited", http.StatusTooManyRequests, `{"error": "quota exceeded"}`, ErrRateLimited},
		{"UpstreamFailure", http.StatusInternalServerError, `{"error": "internal crash"}`, ErrUpstream},
		{"MalformedJSON", http.StatusOK, `{"candidates": [{"content": {"parts": [{"text": "not valid json {"}]}}]}`, ErrInvalidResponse},
		{"EmptyCandidates", http.StatusOK, `{"candidates": []}`, ErrInvalidResponse},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.responseBody))
			}))
			defer server.Close()

			provider := NewGeminiProvider("key", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
			_, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, tc.expectedError) {
				t.Errorf("expected error %v, got %v", tc.expectedError, err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 4. OpenAI Adapter Tests
// -----------------------------------------------------------------------------

func TestOpenAIProvider_Success(t *testing.T) {
	responseJSON, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}

	var capturedAuth string
	var capturedPath string
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedPath = r.URL.Path

		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &capturedBody)

		openAIResp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": string(responseJSON),
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(openAIResp)
	}))
	defer server.Close()

	provider := NewOpenAIProvider("test-openai-key",
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)

	if provider.Name() != ProviderOpenAI {
		t.Errorf("expected provider name %s, got %s", ProviderOpenAI, provider.Name())
	}

	req := sampleRequest()
	resp, err := provider.GenerateTailoredResume(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error from openai provider: %v", err)
	}

	// Verify Bearer auth header
	if capturedAuth != "Bearer test-openai-key" {
		t.Errorf("expected Authorization 'Bearer test-openai-key', got %q", capturedAuth)
	}

	// Verify URL path
	if capturedPath != "/v1/chat/completions" {
		t.Errorf("expected path /v1/chat/completions, got %s", capturedPath)
	}

	// Verify response_format structure
	respFormat, ok := capturedBody["response_format"].(map[string]any)
	if !ok || respFormat["type"] != "json_schema" {
		t.Fatalf("expected response_format type json_schema, got %v", respFormat)
	}
	jsonSchema, ok := respFormat["json_schema"].(map[string]any)
	if !ok || jsonSchema["strict"] != true {
		t.Errorf("expected json_schema.strict == true")
	}
	if _, ok := jsonSchema["schema"]; !ok {
		t.Errorf("missing schema in json_schema")
	}

	// Verify unmarshaled response
	if resp.TailoredContent.Summary != sampleResponse.TailoredContent.Summary {
		t.Errorf("summary mismatch")
	}
}

func TestOpenAIProvider_Errors(t *testing.T) {
	testCases := []struct {
		name          string
		statusCode    int
		responseBody  string
		expectedError error
	}{
		{"Unauthorized", http.StatusUnauthorized, `{"error": {"message": "Invalid API key"}}`, ErrUnauthorized},
		{"RateLimited", http.StatusTooManyRequests, `{"error": {"message": "Rate limit reached"}}`, ErrRateLimited},
		{"UpstreamFailure", http.StatusBadGateway, `{"error": {"message": "Bad gateway"}}`, ErrUpstream},
		{"MalformedJSON", http.StatusOK, `{"choices": [{"message": {"content": "not json"}}]}`, ErrInvalidResponse},
		{"EmptyChoices", http.StatusOK, `{"choices": []}`, ErrInvalidResponse},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.responseBody))
			}))
			defer server.Close()

			provider := NewOpenAIProvider("key", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
			_, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, tc.expectedError) {
				t.Errorf("expected error %v, got %v", tc.expectedError, err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 5. Anthropic Adapter Tests
// -----------------------------------------------------------------------------

func TestAnthropicProvider_Success(t *testing.T) {
	responseJSON, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}

	var capturedKey string
	var capturedVersion string
	var capturedPath string
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedKey = r.Header.Get("x-api-key")
		capturedVersion = r.Header.Get("anthropic-version")
		capturedPath = r.URL.Path

		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &capturedBody)

		anthropicResp := map[string]any{
			"content": []map[string]any{
				{
					"type":  "tool_use",
					"id":    "tool_use_123",
					"name":  "generate_tailored_resume",
					"input": json.RawMessage(responseJSON),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(anthropicResp)
	}))
	defer server.Close()

	provider := NewAnthropicProvider("test-anthropic-key",
		WithBaseURL(server.URL),
		WithHTTPClient(server.Client()),
	)

	if provider.Name() != ProviderAnthropic {
		t.Errorf("expected provider name %s, got %s", ProviderAnthropic, provider.Name())
	}

	req := sampleRequest()
	resp, err := provider.GenerateTailoredResume(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error from anthropic provider: %v", err)
	}

	// Verify Anthropic headers
	if capturedKey != "test-anthropic-key" {
		t.Errorf("expected x-api-key 'test-anthropic-key', got %q", capturedKey)
	}
	if capturedVersion != "2023-06-01" {
		t.Errorf("expected anthropic-version '2023-06-01', got %q", capturedVersion)
	}

	// Verify URL path
	if capturedPath != "/v1/messages" {
		t.Errorf("expected path /v1/messages, got %s", capturedPath)
	}

	// Verify forced tool_choice
	toolChoice, ok := capturedBody["tool_choice"].(map[string]any)
	if !ok || toolChoice["type"] != "tool" || toolChoice["name"] != "generate_tailored_resume" {
		t.Errorf("expected tool_choice {type: 'tool', name: 'generate_tailored_resume'}, got %v", toolChoice)
	}

	// Verify tools array input_schema
	tools, ok := capturedBody["tools"].([]any)
	if !ok || len(tools) != 1 {
		t.Fatalf("expected 1 tool in tools array, got %v", tools)
	}
	tool0, _ := tools[0].(map[string]any)
	if tool0["name"] != "generate_tailored_resume" {
		t.Errorf("expected tool name 'generate_tailored_resume'")
	}
	if _, ok := tool0["input_schema"]; !ok {
		t.Errorf("missing input_schema in tool definition")
	}

	// Verify unmarshaled content
	if resp.TailoredContent.Summary != sampleResponse.TailoredContent.Summary {
		t.Errorf("summary mismatch")
	}
}

func TestAnthropicProvider_Errors(t *testing.T) {
	testCases := []struct {
		name          string
		statusCode    int
		responseBody  string
		expectedError error
	}{
		{"Unauthorized", http.StatusUnauthorized, `{"type": "error", "error": {"type": "authentication_error"}}`, ErrUnauthorized},
		{"RateLimited", http.StatusTooManyRequests, `{"type": "error", "error": {"type": "rate_limit_error"}}`, ErrRateLimited},
		{"UpstreamFailure", http.StatusServiceUnavailable, `{"type": "error", "error": {"type": "overloaded_error"}}`, ErrUpstream},
		{"MalformedInputJSON", http.StatusOK, `{"content": [{"type": "tool_use", "name": "generate_tailored_resume", "input": "not json"}]}`, ErrInvalidResponse},
		{"MissingToolUse", http.StatusOK, `{"content": [{"type": "text", "text": "I refuse to use the tool"}]}`, ErrInvalidResponse},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.statusCode)
				_, _ = w.Write([]byte(tc.responseBody))
			}))
			defer server.Close()

			provider := NewAnthropicProvider("key", WithBaseURL(server.URL), WithHTTPClient(server.Client()))
			_, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !errors.Is(err, tc.expectedError) {
				t.Errorf("expected error %v, got %v", tc.expectedError, err)
			}
		})
	}
}

// -----------------------------------------------------------------------------
// 6. Generic OpenAI-Compatible Adapter Tests
// -----------------------------------------------------------------------------

func TestCompatProvider_SuccessWithMarkdownFences(t *testing.T) {
	responseJSON, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}

	// Wrap in markdown code fences to verify sanitizer
	fencedText := "```json\n" + string(responseJSON) + "\n```"

	var capturedAuth string
	var capturedPath string
	var capturedBody map[string]any

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedPath = r.URL.Path

		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &capturedBody)

		compatResp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": fencedText,
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(compatResp)
	}))
	defer server.Close()

	provider := NewCompatProvider(server.URL+"/v1", "compat-secret-key",
		WithHTTPClient(server.Client()),
	)

	if provider.Name() != ProviderCompat {
		t.Errorf("expected provider name %s, got %s", ProviderCompat, provider.Name())
	}

	req := sampleRequest()
	resp, err := provider.GenerateTailoredResume(context.Background(), req)
	if err != nil {
		t.Fatalf("unexpected error from compat provider: %v", err)
	}

	// Verify authorization header
	if capturedAuth != "Bearer compat-secret-key" {
		t.Errorf("expected Authorization 'Bearer compat-secret-key', got %q", capturedAuth)
	}

	// Verify URL path was appended correctly
	if capturedPath != "/v1/chat/completions" {
		t.Errorf("expected path /v1/chat/completions, got %s", capturedPath)
	}

	// Verify response_format json_object
	respFormat, ok := capturedBody["response_format"].(map[string]any)
	if !ok || respFormat["type"] != "json_object" {
		t.Errorf("expected response_format type json_object, got %v", respFormat)
	}

	// Verify system prompt embedded json schema
	messages, ok := capturedBody["messages"].([]any)
	if !ok || len(messages) < 2 {
		t.Fatalf("expected at least 2 messages")
	}
	sysMsg, _ := messages[0].(map[string]any)
	sysContent, _ := sysMsg["content"].(string)
	if !strings.Contains(sysContent, "<json_output_schema>") {
		t.Errorf("system prompt missing embedded json schema for open-weight model")
	}

	// Verify unmarshaling after fence stripping
	if resp.TailoredContent.Summary != sampleResponse.TailoredContent.Summary {
		t.Errorf("summary mismatch after stripping code fences")
	}
}

func TestCompatProvider_WithoutAuth(t *testing.T) {
	responseJSON, _ := json.Marshal(sampleResponse)
	var capturedAuth string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		compatResp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": string(responseJSON),
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(compatResp)
	}))
	defer server.Close()

	// Empty API key (e.g. local Ollama)
	provider := NewCompatProvider(server.URL, "", WithHTTPClient(server.Client()))
	_, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if capturedAuth != "" {
		t.Errorf("expected no Authorization header when key is empty, got %q", capturedAuth)
	}
}

// -----------------------------------------------------------------------------
// 7. Registry & Resolution Hierarchy Tests
// -----------------------------------------------------------------------------

type dummyProvider struct {
	name string
}

func (d *dummyProvider) Name() string { return d.name }
func (d *dummyProvider) GenerateTailoredResume(_ context.Context, _ domain.GenerationRequest) (*domain.GenerationResponse, error) {
	return &sampleResponse, nil
}

func TestRegistry_PrecedenceResolution(t *testing.T) {
	gemini := &dummyProvider{name: "gemini"}
	openai := &dummyProvider{name: "openai"}
	anthropic := &dummyProvider{name: "anthropic"}
	compat := &dummyProvider{name: "compat"}

	reg := &Registry{
		providers: map[string]domain.LLMProvider{
			"gemini":    gemini,
			"openai":    openai,
			"anthropic": anthropic,
			"compat":    compat,
		},
		defaultProvider: "gemini",
		defaultModel:    "gemini-3.8-flash",
	}

	// Case 1: GenerationRequest Overrides (Model + Branch ProviderOverride)
	req1 := domain.GenerationRequest{
		Model: "gpt-4o-custom",
		BranchContext: domain.Branch{
			ProviderOverride: "openai",
			ModelOverride:    "branch-model-ignored",
		},
	}
	p1, m1, err := reg.Resolve(req1, &domain.Preset{ProviderOverride: "anthropic", ModelOverride: "claude-ignored"}, &domain.Profile{DefaultProvider: "gemini", DefaultModel: "gemini-ignored"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p1.Name() != "openai" || m1 != "gpt-4o-custom" {
		t.Errorf("expected openai / gpt-4o-custom, got %s / %s", p1.Name(), m1)
	}

	// Case 2: Branch Overrides (when req.Model is empty)
	req2 := domain.GenerationRequest{
		BranchContext: domain.Branch{
			ProviderOverride: "anthropic",
			ModelOverride:    "claude-3-7-sonnet-20250219",
		},
	}
	p2, m2, err := reg.Resolve(req2, &domain.Preset{ProviderOverride: "openai", ModelOverride: "gpt-ignored"}, &domain.Profile{DefaultProvider: "gemini", DefaultModel: "gemini-ignored"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p2.Name() != "anthropic" || m2 != "claude-3-7-sonnet-20250219" {
		t.Errorf("expected anthropic / claude-3-7-sonnet-20250219, got %s / %s", p2.Name(), m2)
	}

	// Case 3: Preset Overrides (when branch has no overrides)
	req3 := domain.GenerationRequest{BranchContext: domain.Branch{}}
	preset3 := &domain.Preset{ProviderOverride: "compat", ModelOverride: "llama3.3:70b"}
	profile3 := &domain.Profile{DefaultProvider: "gemini", DefaultModel: "gemini-ignored"}
	p3, m3, err := reg.Resolve(req3, preset3, profile3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p3.Name() != "compat" || m3 != "llama3.3:70b" {
		t.Errorf("expected compat / llama3.3:70b, got %s / %s", p3.Name(), m3)
	}

	// Case 4: Master Profile Defaults (when preset has no overrides)
	req4 := domain.GenerationRequest{BranchContext: domain.Branch{}}
	profile4 := &domain.Profile{DefaultProvider: "openai", DefaultModel: "gpt-4o-mini"}
	p4, m4, err := reg.Resolve(req4, nil, profile4)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p4.Name() != "openai" || m4 != "gpt-4o-mini" {
		t.Errorf("expected openai / gpt-4o-mini, got %s / %s", p4.Name(), m4)
	}

	// Case 5: System Fallback (when profile and request are unconfigured)
	req5 := domain.GenerationRequest{BranchContext: domain.Branch{}}
	p5, m5, err := reg.Resolve(req5, nil, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p5.Name() != "gemini" || m5 != "gemini-3.8-flash" {
		t.Errorf("expected system default gemini / gemini-3.8-flash, got %s / %s", p5.Name(), m5)
	}
}

func TestRegistry_AliasesAndErrors(t *testing.T) {
	compat := &dummyProvider{name: "compat"}
	reg := &Registry{
		providers: map[string]domain.LLMProvider{
			"compat": compat,
		},
	}

	// Test openai-compat alias resolution
	p, err := reg.Get("openai-compat")
	if err != nil {
		t.Fatalf("failed to resolve openai-compat alias: %v", err)
	}
	if p.Name() != "compat" {
		t.Errorf("expected compat provider, got %s", p.Name())
	}

	// Test unknown provider error
	_, err = reg.Get("nonexistent")
	if !errors.Is(err, ErrProviderNotFound) {
		t.Errorf("expected ErrProviderNotFound, got %v", err)
	}

	// Test empty registry error
	emptyReg := NewRegistry(nil)
	_, _, err = emptyReg.Resolve(domain.GenerationRequest{}, nil, nil)
	if !errors.Is(err, ErrNoProviders) {
		t.Errorf("expected ErrNoProviders, got %v", err)
	}
}

func TestNewRegistry_FromConfig(t *testing.T) {
	cfg := &config.Config{
		GeminiAPIKey:        "gemini-key",
		OpenAIAPIKey:        "openai-key",
		AnthropicAPIKey:     "anthropic-key",
		OpenAICompatBaseURL: "http://localhost:11434/v1",
		OpenAICompatAPIKey:  "compat-key",
	}

	reg := NewRegistry(cfg)
	list := reg.List()

	if len(list) != 4 {
		t.Fatalf("expected 4 registered providers, got %d (%v)", len(list), list)
	}
	if reg.DefaultProvider() != "gemini" {
		t.Errorf("expected default provider gemini, got %s", reg.DefaultProvider())
	}
}

func TestExtractJSON_Resilience(t *testing.T) {
	expectedJSON := `{"tailored_content":{"summary":"test"}}`

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Clean JSON",
			input:    `{"tailored_content":{"summary":"test"}}`,
			expected: expectedJSON,
		},
		{
			name:     "Markdown fenced json",
			input:    "```json\n" + expectedJSON + "\n```",
			expected: expectedJSON,
		},
		{
			name:     "Markdown fenced uppercase JSON",
			input:    "```JSON\n" + expectedJSON + "\n```",
			expected: expectedJSON,
		},
		{
			name:     "Markdown fenced without tag",
			input:    "```\n" + expectedJSON + "\n```",
			expected: expectedJSON,
		},
		{
			name:     "Conversational preamble and postscript with fences",
			input:    "Here is the tailored resume:\n```json\n" + expectedJSON + "\n```\nHope this helps!",
			expected: expectedJSON,
		},
		{
			name:     "Conversational wrapper without fences",
			input:    "Sure, here is your JSON payload: " + expectedJSON + " - please verify.",
			expected: expectedJSON,
		},
		{
			name:     "Preamble with braces before fenced JSON",
			input:    "For role {staff engineer}:\n```json\n" + expectedJSON + "\n```\nDone.",
			expected: expectedJSON,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractJSON(tc.input)
			if got != tc.expected {
				t.Errorf("ExtractJSON() = %q, want %q", got, tc.expected)
			}
		})
	}
}

func TestCompatProvider_SuccessWithConversationalPreamble(t *testing.T) {
	responseJSON, err := json.Marshal(sampleResponse)
	if err != nil {
		t.Fatalf("failed to marshal sample response: %v", err)
	}

	conversationalText := "Here is your requested resume JSON:\n```json\n" + string(responseJSON) + "\n```\nLet me know if you need any adjustments!"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		compatResp := map[string]any{
			"choices": []map[string]any{
				{
					"message": map[string]any{
						"role":    "assistant",
						"content": conversationalText,
					},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(compatResp)
	}))
	defer server.Close()

	provider := NewCompatProvider(server.URL, "test-key", WithHTTPClient(server.Client()))
	resp, err := provider.GenerateTailoredResume(context.Background(), sampleRequest())
	if err != nil {
		t.Fatalf("GenerateTailoredResume failed on conversational preamble: %v", err)
	}

	if resp.TailoredContent.Summary != sampleResponse.TailoredContent.Summary {
		t.Errorf("summary mismatch: got %q, want %q", resp.TailoredContent.Summary, sampleResponse.TailoredContent.Summary)
	}
}

func TestRegistry_PresetOverridePropagationThroughGenerationRequest(t *testing.T) {
	anthropic := &dummyProvider{name: "anthropic"}
	gemini := &dummyProvider{name: "gemini"}

	reg := &Registry{
		providers: map[string]domain.LLMProvider{
			"anthropic": anthropic,
			"gemini":    gemini,
		},
		defaultProvider: "gemini",
		defaultModel:    "gemini-3.8-flash",
	}

	// Request carries Preset with provider and model overrides, while preset parameter to Resolve is nil
	req := domain.GenerationRequest{
		BranchContext: domain.Branch{}, // no branch overrides
		Preset: &domain.Preset{
			ProviderOverride: "anthropic",
			ModelOverride:    "claude-3-7-sonnet-20250219",
		},
	}
	profile := &domain.Profile{
		DefaultProvider: "gemini",
		DefaultModel:    "gemini-pro-default",
	}

	provider, model, err := reg.Resolve(req, nil, profile)
	if err != nil {
		t.Fatalf("Resolve failed: %v", err)
	}

	if provider.Name() != "anthropic" {
		t.Errorf("expected provider 'anthropic' from req.Preset, got %s", provider.Name())
	}
	if model != "claude-3-7-sonnet-20250219" {
		t.Errorf("expected model 'claude-3-7-sonnet-20250219' from req.Preset, got %s", model)
	}
}

func TestRegistry_AvailableModels_FilterMatrix(t *testing.T) {
	tests := []struct {
		name              string
		geminiKey         string
		openaiKey         string
		anthropicKey      string
		compatURL         string
		expectedProviders []string
	}{
		{
			name:              "no keys configured",
			expectedProviders: []string{},
		},
		{
			name:              "only gemini configured",
			geminiKey:         "test-gemini-key",
			expectedProviders: []string{"gemini"},
		},
		{
			name:              "gemini and anthropic",
			geminiKey:         "test-gemini-key",
			anthropicKey:      "test-anthropic-key",
			expectedProviders: []string{"anthropic", "gemini"},
		},
		{
			name:              "all providers configured",
			geminiKey:         "secret-g-key",
			openaiKey:         "secret-o-key",
			anthropicKey:      "secret-a-key",
			compatURL:         "http://localhost:11434/v1",
			expectedProviders: []string{"anthropic", "compat", "gemini", "openai"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{
				GeminiAPIKey:        tc.geminiKey,
				OpenAIAPIKey:        tc.openaiKey,
				AnthropicAPIKey:     tc.anthropicKey,
				OpenAICompatBaseURL: tc.compatURL,
			}

			reg := NewRegistry(cfg)
			models := reg.AvailableModels()

			providerMap := make(map[string]bool)
			for _, m := range models {
				providerMap[m.Provider] = true
			}

			var providers []string
			for p := range providerMap {
				providers = append(providers, p)
			}
			sort.Strings(providers)

			if len(providers) != len(tc.expectedProviders) {
				t.Fatalf("provider count mismatch: got %v, want %v", providers, tc.expectedProviders)
			}
			for i, p := range providers {
				if p != tc.expectedProviders[i] {
					t.Errorf("provider[%d] mismatch: got %s, want %s", i, p, tc.expectedProviders[i])
				}
			}

			// Verify Gemini models include up-to-date versions if gemini is configured
			if tc.geminiKey != "" {
				var geminiModels []string
				for _, m := range models {
					if m.Provider == "gemini" {
						geminiModels = append(geminiModels, m.Model)
					}
				}
				expectedGemini := []string{
					"gemini-4-argon",
					"gemini-3.1-pro-preview",
					"gemini-3.1-pro-preview-customtools",
					"gemini-3.8-flash",
					"gemini-3.6-flash",
					"gemini-flash-latest",
					"gemini-3.8-flash-lite",
					"gemini-3.5-flash-lite",
					"gemini-flash-lite-latest",
					"gemini-3.8-live",
					"gemini-3.8-live-extended-thinking",
					"gemini-3.8-flash-tts",
					"gemini-3.8-flash-lite-tts",
					"gemini-2.5-pro",
					"gemini-2.5-flash",
					"gemini-2.5-flash-lite",
					"gemini-embedding-2",
					"gemini-embedding-001",
					"text-embedding-004",
					"imagen-3.0-generate-002",
				}
				for _, eg := range expectedGemini {
					found := false
					for _, gm := range geminiModels {
						if gm == eg {
							found = true
							break
						}
					}
					if !found {
						t.Errorf("expected Gemini model %q in available models, got %v", eg, geminiModels)
					}
				}
			}

			// Security verification: verify secret keys are never leaked in models list
			for _, m := range models {
				for _, secret := range []string{tc.geminiKey, tc.openaiKey, tc.anthropicKey} {
					if secret != "" {
						if strings.Contains(m.DisplayName, secret) || strings.Contains(m.Model, secret) || strings.Contains(m.Provider, secret) {
							t.Fatalf("SECURITY VIOLATION: secret %q leaked in model option: %+v", secret, m)
						}
					}
				}
			}
		})
	}
}

// -----------------------------------------------------------------------------
// Gemini Schema Sanitizer & Error Parsing Tests (Issue #67)
// -----------------------------------------------------------------------------

func TestSanitizeGeminiSchema(t *testing.T) {
	t.Run("CanonicalGenerationResponseSchema", func(t *testing.T) {
		orig := GenerationResponseSchema()
		sanitized := SanitizeGeminiSchema(orig)

		// Assert no "additionalProperties" anywhere in sanitized
		assertNoAdditionalProperties(t, sanitized, "root")

		// Assert required, properties, items, type are intact
		if sanitized["type"] != "object" {
			t.Fatalf("expected type object, got %v", sanitized["type"])
		}
		props, ok := sanitized["properties"].(map[string]any)
		if !ok {
			t.Fatalf("expected properties map")
		}
		if _, ok := props["tailored_content"]; !ok {
			t.Errorf("missing tailored_content property")
		}
		if _, ok := props["gap_report"]; !ok {
			t.Errorf("missing gap_report property")
		}
	})

	t.Run("DeeplyNestedObjects", func(t *testing.T) {
		input := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"level1": map[string]any{
					"type":                 "object",
					"additionalProperties": false,
					"properties": map[string]any{
						"level2": map[string]any{
							"type":                 "object",
							"additionalProperties": false,
							"properties": map[string]any{
								"name": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
		}

		sanitized := SanitizeGeminiSchema(input)
		assertNoAdditionalProperties(t, sanitized, "root")
	})

	t.Run("ArrayOfObjects", func(t *testing.T) {
		input := map[string]any{
			"type": "array",
			"items": map[string]any{
				"type":                 "object",
				"additionalProperties": false,
				"properties": map[string]any{
					"id": map[string]any{"type": "string"},
				},
			},
		}

		sanitized := SanitizeGeminiSchema(input)
		assertNoAdditionalProperties(t, sanitized, "root")
		items, ok := sanitized["items"].(map[string]any)
		if !ok {
			t.Fatalf("expected items to be map[string]any")
		}
		if items["type"] != "object" {
			t.Errorf("expected items type object, got %v", items["type"])
		}
	})

	t.Run("UnsupportedKeywordsRemoval", func(t *testing.T) {
		input := map[string]any{
			"$schema":              "http://json-schema.org/draft-07/schema#",
			"$defs":                map[string]any{"def1": map[string]any{"type": "string"}},
			"definitions":          map[string]any{"def2": map[string]any{"type": "string"}},
			"title":                "ResumeGenerationResponse",
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"summary": map[string]any{
					"type":  "string",
					"title": "SummaryTitle",
				},
			},
		}

		sanitized := SanitizeGeminiSchema(input)
		for _, forbidden := range []string{"$schema", "$defs", "definitions", "title", "additionalProperties"} {
			if _, ok := sanitized[forbidden]; ok {
				t.Errorf("expected %q to be removed from root", forbidden)
			}
		}
		props := sanitized["properties"].(map[string]any)
		summary := props["summary"].(map[string]any)
		if _, ok := summary["title"]; ok {
			t.Errorf("expected title to be removed from nested property")
		}
	})

	t.Run("EmptySchema", func(t *testing.T) {
		input := map[string]any{}
		sanitized := SanitizeGeminiSchema(input)
		if sanitized == nil || len(sanitized) != 0 {
			t.Errorf("expected empty map, got %v", sanitized)
		}
	})

	t.Run("NilSchema", func(t *testing.T) {
		sanitized := SanitizeGeminiSchema(nil)
		if sanitized != nil {
			t.Errorf("expected nil for nil input, got %v", sanitized)
		}
	})

	t.Run("SchemaWithoutForbiddenKeys", func(t *testing.T) {
		input := map[string]any{
			"type":        "string",
			"description": "simple string",
		}
		sanitized := SanitizeGeminiSchema(input)
		if sanitized["type"] != "string" || sanitized["description"] != "simple string" {
			t.Errorf("expected unmodified fields, got %v", sanitized)
		}
	})

	t.Run("OriginalSchemaImmutability", func(t *testing.T) {
		input := map[string]any{
			"type":                 "object",
			"additionalProperties": false,
			"properties": map[string]any{
				"sub": map[string]any{
					"additionalProperties": false,
				},
			},
		}
		_ = SanitizeGeminiSchema(input)

		if input["additionalProperties"] != false {
			t.Errorf("original root schema was mutated!")
		}
		sub := input["properties"].(map[string]any)["sub"].(map[string]any)
		if sub["additionalProperties"] != false {
			t.Errorf("original nested schema was mutated!")
		}
	})

	t.Run("GeminiResponseSchema_Helper", func(t *testing.T) {
		schema := GeminiResponseSchema()
		assertNoAdditionalProperties(t, schema, "root")
		if schema["type"] != "object" {
			t.Errorf("expected type object, got %v", schema["type"])
		}
	})
}

func assertNoAdditionalProperties(t *testing.T, node any, path string) {
	t.Helper()
	switch v := node.(type) {
	case map[string]any:
		if _, ok := v["additionalProperties"]; ok {
			t.Errorf("forbidden additionalProperties found at %s", path)
		}
		for k, child := range v {
			assertNoAdditionalProperties(t, child, path+"."+k)
		}
	case []any:
		for i, child := range v {
			assertNoAdditionalProperties(t, child, fmt.Sprintf("%s[%d]", path, i))
		}
	}
}

func TestGeminiProvider_ErrorParsing(t *testing.T) {
	testCases := []struct {
		name        string
		statusCode  int
		errorBody   string
		expectedErr error
		mustContain string
	}{
		{
			name:       "HTTP400_RepeatingPayloadError",
			statusCode: 400,
			errorBody: `{
  "error": {
    "code": 400,
    "message": "Invalid JSON payload received. Unknown name \"additionalProperties\" at 'generation_config.response_schema': Cannot find field.\nInvalid JSON payload received. Unknown name \"additionalProperties\" at 'generation_config.response_schema': Cannot find field.\nInvalid JSON payload received. Unknown name \"additionalProperties\" at 'generation_config.response_schema': Cannot find field.",
    "status": "INVALID_ARGUMENT"
  }
}`,
			expectedErr: ErrBadRequest,
			mustContain: `Invalid JSON payload received. Unknown name "additionalProperties" at 'generation_config.response_schema': Cannot find field.`,
		},
		{
			name:        "HTTP400_StandardMessage",
			statusCode:  400,
			errorBody:   `{"error": {"code": 400, "message": "Invalid field value", "status": "INVALID_ARGUMENT"}}`,
			expectedErr: ErrBadRequest,
			mustContain: "Invalid field value",
		},
		{
			name:        "HTTP401_Unauthorized",
			statusCode:  401,
			errorBody:   `{"error": {"code": 401, "message": "API key not valid"}}`,
			expectedErr: ErrUnauthorized,
			mustContain: "API key not valid",
		},
		{
			name:        "HTTP429_RateLimited",
			statusCode:  429,
			errorBody:   `{"error": {"code": 429, "message": "Resource exhausted"}}`,
			expectedErr: ErrRateLimited,
			mustContain: "Resource exhausted",
		},
		{
			name:        "HTTP500_UpstreamFailure",
			statusCode:  500,
			errorBody:   `{"error": {"code": 500, "message": "Internal server crash"}}`,
			expectedErr: ErrUpstream,
			mustContain: "Internal server crash",
		},
		{
			name:        "MalformedJSON_Body",
			statusCode:  400,
			errorBody:   `Bad Request: cannot parse payload`,
			expectedErr: ErrBadRequest,
			mustContain: "Bad Request: cannot parse payload",
		},
		{
			name:        "EmptyBody",
			statusCode:  400,
			errorBody:   ``,
			expectedErr: ErrBadRequest,
			mustContain: "gemini api returned status 400",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := parseGeminiError(tc.statusCode, []byte(tc.errorBody))
			if err == nil {
				t.Fatalf("expected error, got nil")
			}
			if tc.expectedErr != nil && !errors.Is(err, tc.expectedErr) {
				t.Errorf("expected error to wrap %v, got %v", tc.expectedErr, err)
			}
			if tc.mustContain != "" && !strings.Contains(err.Error(), tc.mustContain) {
				t.Errorf("expected error %q to contain %q", err.Error(), tc.mustContain)
			}
			if tc.name == "HTTP400_RepeatingPayloadError" {
				// Assert that the message appears exactly once (deduplicated)
				count := strings.Count(err.Error(), `Invalid JSON payload received. Unknown name "additionalProperties"`)
				if count != 1 {
					t.Errorf("expected repeating lines to be deduplicated to 1 occurrence, found %d", count)
				}
			}
		})
	}
}

func TestReadBoundedBody(t *testing.T) {
	t.Run("within bounds", func(t *testing.T) {
		payload := strings.Repeat("a", 1024)
		data, err := readBoundedBody(strings.NewReader(payload))
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(data) != 1024 {
			t.Errorf("expected 1024 bytes, got %d", len(data))
		}
	})

	t.Run("truncates at max boundary", func(t *testing.T) {
		// Create a reader larger than maxLLMResponseBodyBytes
		oversized := io.MultiReader(
			strings.NewReader(strings.Repeat("b", maxLLMResponseBodyBytes)),
			strings.NewReader("overflow-bytes-that-should-not-be-read"),
		)
		data, err := readBoundedBody(oversized)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(data) != maxLLMResponseBodyBytes {
			t.Errorf("expected exactly %d bytes, got %d", maxLLMResponseBodyBytes, len(data))
		}
	})
}

func TestCheckStatusError(t *testing.T) {
	testCases := []struct {
		statusCode  int
		expectedErr error
	}{
		{http.StatusOK, nil},
		{http.StatusUnauthorized, ErrUnauthorized},
		{http.StatusForbidden, ErrUnauthorized},
		{http.StatusTooManyRequests, ErrRateLimited},
		{http.StatusInternalServerError, ErrUpstream},
		{http.StatusBadGateway, ErrUpstream},
		{http.StatusServiceUnavailable, ErrUpstream},
		{http.StatusTeapot, nil},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("status_%d", tc.statusCode), func(t *testing.T) {
			err := checkStatusError("test-provider", tc.statusCode, []byte("error payload"))
			if tc.statusCode == http.StatusOK {
				if err != nil {
					t.Fatalf("expected nil error for 200 OK, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for status %d, got nil", tc.statusCode)
			}
			if tc.expectedErr != nil && !errors.Is(err, tc.expectedErr) {
				t.Errorf("expected error to wrap %v, got %v", tc.expectedErr, err)
			}
		})
	}
}

func TestLLMTimeoutOption(t *testing.T) {
	t.Run("Default HTTP client timeout is 10 minutes", func(t *testing.T) {
		opts := applyOptions(nil, "", "")
		if opts.httpClient.Timeout != 10*time.Minute {
			t.Errorf("expected default timeout 10m, got %v", opts.httpClient.Timeout)
		}
	})

	t.Run("WithTimeout option overrides client timeout", func(t *testing.T) {
		opts := applyOptions([]Option{WithTimeout(5 * time.Minute)}, "", "")
		if opts.httpClient.Timeout != 5*time.Minute {
			t.Errorf("expected overridden timeout 5m, got %v", opts.httpClient.Timeout)
		}
	})

	t.Run("NewRegistry configures providers with cfg.LLMTimeout", func(t *testing.T) {
		cfg := &config.Config{
			OpenAICompatBaseURL: "http://localhost:11434/v1",
			LLMTimeout:          7 * time.Minute,
		}
		reg := NewRegistry(cfg)
		p, err := reg.Get("compat")
		if err != nil {
			t.Fatalf("failed to get compat provider: %v", err)
		}
		cp, ok := p.(*CompatProvider)
		if !ok {
			t.Fatalf("expected *CompatProvider, got %T", p)
		}
		if cp.httpClient.Timeout != 7*time.Minute {
			t.Errorf("expected 7m timeout on provider client, got %v", cp.httpClient.Timeout)
		}
	})

	t.Run("NewRegistry preserves LLMTimeout when LLMDebugPrompts is enabled", func(t *testing.T) {
		tempDir := t.TempDir()
		cfg := &config.Config{
			OpenAICompatBaseURL: "http://localhost:11434/v1",
			LLMTimeout:          8 * time.Minute,
			LLMDebugPrompts:     true,
			LLMDebugDir:         tempDir,
		}
		reg := NewRegistry(cfg)
		p, err := reg.Get("compat")
		if err != nil {
			t.Fatalf("failed to get compat provider: %v", err)
		}
		cp, ok := p.(*CompatProvider)
		if !ok {
			t.Fatalf("expected *CompatProvider, got %T", p)
		}
		if cp.httpClient.Timeout != 8*time.Minute {
			t.Errorf("expected 8m timeout on provider client with debug prompts enabled, got %v", cp.httpClient.Timeout)
		}
	})
}

func TestGapAnalysisResponseSchema(t *testing.T) {
	schema := GapAnalysisResponseSchema()
	if schema["type"] != "object" {
		t.Fatalf("expected schema type object, got %v", schema["type"])
	}
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %T", schema["properties"])
	}
	gapReportProp, ok := props["gap_report"].(map[string]any)
	if !ok {
		t.Fatalf("expected gap_report property, got %v", props)
	}
	grProps, ok := gapReportProp["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected gap_report properties map")
	}
	if _, ok := grProps["overall_match_summary"]; !ok {
		t.Errorf("missing overall_match_summary in schema")
	}
	if _, ok := grProps["missing_requirements"]; !ok {
		t.Errorf("missing missing_requirements in schema")
	}
}

func TestBuildGapAnalysisPrompt(t *testing.T) {
	req := sampleRequest()
	req.BranchContext = domain.Branch{
		TargetRole:         "Staff Distributed Systems Engineer",
		TargetCompany:      "CloudScale Labs",
		TargetRequirements: "Must have deep expertise in Raft, Paxos, and Go.",
	}

	payload, err := BuildGapAnalysisPrompt(req)
	if err != nil {
		t.Fatalf("BuildGapAnalysisPrompt returned error: %v", err)
	}
	if payload == nil {
		t.Fatalf("expected non-nil PromptPayload")
	}

	if !strings.Contains(payload.SystemPrompt, "Resumeee AI") {
		t.Errorf("expected system prompt to contain Resumeee AI intro")
	}
	if !strings.Contains(payload.SystemPrompt, "Anti-Hallucination") {
		t.Errorf("expected system prompt to contain Anti-Hallucination rules")
	}
	if !strings.Contains(payload.UserPrompt, "Staff Distributed Systems Engineer") {
		t.Errorf("expected user prompt to contain target role")
	}
	if !strings.Contains(payload.UserPrompt, "CloudScale Labs") {
		t.Errorf("expected user prompt to contain target company")
	}
	if !strings.Contains(payload.UserPrompt, "Must have deep expertise in Raft") {
		t.Errorf("expected user prompt to contain target requirements")
	}
	if !strings.Contains(payload.UserPrompt, "<master_profile_data>") {
		t.Errorf("expected user prompt to contain master profile data")
	}
}
