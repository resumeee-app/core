# core

Pure Go domain core for **Resumeee** (`github.com/resumeee-app/core`), containing all domain entities, business rules, provenance validation ("No Lies" invariant), PII obfuscation, prompt compilation & LLM orchestration, PDF text extraction, background generation worker pool, and zero-dependency in-memory storage.

## Packages

- `/` (`package core`) — Core domain entities (`Profile`, `Branch`, `BranchVersion`, `Job`, `Preset`, `TemplateSet`, `TenantQuota`, `User`), value objects, sentinel errors, identity contracts, and normalization helpers.
- `store/` — Centralized persistence domain rules, validation, state transitions, quota accounting, and unified `core.Job` queue orchestration (`Store`) built over a pure persistence SPI (`Backend`, `ReadTx`, `Tx`).
- `storetest/` — Backend-agnostic compliance test suite (`RunAll`) verifying that any `store.Backend` enforces all persistence invariants, concurrency guarantees, and queue semantics.
- `seed/` — Database-agnostic seed data, lazy tenant provisioning, and embedded built-in resume `.tmpl` templates (`DefaultData`, `DefaultDataStores`, `EnsureTenantProvisioned`, `EnsureTenantProvisionedStores`, `DefaultTemplateSets`, `DefaultPresets`, `DefaultGlobalSkills`).
- `provenance/` — Anti-hallucination provenance validator verifying all tailored bullets and skills cite verified Main Profile entities (`Validator`, `Validate`, `ValidateStrict`).
- `sanitizer/` — Deterministic PII obfuscation and deobfuscation across LLM prompt/response boundaries (`Session`, `NewSession`, `NewSessionFromSnapshot`, `NewSessionFromRequest`).
- `pdf/` — PDF binary extraction into plain resume text (`Extractor`).
- `profile/` — Career profile management, revision tracking, and JSON Resume import/export (`Service`).
- `branch/` — Targeted resume branch & version lifecycle orchestration with narrow consumer-defined ports (`Service`).
- `renderer/` — Multi-format resume rendering (Markdown, HTML, Plain Text) and preview helpers (`Renderer`, `ResolveVersionOutputs`).
- `worker/` — Asynchronous generation & PDF-parsing worker pool and crash-recovery coordinator (`Pool`).
- `llm/` — Pure domain LLM orchestration, prompt templates, typed `Schema` definitions, structured output/fallback registry, PII-aware `Service`, and raw resume `Extractor` (`Client`, `Registry`, `Service`, `Extractor`).
  - `llm/gemini/` — Google Gemini API client implementing `llm.Client`.
  - `llm/openai/` — OpenAI Responses API client implementing `llm.Client`.
  - `llm/anthropic/` — Anthropic Messages API client implementing `llm.Client`.
  - `llm/ollama/` — Local Ollama API client implementing `llm.Client`.
  - `llm/openaicompat/` — OpenAI-compatible Chat Completions client implementing `llm.Client`.
- `memory/` — Thread-safe in-memory `store.Backend` and convenience `*store.Store` constructors (`NewBackend`, `New`, `NewWithClock`, `NewSeeded`).

## Quality Gates

```bash
# Run all verification checks (gofmt check, go test -race -v ./..., golangci-lint run ./...)
make verify
```
