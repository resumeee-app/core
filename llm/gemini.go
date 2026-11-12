package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/resumeee-app/resumeee-core"
)

const defaultGeminiBaseURL = "https://generativelanguage.googleapis.com"

// GeminiProvider implements core.LLMClient for Google Gemini models.
type GeminiProvider struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	defaultModel string
}

// NewGeminiProvider creates a new Google Gemini transport client with the specified API key.
func NewGeminiProvider(apiKey string, opts ...Option) *GeminiProvider {
	cfg := applyOptions(opts, defaultGeminiBaseURL, defaultGeminiModel)
	return &GeminiProvider{
		apiKey:       apiKey,
		baseURL:      cfg.baseURL,
		httpClient:   cfg.httpClient,
		defaultModel: cfg.defaultModel,
	}
}

// Name returns the provider identifier.
func (p *GeminiProvider) Name() string {
	return ProviderGemini
}

// DefaultModel returns the configured default model for Gemini.
func (p *GeminiProvider) DefaultModel() string {
	if strings.TrimSpace(p.defaultModel) != "" {
		return p.defaultModel
	}
	return defaultGeminiModel
}

// Models returns the default model option for Google Gemini.
func (p *GeminiProvider) Models() []core.ModelOption {
	def := p.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    ProviderGemini,
			Model:       def,
			DisplayName: fmt.Sprintf("Google Gemini — %s", def),
			IsDefault:   true,
		},
	}
}

// Complete executes a completion request against the Google Gemini Generative Language API.
func (p *GeminiProvider) Complete(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
	}

	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Role  string `json:"role,omitempty"`
		Parts []part `json:"parts"`
	}
	type generationConfig struct {
		ResponseMimeType string         `json:"responseMimeType,omitempty"`
		ResponseSchema   map[string]any `json:"responseSchema,omitempty"`
	}
	type requestBody struct {
		SystemInstruction *content          `json:"systemInstruction,omitempty"`
		Contents          []content         `json:"contents"`
		GenerationConfig  *generationConfig `json:"generationConfig,omitempty"`
	}

	payload := requestBody{
		SystemInstruction: &content{
			Parts: []part{{Text: req.SystemPrompt}},
		},
		Contents: []content{
			{
				Role:  "user",
				Parts: []part{{Text: req.UserPrompt}},
			},
		},
	}

	if req.ResponseSchema != nil {
		payload.GenerationConfig = &generationConfig{
			ResponseMimeType: "application/json",
			ResponseSchema:   SanitizeGeminiSchema(req.ResponseSchema),
		}
	}

	reqBodyBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal gemini request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", strings.TrimRight(p.baseURL, "/"), model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("gemini http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := readBoundedBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	rawBody := string(bodyBytes)
	if resp.StatusCode != http.StatusOK {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, parseGeminiError(resp.StatusCode, bodyBytes)
	}

	type geminiResponse struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
			TotalTokenCount      int `json:"totalTokenCount"`
		} `json:"usageMetadata"`
	}

	var geminiResp geminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: malformed gemini response: %v", ErrInvalidResponse, err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: resp.StatusCode,
		}, fmt.Errorf("%w: no candidates returned by gemini", ErrInvalidResponse)
	}

	usage := core.TokenUsage{
		PromptTokens:     geminiResp.UsageMetadata.PromptTokenCount,
		CompletionTokens: geminiResp.UsageMetadata.CandidatesTokenCount,
		TotalTokens:      geminiResp.UsageMetadata.TotalTokenCount,
	}
	if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
		usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
	}

	return &core.LLMCompletionResponse{
		Text:       strings.TrimSpace(geminiResp.Candidates[0].Content.Parts[0].Text),
		RawBody:    rawBody,
		StatusCode: resp.StatusCode,
		Usage:      usage,
	}, nil
}

// parseGeminiError parses HTTP error payloads from Google Gemini API, deduplicating repeated
// error lines in the response message and wrapping canonical sentinel errors.
func parseGeminiError(statusCode int, bodyBytes []byte) error {
	raw := strings.TrimSpace(string(bodyBytes))
	var msg string

	if len(raw) > 0 {
		var objErr struct {
			Error struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
				Status  string `json:"status"`
			} `json:"error"`
		}
		if err := json.Unmarshal(bodyBytes, &objErr); err == nil && objErr.Error.Message != "" {
			msg = objErr.Error.Message
		} else {
			var strErr struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(bodyBytes, &strErr); err == nil && strErr.Error != "" {
				msg = strErr.Error
			} else {
				msg = raw
			}
		}
	}

	var cleanMsg string
	if msg != "" {
		lines := strings.Split(msg, "\n")
		var deduped []string
		seen := make(map[string]bool)
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed == "" {
				continue
			}
			if !seen[trimmed] {
				seen[trimmed] = true
				deduped = append(deduped, trimmed)
			}
		}
		if len(deduped) > 0 {
			cleanMsg = strings.Join(deduped, "\n")
		} else {
			cleanMsg = strings.TrimSpace(msg)
		}
	}

	if cleanMsg == "" {
		cleanMsg = fmt.Sprintf("gemini api returned status %d", statusCode)
	}

	switch {
	case statusCode == http.StatusBadRequest:
		return fmt.Errorf("%w: %s", ErrBadRequest, cleanMsg)
	case statusCode == http.StatusUnauthorized || statusCode == http.StatusForbidden:
		return fmt.Errorf("%w: status %d: %s", ErrUnauthorized, statusCode, cleanMsg)
	case statusCode == http.StatusTooManyRequests:
		return fmt.Errorf("%w: status 429: %s", ErrRateLimited, cleanMsg)
	case statusCode >= 500:
		return fmt.Errorf("%w: status %d: %s", ErrUpstream, statusCode, cleanMsg)
	default:
		return fmt.Errorf("gemini api returned status %d: %s", statusCode, cleanMsg)
	}
}
