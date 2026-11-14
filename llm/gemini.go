package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/apierrors"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
	"google.golang.org/genai/interactions/retry"

	"github.com/resumeee-app/resumeee-core"
)

const defaultGeminiBaseURL = "https://generativelanguage.googleapis.com"

// GeminiProvider implements core.LLMClient for Google Gemini models using the official
// Google GenAI Interactions SDK (google.golang.org/genai).
type GeminiProvider struct {
	apiKey       string
	baseURL      string
	httpClient   *http.Client
	defaultModel string
	client       *genai.Client
	initErr      error
}

// NewGeminiProvider creates a new Google Gemini transport client with the specified API key.
func NewGeminiProvider(apiKey string, opts ...Option) *GeminiProvider {
	cfg := applyOptions(opts, defaultGeminiBaseURL, defaultGeminiModel)
	prov := &GeminiProvider{
		apiKey:       apiKey,
		baseURL:      cfg.baseURL,
		httpClient:   cfg.httpClient,
		defaultModel: cfg.defaultModel,
	}

	trimmedKey := strings.TrimSpace(apiKey)
	if trimmedKey != "" {
		client, err := genai.NewClient(context.Background(), &genai.ClientConfig{
			APIKey:     trimmedKey,
			Backend:    genai.BackendGeminiAPI,
			HTTPClient: wrapBoundedHTTPClient(cfg.httpClient),
			HTTPOptions: genai.HTTPOptions{
				BaseURL: strings.TrimRight(strings.TrimSpace(cfg.baseURL), "/"),
			},
		})
		prov.client = client
		prov.initErr = err
	}

	return prov
}

// Name returns the provider identifier.
func (p *GeminiProvider) Name() string {
	return ProviderGemini
}

// DefaultModel returns the configured default model for Gemini.
func (p *GeminiProvider) DefaultModel() string {
	if p != nil && strings.TrimSpace(p.defaultModel) != "" {
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

// SupportsStructuredOutput reports that Google Gemini supports native structured JSON outputs.
func (p *GeminiProvider) SupportsStructuredOutput(_ string) bool {
	return true
}

// Complete executes a completion request against the Google Gemini Interactions API using the official Go SDK.
func (p *GeminiProvider) Complete(ctx context.Context, req core.LLMCompletionRequest) (*core.LLMCompletionResponse, error) {
	if p == nil || strings.TrimSpace(p.apiKey) == "" {
		return nil, fmt.Errorf("%w: gemini api key is required", ErrUnauthorized)
	}
	if p.initErr != nil {
		return nil, fmt.Errorf("failed to initialize gemini client: %w", p.initErr)
	}
	if p.client == nil || p.client.Interactions == nil {
		return nil, fmt.Errorf("gemini interactions client is not initialized")
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = p.DefaultModel()
	}

	input := interactions.NewInteractionsInput(req.UserPrompt)
	modelInteraction := interactions.CreateModelInteraction{
		Model: interactions.Model(model),
		Input: &input,
	}

	if strings.TrimSpace(req.SystemPrompt) != "" {
		modelInteraction.SystemInstruction = genai.Ptr(req.SystemPrompt)
	}

	if thinkingLevel := mapGeminiThinkingLevel(model, req.ReasoningLevel); thinkingLevel != nil {
		modelInteraction.GenerationConfig = &interactions.GenerationConfig{
			ThinkingLevel: thinkingLevel,
		}
	}

	if req.ResponseSchema != nil {
		format := interactions.NewCreateModelInteractionResponseFormat(
			interactions.NewResponseFormat(interactions.TextResponseFormat{
				MimeType: interactions.TextResponseFormatMimeTypeApplicationJSON.ToPointer(),
				Schema:   SanitizeGeminiJSONSchema(req.ResponseSchema),
			}),
		)
		modelInteraction.ResponseFormat = &format
	}

	sdkReq := operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(modelInteraction),
	}
	res, err := p.client.Interactions.Create(
		ctx,
		sdkReq,
		operations.WithRetries(retry.Config{Strategy: "none"}),
	)
	if err != nil {
		return mapGeminiSDKError(err)
	}

	statusCode := http.StatusOK
	if res != nil {
		if httpRes := res.HTTPMeta.GetResponse(); httpRes != nil && httpRes.StatusCode != 0 {
			statusCode = httpRes.StatusCode
		}
	}

	if res == nil || res.Interaction == nil {
		return &core.LLMCompletionResponse{
			StatusCode: statusCode,
		}, fmt.Errorf("%w: empty interaction returned by gemini", ErrInvalidResponse)
	}

	inter := res.Interaction
	rawBytes, _ := json.Marshal(inter)
	rawBody := string(rawBytes)

	var usage core.TokenUsage
	if u := inter.GetUsage(); u != nil {
		if u.TotalInputTokens != nil {
			usage.PromptTokens = *u.TotalInputTokens
		}
		if u.TotalOutputTokens != nil {
			usage.CompletionTokens = *u.TotalOutputTokens
		}
		if u.TotalTokens != nil {
			usage.TotalTokens = *u.TotalTokens
		}
		if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
	}

	switch inter.Status {
	case interactions.InteractionStatusFailed, interactions.InteractionStatusCancelled, interactions.InteractionStatusIncomplete:
		errDetail := string(inter.Status)
		if len(inter.Errors) > 0 && inter.Errors[0].Message != nil && strings.TrimSpace(*inter.Errors[0].Message) != "" {
			errDetail = fmt.Sprintf("%s: %s", inter.Status, strings.TrimSpace(*inter.Errors[0].Message))
		}
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: statusCode,
			Usage:      usage,
		}, fmt.Errorf("%w: gemini interaction %s", ErrInvalidResponse, errDetail)
	}

	var outText string
	if inter.OutputText != nil {
		outText = strings.TrimSpace(*inter.OutputText)
	}
	if outText == "" {
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: statusCode,
			Usage:      usage,
		}, fmt.Errorf("%w: no output text returned by gemini", ErrInvalidResponse)
	}

	return &core.LLMCompletionResponse{
		Text:       outText,
		RawBody:    rawBody,
		StatusCode: statusCode,
		Usage:      usage,
	}, nil
}

func mapGeminiThinkingLevel(model, level string) *interactions.ThinkingLevel {
	normModel := strings.ToLower(strings.TrimSpace(model))
	if strings.HasPrefix(normModel, "gemini-1.") || strings.HasPrefix(normModel, "gemini-1-") ||
		strings.HasPrefix(normModel, "gemini-2.") || strings.HasPrefix(normModel, "gemini-2-") {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "low":
		return interactions.ThinkingLevelLow.ToPointer()
	case "medium":
		return interactions.ThinkingLevelMedium.ToPointer()
	case "high":
		return interactions.ThinkingLevelHigh.ToPointer()
	default:
		return nil
	}
}

func mapGeminiSDKError(err error) (*core.LLMCompletionResponse, error) {
	var clientErr *apierrors.CreateInteractionClientError
	if errors.As(err, &clientErr) {
		statusCode := http.StatusBadRequest
		if httpRes := clientErr.HTTPMeta.GetResponse(); httpRes != nil && httpRes.StatusCode != 0 {
			statusCode = httpRes.StatusCode
		}
		rawBody := clientErr.Error()
		msg := ""
		if clientErr.Error_.Message != nil {
			msg = *clientErr.Error_.Message
		}
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: statusCode,
		}, formatGeminiErrorMessage(statusCode, msg, rawBody)
	}

	var srvErr *apierrors.CreateInteractionServerError
	if errors.As(err, &srvErr) {
		statusCode := http.StatusInternalServerError
		if httpRes := srvErr.HTTPMeta.GetResponse(); httpRes != nil && httpRes.StatusCode != 0 {
			statusCode = httpRes.StatusCode
		}
		rawBody := srvErr.Error()
		msg := ""
		if srvErr.Error_.Message != nil {
			msg = *srvErr.Error_.Message
		}
		return &core.LLMCompletionResponse{
			RawBody:    rawBody,
			StatusCode: statusCode,
		}, formatGeminiErrorMessage(statusCode, msg, rawBody)
	}

	var valErr *apierrors.ResponseValidationError
	if errors.As(err, &valErr) {
		statusCode := valErr.StatusCode
		if statusCode == http.StatusOK || statusCode == 0 {
			return &core.LLMCompletionResponse{
				RawBody:    valErr.Body,
				StatusCode: http.StatusOK,
			}, fmt.Errorf("%w: malformed gemini response: %v", ErrInvalidResponse, valErr)
		}
		return &core.LLMCompletionResponse{
			RawBody:    valErr.Body,
			StatusCode: statusCode,
		}, parseGeminiError(statusCode, []byte(valErr.Body))
	}

	var apiErr *apierrors.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusOK {
			return &core.LLMCompletionResponse{
				RawBody:    apiErr.Body,
				StatusCode: http.StatusOK,
			}, fmt.Errorf("%w: unexpected gemini response: %v", ErrInvalidResponse, apiErr)
		}
		return &core.LLMCompletionResponse{
			RawBody:    apiErr.Body,
			StatusCode: apiErr.StatusCode,
		}, parseGeminiError(apiErr.StatusCode, []byte(apiErr.Body))
	}

	errText := err.Error()
	if strings.Contains(errText, "error unmarshaling json response body") ||
		strings.Contains(errText, "error unmarshalling json response body") {
		return &core.LLMCompletionResponse{
			StatusCode: http.StatusOK,
		}, fmt.Errorf("%w: malformed gemini response: %v", ErrInvalidResponse, err)
	}

	return nil, fmt.Errorf("gemini interactions request failed: %w", err)
}

func formatGeminiErrorMessage(statusCode int, msg, fallbackRaw string) error {
	if strings.TrimSpace(msg) != "" {
		return mapGeminiCleanError(statusCode, dedupeErrorLines(msg))
	}
	return parseGeminiError(statusCode, []byte(fallbackRaw))
}

// parseGeminiError parses HTTP error payloads from Google Gemini API, deduplicating repeated
// error lines in the response message and wrapping canonical sentinel errors.
func parseGeminiError(statusCode int, bodyBytes []byte) error {
	raw := strings.TrimSpace(string(bodyBytes))
	var msg string

	if len(raw) > 0 {
		var objErr struct {
			Error struct {
				Message string `json:"message"`
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

	return mapGeminiCleanError(statusCode, dedupeErrorLines(msg))
}

func dedupeErrorLines(msg string) string {
	if strings.TrimSpace(msg) == "" {
		return ""
	}
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
		return strings.Join(deduped, "\n")
	}
	return strings.TrimSpace(msg)
}

func mapGeminiCleanError(statusCode int, cleanMsg string) error {
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
