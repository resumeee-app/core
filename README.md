# resumeee-core

Pure Go domain core for **Resumeee** (`github.com/resumeee-app/resumeee-core`), containing all domain entities, business rules, provenance validation ("No Lies" invariant), PII obfuscation, prompt compilation & LLM orchestration, background generation worker pool, and zero-dependency in-memory storage.

## Packages

- `/` (`package core`) — Core entities, value objects, repository & service ports, and `LLMClient` transport interface (`Store`, `StoreBundle`, `LLMClient`, `LLMProvider`, `AuthProvider`).
- `llm/` — Prompt templates, prompt compiler (`BuildPrompt`), structured JSON schemas, PII-aware `ServiceAdapter`, `ResumeExtractor`, `ClientProvider` (adapting pure transport `core.LLMClient` implementations), and `Registry`.
- `validator/` — Anti-hallucination provenance validator verifying all tailored bullets and skills cite verified Main Profile entities.
- `privacy/` — Deterministic PII obfuscation and deobfuscation across LLM prompt/response boundaries.
- `services/profile/` — Career profile management, revision tracking, and JSON Resume import/export.
- `services/branch/` — Targeted resume branch & version lifecycle orchestration.
- `services/renderer/` — Multi-format resume rendering (Markdown, HTML, Plain Text, LaTeX).
- `services/worker/` — Asynchronous generation worker pool and retry/recovery coordinator.
- `memory/` — Thread-safe in-memory `core.Store` implementation and default presets seed data.
