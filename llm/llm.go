// Package llm provides client adapters, prompt engineering pipelines, schema enforcement,
// debugging mechanisms, and provider registry resolution for generative AI model providers.
//
// Supported model provider backends include:
//   - Google Gemini (gemini-2.5-pro, gemini-2.5-flash, gemini-1.5-pro, gemini-1.5-flash)
//   - OpenAI (gpt-4o, gpt-4o-mini, gpt-4-turbo)
//   - Anthropic (claude-3-5-sonnet, claude-3-opus, claude-3-5-haiku)
//   - OpenAI-compatible endpoints (LocalAI, Ollama, vLLM, DeepSeek)
//
// The package enforces structured JSON outputs conforming to domain.TailoredContent
// schemas and records prompt/response traces when debugging is enabled.
package llm
