package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

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
	logger       *slog.Logger
}

// NewGeminiProvider creates a new Google Gemini adapter with the specified API key.
func NewGeminiProvider(apiKey string, opts ...Option) *GeminiProvider {
	cfg := applyOptions(opts, defaultGeminiBaseURL, defaultGeminiModel)
	return &GeminiProvider{
		apiKey:       apiKey,
		baseURL:      cfg.baseURL,
		httpClient:   cfg.httpClient,
		defaultModel: cfg.defaultModel,
		logger:       cfg.logger,
	}
}

// Name returns the provider identifier.
func (p *GeminiProvider) Name() string {
	return ProviderGemini
}

// GenerateTailoredResume submits the prompt to Google Gemini and parses the structured response.
func (p *GeminiProvider) GenerateTailoredResume(ctx context.Context, req domain.GenerationRequest) (genResp *domain.GenerationResponse, retErr error) {
	prompts, err := BuildPrompt(req)
	if err != nil {
		return nil, fmt.Errorf("failed to build prompt: %w", err)
	}

	model := req.Model
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	logLLMRequest(ctx, p.logger, p.Name(), model, "GenerateTailoredResume", prompts.SystemPrompt, prompts.UserPrompt)

	start := time.Now()
	var statusCode int
	var rawResp string
	defer func() {
		logLLMResponse(ctx, p.logger, p.Name(), model, "GenerateTailoredResume", time.Since(start), statusCode, rawResp, retErr)
	}()

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
		retErr = fmt.Errorf("failed to marshal gemini request: %w", err)
		return nil, retErr
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", strings.TrimRight(p.baseURL, "/"), model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		retErr = fmt.Errorf("failed to create http request: %w", err)
		return nil, retErr
	}

	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		retErr = fmt.Errorf("gemini http request failed: %w", err)
		return nil, retErr
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		retErr = fmt.Errorf("failed to read response body: %w", err)
		return nil, retErr
	}

	statusCode = resp.StatusCode
	rawResp = string(bodyBytes)

	if resp.StatusCode != http.StatusOK {
		retErr = parseGeminiError(resp.StatusCode, bodyBytes)
		return nil, retErr
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
		retErr = fmt.Errorf("%w: malformed gemini response: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		retErr = fmt.Errorf("%w: no candidates returned by gemini", ErrInvalidResponse)
		return nil, retErr
	}

	rawText := strings.TrimSpace(geminiResp.Candidates[0].Content.Parts[0].Text)
	cleanJSON := stripCodeFences(rawText)

	var parsed domain.GenerationResponse
	if err := json.Unmarshal([]byte(cleanJSON), &parsed); err != nil {
		retErr = fmt.Errorf("%w: failed to unmarshal generation response: %v", ErrInvalidResponse, err)
		return nil, retErr
	}

	return &parsed, nil
}

// CompleteText implements RawCompleter for Google Gemini.
func (p *GeminiProvider) CompleteText(ctx context.Context, systemPrompt, userPrompt, model string) (text string, retErr error) {
	if strings.TrimSpace(model) == "" {
		model = p.defaultModel
	}

	logLLMRequest(ctx, p.logger, p.Name(), model, "CompleteText", systemPrompt, userPrompt)

	start := time.Now()
	var statusCode int
	var rawResp string
	defer func() {
		logLLMResponse(ctx, p.logger, p.Name(), model, "CompleteText", time.Since(start), statusCode, rawResp, retErr)
	}()

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
		retErr = fmt.Errorf("failed to marshal gemini request: %w", err)
		return "", retErr
	}

	url := fmt.Sprintf("%s/v1beta/models/%s:generateContent", strings.TrimRight(p.baseURL, "/"), model)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(reqBodyBytes))
	if err != nil {
		retErr = fmt.Errorf("failed to create http request: %w", err)
		return "", retErr
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", p.apiKey)

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		retErr = fmt.Errorf("gemini http request failed: %w", err)
		return "", retErr
	}
	defer func() { _ = resp.Body.Close() }()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		retErr = fmt.Errorf("failed to read response body: %w", err)
		return "", retErr
	}

	statusCode = resp.StatusCode
	rawResp = string(bodyBytes)

	if resp.StatusCode != http.StatusOK {
		retErr = parseGeminiError(resp.StatusCode, bodyBytes)
		return "", retErr
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
		retErr = fmt.Errorf("%w: malformed gemini response: %v", ErrInvalidResponse, err)
		return "", retErr
	}

	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		retErr = fmt.Errorf("%w: no candidates returned by gemini", ErrInvalidResponse)
		return "", retErr
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
