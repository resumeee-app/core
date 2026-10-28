package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"resumeee/internal/domain"
)

const (
	defaultGeminiBaseURL = "https://generativelanguage.googleapis.com"
	defaultGeminiModel   = "gemini-3.8-flash"
	ProviderGemini       = "gemini"
)

// GeminiProvider implements domain.LLMProvider for Google Gemini models.
type GeminiProvider struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	defaultModel string
}

// NewGeminiProvider creates a new Google Gemini adapter with the specified API key.
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

// GenerateTailoredResume submits the prompt to Google Gemini and parses the structured response.
func (p *GeminiProvider) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (*domain.GenerationResponse, error) {
	prompts, err := BuildPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Role  string `json:"role,omitempty"`
		Parts []part `json:"parts"`
	}
	type generationConfig struct {
		ResponseMimeType string         `json:"responseMimeType"`
		ResponseSchema   map[string]any `json:"responseSchema"`
	}
	type requestBody struct {
		SystemInstruction *content         `json:"systemInstruction,omitempty"`
		Contents          []content        `json:"contents"`
		GenerationConfig  generationConfig `json:"generationConfig"`
	}

	payload := requestBody{
		SystemInstruction: &content{
			Parts: []part{{Text: prompts.SystemPrompt}},
		},
		Contents: []content{
			{
				Role:  "user",
				Parts: []part{{Text: prompts.UserPrompt}},
			},
		},
		GenerationConfig: generationConfig{
			ResponseMimeType: "application/json",
			ResponseSchema:   GeminiResponseSchema(),
		},
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

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, parseGeminiError(resp.StatusCode, bodyBytes)
	}

	type geminiResponse struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	var geminiResp geminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return nil, fmt.Errorf("%w: malformed gemini response: %v", ErrInvalidResponse, err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("%w: no candidates returned by gemini", ErrInvalidResponse)
	}

	rawText := strings.TrimSpace(geminiResp.Candidates[0].Content.Parts[0].Text)
	cleanJSON := stripCodeFences(rawText)

	var genResp domain.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &genResp); err != nil {
		return nil, fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
	}

	return &genResp, nil
}

// CompleteText implements RawCompleter for Google Gemini.
func (p *GeminiProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (string, error) {
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	type part struct {
		Text string `json:"text"`
	}
	type content struct {
		Role  string `json:"role,omitempty"`
		Parts []part `json:"parts"`
	}
	type systemInstruction struct {
		Parts []part `json:"parts"`
	}
	type geminiRequest struct {
		SystemInstruction systemInstruction `json:"system_instruction"`
		Contents          []content         `json:"contents"`
	}

	reqPayload := geminiRequest{
		SystemInstruction: systemInstruction{
			Parts: []part{{Text: systemPrompt}},
		},
		Contents: []content{
			{
				Role:  "user",
				Parts: []part{{Text: userPrompt}},
			},
		},
	}

	reqBodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal gemini request: %w", err)
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent?key=%s", strings.TrimRight(p.baseURL, "/"), model, p.apiKey)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create http request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return "", fmt.Errorf("gemini http request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", parseGeminiError(resp.StatusCode, bodyBytes)
	}

	type geminiResponse struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}

	var geminiResp geminiResponse
	if err := json.Unmarshal(bodyBytes, &geminiResp); err != nil {
		return "", fmt.Errorf("%w: malformed gemini response: %v", ErrInvalidResponse, err)
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("%w: no candidates returned by gemini", ErrInvalidResponse)
	}

	return strings.TrimSpace(geminiResp.Candidates[0].Content.Parts[0].Text), nil
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

	// Deduplicate lines in error message while preserving ordering
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

