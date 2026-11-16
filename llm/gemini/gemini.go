// Package gemini implements llm.Client for Google Gemini models using the official
// Google GenAI Interactions SDK (google.golang.org/genai).
package gemini

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"google.golang.org/genai"
	"google.golang.org/genai/interactions/models/apierrors"
	"google.golang.org/genai/interactions/models/interactions"
	"google.golang.org/genai/interactions/models/operations"
	"google.golang.org/genai/interactions/retry"

	"github.com/resumeee-app/core"
	"github.com/resumeee-app/core/llm"
	"github.com/resumeee-app/core/llm/internal/httpx"
)

// defaultBaseURL is the upstream Google Generative Language API base endpoint.
const defaultBaseURL = "https://generativelanguage.googleapis.com"

// Option configures a Google Gemini Client.
type Option func(*Client)

// WithBaseURL overrides the upstream Google Gemini API base URL.
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.settings.SetBaseURL(baseURL)
	}
}

// WithHTTPClient overrides the underlying HTTP client used by the Gemini SDK transport.
func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) {
		c.settings.SetHTTPClient(client)
	}
}

// WithDefaultModel overrides the default Gemini model identifier used when Request.Model is blank.
func WithDefaultModel(model string) Option {
	return func(c *Client) {
		c.settings.SetDefaultModel(model)
	}
}

// WithTimeout overrides the request timeout applied to Gemini API requests.
func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.settings.SetTimeout(timeout)
	}
}

// Client implements llm.Client for Google Gemini models via google.golang.org/genai.
type Client struct {
	apiKey   string
	settings httpx.Settings
	sdk      *genai.Client
	initErr  error
}

// New creates a Google Gemini Client configured with apiKey and optional settings.
func New(apiKey string, opts ...Option) *Client {
	c := &Client{
		apiKey:   apiKey,
		settings: httpx.DefaultSettings(defaultBaseURL, llm.DefaultModelForProvider(llm.ProviderGemini)),
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	c.settings.Finalize()

	if trimmedKey := strings.TrimSpace(apiKey); trimmedKey != "" {
		sdkClient, err := genai.NewClient(context.Background(), &genai.ClientConfig{
			APIKey:     trimmedKey,
			Backend:    genai.BackendGeminiAPI,
			HTTPClient: httpx.WrapBoundedClient(c.settings.HTTPClient),
			HTTPOptions: genai.HTTPOptions{
				BaseURL: strings.TrimRight(strings.TrimSpace(c.settings.BaseURL), "/"),
			},
		})
		c.sdk = sdkClient
		c.initErr = err
	}
	return c
}

// Name returns the canonical Gemini provider identifier.
func (c *Client) Name() string {
	return llm.ProviderGemini
}

// DefaultModel returns the configured default Gemini model identifier.
func (c *Client) DefaultModel() string {
	if c != nil && strings.TrimSpace(c.settings.DefaultModel) != "" {
		return c.settings.DefaultModel
	}
	return llm.DefaultModelForProvider(llm.ProviderGemini)
}

// Models returns the default model catalog entry exposed by this Gemini Client.
func (c *Client) Models() []core.ModelOption {
	def := c.DefaultModel()
	return []core.ModelOption{
		{
			Provider:    llm.ProviderGemini,
			Model:       def,
			DisplayName: fmt.Sprintf("Google Gemini — %s", def),
			IsDefault:   true,
		},
	}
}

// SupportsStructuredOutput reports that Google Gemini supports native JSON Schema structured outputs.
func (c *Client) SupportsStructuredOutput(_ string) bool {
	return true
}

// Complete executes a completion request against the Google Gemini Interactions API.
func (c *Client) Complete(ctx context.Context, req llm.Request) (*llm.Response, error) {
	if c == nil || strings.TrimSpace(c.apiKey) == "" {
		return nil, fmt.Errorf("%w: gemini api key is required", llm.ErrUnauthorized)
	}
	if c.initErr != nil {
		return nil, fmt.Errorf("failed to initialize gemini client: %w", c.initErr)
	}
	if c.sdk == nil || c.sdk.Interactions == nil {
		return nil, fmt.Errorf("gemini interactions client is not initialized")
	}

	model := strings.TrimSpace(req.Model)
	if model == "" {
		model = c.DefaultModel()
	}

	body, err := buildInteractionRequest(model, req)
	if err != nil {
		return nil, err
	}
	sdkReq := operations.CreateInteractionRequest{
		Body: operations.NewCreateInteractionRequestBody(body),
	}
	start := time.Now()
	res, err := c.sdk.Interactions.Create(
		ctx,
		sdkReq,
		operations.WithRetries(retry.Config{Strategy: "none"}),
	)
	latency := time.Since(start)
	if err != nil {
		resp, mappedErr := mapSDKError(err)
		return resp.PopulateTelemetry(c.Name(), model, latency), mappedErr
	}
	resp, extractErr := extractInteractionResponse(res)
	return resp.PopulateTelemetry(c.Name(), model, latency), extractErr
}

// buildInteractionRequest translates req into a GenAI SDK CreateModelInteraction payload.
func buildInteractionRequest(model string, req llm.Request) (interactions.CreateModelInteraction, error) {
	input := interactions.NewInteractionsInput(req.UserPrompt)
	inter := interactions.CreateModelInteraction{
		Model: interactions.Model(model),
		Input: &input,
	}
	if strings.TrimSpace(req.SystemPrompt) != "" {
		inter.SystemInstruction = genai.Ptr(req.SystemPrompt)
	}
	if level := mapThinkingLevel(model, req.ReasoningLevel); level != nil {
		inter.GenerationConfig = &interactions.GenerationConfig{ThinkingLevel: level}
	}
	if req.ResponseSchema != nil {
		schemaMap, err := schemaToMap(req.ResponseSchema)
		if err != nil {
			return interactions.CreateModelInteraction{}, fmt.Errorf("%w: invalid response schema: %v", llm.ErrBadRequest, err)
		}
		format := interactions.NewCreateModelInteractionResponseFormat(
			interactions.NewResponseFormat(interactions.TextResponseFormat{
				MimeType: interactions.TextResponseFormatMimeTypeApplicationJSON.ToPointer(),
				Schema:   schemaMap,
			}),
		)
		inter.ResponseFormat = &format
	}
	return inter, nil
}

// schemaToMap converts a typed llm.Schema into the untyped map expected by the Gemini SDK.
func schemaToMap(schema *llm.Schema) (map[string]any, error) {
	b, err := json.Marshal(schema.Clone())
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// extractInteractionResponse validates SDK interaction output and maps status, usage, and text.
func extractInteractionResponse(res *operations.CreateInteractionResponse) (*llm.Response, error) {
	statusCode := interactionStatusCode(res)
	if res == nil || res.Interaction == nil {
		return &llm.Response{StatusCode: statusCode}, fmt.Errorf("%w: empty interaction returned by gemini", llm.ErrInvalidResponse)
	}

	inter := res.Interaction
	var rawBody string
	if rawBytes, err := json.Marshal(inter); err == nil {
		rawBody = string(rawBytes)
	}
	usage := extractInteractionUsage(inter)

	switch inter.Status {
	case interactions.InteractionStatusFailed,
		interactions.InteractionStatusCancelled,
		interactions.InteractionStatusIncomplete,
		interactions.InteractionStatusBudgetExceeded,
		interactions.InteractionStatusRequiresContinuation:
		return &llm.Response{
			RawBody:    rawBody,
			StatusCode: statusCode,
			Usage:      usage,
		}, fmt.Errorf("%w: gemini interaction %s", llm.ErrInvalidResponse, formatInteractionFailure(inter))
	}

	var outText string
	if inter.OutputText != nil {
		outText = strings.TrimSpace(*inter.OutputText)
	}
	if outText == "" {
		return &llm.Response{
			RawBody:    rawBody,
			StatusCode: statusCode,
			Usage:      usage,
		}, fmt.Errorf("%w: no output text returned by gemini", llm.ErrInvalidResponse)
	}

	return &llm.Response{
		Text:       outText,
		RawBody:    rawBody,
		StatusCode: statusCode,
		Usage:      usage,
	}, nil
}

// interactionStatusCode extracts the underlying HTTP status code from a Gemini SDK response.
func interactionStatusCode(res *operations.CreateInteractionResponse) int {
	if res != nil {
		if httpRes := res.HTTPMeta.GetResponse(); httpRes != nil && httpRes.StatusCode != 0 {
			return httpRes.StatusCode
		}
	}
	return 0
}

// formatInteractionFailure builds a safe status summary from an unsuccessful interaction without echoing body payloads.
func formatInteractionFailure(inter *interactions.Interaction) string {
	return string(inter.Status)
}

// extractInteractionUsage reads token counts from inter and backfills TotalTokens when omitted.
func extractInteractionUsage(inter *interactions.Interaction) core.TokenUsage {
	var usage core.TokenUsage
	u := inter.GetUsage()
	if u == nil {
		return usage
	}
	if u.TotalInputTokens != nil {
		usage.PromptTokens = *u.TotalInputTokens
	}
	if u.TotalOutputTokens != nil {
		usage.CompletionTokens = *u.TotalOutputTokens
	}
	if u.TotalTokens != nil {
		usage.TotalTokens = *u.TotalTokens
	}
	return usage.Normalize()
}

// mapThinkingLevel translates domain reasoning effort onto Gemini 3+ interaction thinking levels.
func mapThinkingLevel(model, level string) *interactions.ThinkingLevel {
	normModel := strings.ToLower(strings.TrimSpace(model))
	// Gemini 1.x/2.x models reject the Interactions thinking_level field.
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

// mapSDKError converts GenAI SDK error types into canonical llm sentinel errors without leaking upstream response bodies.
func mapSDKError(err error) (*llm.Response, error) {
	var clientErr *apierrors.CreateInteractionClientError
	if errors.As(err, &clientErr) {
		statusCode := httpStatusFromMeta(clientErr.HTTPMeta.GetResponse(), http.StatusBadRequest)
		code := derefString(clientErr.Error_.Code)
		msg := derefString(clientErr.Error_.Message)
		rawBody := clientErr.Error()
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode}, mapCleanError(statusCode, rawBody, code, msg)
	}

	var srvErr *apierrors.CreateInteractionServerError
	if errors.As(err, &srvErr) {
		statusCode := httpStatusFromMeta(srvErr.HTTPMeta.GetResponse(), http.StatusInternalServerError)
		code := derefString(srvErr.Error_.Code)
		msg := derefString(srvErr.Error_.Message)
		rawBody := srvErr.Error()
		return &llm.Response{RawBody: rawBody, StatusCode: statusCode}, mapCleanError(statusCode, rawBody, code, msg)
	}

	var valErr *apierrors.ResponseValidationError
	if errors.As(err, &valErr) {
		return mapValidationError(valErr.StatusCode, valErr.Body)
	}

	var apiErr *apierrors.APIError
	if errors.As(err, &apiErr) {
		if apiErr.StatusCode == http.StatusOK {
			return &llm.Response{RawBody: apiErr.Body, StatusCode: http.StatusOK},
				fmt.Errorf("%w: unexpected gemini response (status 200, %d bytes)", llm.ErrInvalidResponse, len(apiErr.Body))
		}
		return &llm.Response{RawBody: apiErr.Body, StatusCode: apiErr.StatusCode},
			mapCleanError(apiErr.StatusCode, apiErr.Body)
	}

	errText := err.Error()
	if strings.Contains(errText, "error unmarshaling json response body") ||
		strings.Contains(errText, "error unmarshalling json response body") {
		return &llm.Response{StatusCode: http.StatusOK},
			fmt.Errorf("%w: malformed gemini response json", llm.ErrInvalidResponse)
	}

	return &llm.Response{}, fmt.Errorf("gemini interactions request failed: %w", err)
}

// httpStatusFromMeta returns resp.StatusCode when non-zero, otherwise fallbackStatus.
func httpStatusFromMeta(resp *http.Response, fallbackStatus int) int {
	if resp != nil && resp.StatusCode != 0 {
		return resp.StatusCode
	}
	return fallbackStatus
}

// mapValidationError maps SDK response-validation failures to ErrInvalidResponse or sanitized HTTP errors.
func mapValidationError(statusCode int, body string) (*llm.Response, error) {
	if statusCode == http.StatusOK || statusCode == 0 {
		return &llm.Response{RawBody: body, StatusCode: http.StatusOK},
			fmt.Errorf("%w: malformed gemini response (status 200, %d bytes)", llm.ErrInvalidResponse, len(body))
	}
	return &llm.Response{RawBody: body, StatusCode: statusCode}, mapCleanError(statusCode, body)
}

// derefString returns the string referenced by ptr, or an empty string when nil.
func derefString(ptr *string) string {
	if ptr == nil {
		return ""
	}
	return *ptr
}

// mapCleanError maps an HTTP status code and upstream payloads to canonical llm sentinel errors
// without embedding upstream response payloads into returned error messages.
func mapCleanError(statusCode int, rawBody string, signals ...string) error {
	if mapped := llm.MapHTTPStatusError("gemini", statusCode, rawBody); errors.Is(mapped, llm.ErrUpstreamQuota) {
		return mapped
	}
	for _, sig := range signals {
		if sig != "" && errors.Is(llm.MapHTTPStatusError("gemini", statusCode, sig), llm.ErrUpstreamQuota) {
			return fmt.Errorf("%w: gemini api returned status %d (%d bytes)", llm.ErrUpstreamQuota, statusCode, len(rawBody))
		}
	}
	return llm.MapHTTPStatusError("gemini", statusCode, rawBody)
}

// _ enforces at compile time that Client implements llm.Client.
var _ llm.Client = (*Client)(nil)
