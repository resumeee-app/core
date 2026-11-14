# resumeee-core

Pure Go domain core for **Resumeee** (`github.com/resumeee-app/resumeee-core`), containing all domain entities, business rules, provenance validation ("No Lies" invariant), PII obfuscation, prompt compilation & LLM orchestration, PDF text extraction, background generation worker pool, and zero-dependency in-memory storage.

## Packages

- `/` (`package core`) — Core entities, value objects, sentinel errors, normalization helpers, and store contracts (`Store`, `StoreBundle`, `ProfileStore`, `CareerStore`, `BranchStore`, `PresetStore`, `TemplateStore`, `PDFJobStore`, `QuotaStore`, `UserStore`).
- `seed/` — Database-agnostic seed data, lazy tenant provisioning, and embedded built-in resume `.tmpl` templates (`DefaultData`, `DefaultDataStores`, `EnsureTenantProvisioned`, `EnsureTenantProvisionedStores`, `DefaultTemplateSets`, `DefaultPresets`).
- `provenance/` — Anti-hallucination provenance validator verifying all tailored bullets and skills cite verified Main Profile entities (`Validator`, `Validate`, `ValidateStrict`).
- `sanitizer/` — Deterministic PII obfuscation and deobfuscation across LLM prompt/response boundaries (`Sanitize`, `Deobfuscate`).
- `pdf/` — PDF binary extraction into plain resume text (`Extractor`).
- `profile/` — Career profile management, revision tracking, and JSON Resume import/export (`Service`).
- `branch/` — Targeted resume branch & version lifecycle orchestration with narrow consumer-defined ports (`Service`).
- `renderer/` — Multi-format resume rendering (Markdown, HTML, Plain Text, LaTeX) and preview helpers (`Renderer`, `ResolveVersionOutputs`).
- `worker/` — Asynchronous generation & PDF-parsing worker pool and crash-recovery coordinator (`Pool`).
- `llm/` — Pure domain LLM orchestration, prompt templates, typed `Schema` definitions, structured output/fallback registry, PII-aware `Service`, and raw resume `Extractor` (`Client`, `Registry`, `Service`, `Extractor`).
  - `llm/gemini/` — Google Gemini API client implementing `llm.Client`.
  - `llm/openai/` — OpenAI Responses API client implementing `llm.Client`.
  - `llm/anthropic/` — Anthropic Messages API client implementing `llm.Client`.
  - `llm/ollama/` — Local Ollama API client implementing `llm.Client`.
  - `llm/openaicompat/` — OpenAI-compatible Chat Completions client implementing `llm.Client`.
- `memory/` — Thread-safe in-memory `core.Store` and consumer store implementation (`New`, `NewSeeded`).

## Quality Gates

```bash
# Run all verification checks (gofmt check, go test -race -v ./..., golangci-lint run ./...)
make verify
```
