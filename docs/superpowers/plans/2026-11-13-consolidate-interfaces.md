# Consolidate Fragmented Interfaces Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans or superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate all 19 production capability-probing and cross-repository interface type assertions across `resumeee-core` (plus 6 test-fallout casts) by deepening fragmented interfaces and locking the invariant with an AST regression test.

**Architecture:** Consolidate split optional/sidecar interfaces directly into the primary contracts (`core.LLMProvider`, `core.LLMService`, `core.ResumeExtractor`, and `llm.Completer`), replace `SeedDefaultData`'s `profileRepo.(core.UserRepository)` smuggling with explicit `SeedDefaultData(ctx, core.Store, ...)` and `SeedDefaultDataRepos(..., core.UserRepository, ...)`, and enforce zero interface assertions in non-test code via `TestNoInterfaceTypeAssertions` in `interface_assertion_test.go`.

**Tech Stack:** Go (`go 1.27`), `go/ast` & `go/types`, `gofmt`, `go test -race`, `golangci-lint`

## Global Constraints

- Execute all work in isolated worktree `.worktrees/consolidate-interfaces` on branch `refactor/consolidate-interfaces`; keep root `main` clean.
- Always pass `GOWORK=off` when running `go test`, `golangci-lint`, or `make verify` inside the worktree so the parent `go.work` does not resolve `resumeee-core` to the root checkout.
- Follow `AGENTS.md`: single `Package <name>` doc comment in `<name>.go`, Go doc comments starting with the exact identifier name for every exported and unexported symbol, compile-time `var _ Interface = (*Concrete)(nil)` assertions, and concrete struct pointers returned from constructors.
- Pass `GOWORK=off make verify` (`gofmt`, `go test -race -v ./...`, `golangci-lint run ./...`) after changes.

## Review Focus

- Nil receivers, nil `Completer`, or nil `Provider`/`Store` inputs must return descriptive domain errors (`ErrNoProviders`, `ErrValidation`, or `"store cannot be nil"`) rather than panicking on direct interface method calls.
- Providers returning `SupportsStructuredOutput(model) == false` must receive `<response_schema>` in the system prompt and `ResponseSchema == nil` in `LLMCompletionRequest`, with zero double-attachment.
- Providers returning empty/unparseable output from `CompleteStructured` during `GenerateGapAnalysis` must cleanly fall back to `GenerateTailoredResume`.
- `SeedDefaultDataRepos` must provision users even when `profileRepo` does not implement `core.UserRepository` (e.g., `struct{ core.ProfileRepository }` or `core.StoreBundle`).
- AST guard `TestNoInterfaceTypeAssertions` must reject any future non-allowlisted type assertion or interface type switch in production files while allowing legitimate untyped boundaries (`bool`, `string`, `*bytes.Buffer`, `[]string`, `[]any`, `map[string]any`).

---

### Task 1: Isolated Worktree Setup & Plan Commit

**Files:**
- Modify: `.gitignore`
- Create: `docs/superpowers/plans/2026-11-13-consolidate-interfaces.md`

- [x] **Step 1:** Exclude `.worktrees/` locally in `.git/info/exclude` and create `.worktrees/consolidate-interfaces` on `refactor/consolidate-interfaces`.
- [x] **Step 2:** Verify clean baseline in the worktree with `GOWORK=off make verify`.
- [x] **Step 3:** Add `.worktrees/` to worktree `.gitignore` and commit this implementation plan.

---

### Task 2: AST Regression Guard (`interface_assertion_test.go`) — Red Phase

**Files:**
- Create: `interface_assertion_test.go`

- [x] **Step 1:** Implement `TestNoInterfaceTypeAssertions` parsing every non-test `.go` file via `go/parser` and checking `*ast.TypeAssertExpr` and `*ast.TypeSwitchStmt` against `allowedProductionAssertionTypes` (`bool`, `string`, `*bytes.Buffer`, `[]string`, `[]any`, `map[string]any`).
- [x] **Step 2:** Run `GOWORK=off go test -v -run TestNoInterfaceTypeAssertions .` and confirm it fails reporting all 19 production capability-probing assertions across `interfaces.go`, `llm/client_adapter.go`, `llm/extractor.go`, `llm/llm.go`, `seed/seed.go`, and `services/branch/branch.go`.

---

### Task 3: Consolidate Core Interfaces (`interfaces.go`)

**Files:**
- Modify: `interfaces.go`

- [x] **Step 1:** Remove `StructuredOutputCapable` and `SupportsStructuredOutput(target any, model string) bool`.
- [x] **Step 2:** Add `DefaultModel() string`, `Models() []ModelOption`, `SupportsStructuredOutput(model string) bool`, and `CompleteStructured(ctx context.Context, operation string, req LLMCompletionRequest) (string, TokenUsage, error)` to `LLMProvider`.
- [x] **Step 3:** Add `SimulateATS(ctx context.Context, req ATSSimulationRequest) (*ATSSimulationResponse, error)` to `LLMService`.
- [x] **Step 4:** Add `ExtractResumeWithUsage(ctx context.Context, text string, provider string, model string) (*JSONResume, TokenUsage, error)` to `ResumeExtractor` and remove `UsageResumeExtractor`.

---

### Task 4: Consolidate `llm/` Adapters, Registry, Extractor, and Tests

**Files:**
- Modify: `llm/client_adapter.go`, `llm/llm.go`, `llm/prompt.go`, `llm/service_adapter.go`, `llm/extractor.go`
- Modify: `llm/extractor_test.go`, `llm/llm_test.go`, `llm/ats_test.go`, `llm/service_adapter_sanitizer_test.go`

- [x] **Step 1 (`llm/client_adapter.go`):** Remove `ModelCatalogProvider`, `StructuredCompleter`, `canExecuteProviderCompletion`, and `supportsNativeStructuredCall`. Rename `CompleteStructuredWithUsage` to `CompleteStructured`, simplify `(structuredCall).execute` to call `c.Provider.SupportsStructuredOutput` and `c.Provider.CompleteStructured` directly, and add `var _ core.LLMProvider = (*ClientProvider)(nil)`.
- [x] **Step 2 (`llm/llm.go` & `llm/prompt.go` & `llm/service_adapter.go`):** Remove all 4 `ModelCatalogProvider` assertions in `Registry`, replace `core.SupportsStructuredOutput` / `supportsNativeStructuredCall` calls with `provider.SupportsStructuredOutput(model)`, drop `canExecuteProviderCompletion` in `GenerateGapAnalysis` (falling back to `GenerateTailoredResume` on unrecognized/empty output), and add compile-time checks for `core.ModelEnumerator`, `core.PromptCompiler`, and `core.LLMService`.
- [x] **Step 3 (`llm/extractor.go`):** Remove `UsageCompleter`, `ModelCatalogCompleter`, `SchemaCompleter`, `RawCompleter`, and `RawUsageCompleter`. Consolidate into `Completer` (`AvailableModels`, `SupportsStructuredCompletion`, `Complete`), export `ResumeExtractor` and `RegistryCompleter` with constructors returning concrete pointers (`*ResumeExtractor`, `*RegistryCompleter`), remove `UsageResumeExtractor`/`SchemaCompleter`/`UsageCompleter`/`ModelCatalogCompleter` assertions, and add compile-time interface checks.
- [x] **Step 4 (`llm/*_test.go`):** Update `stubUsageCompleter`, `unstructuredCatalogCompleter`, `fakeProvider`, `unstructuredCustomProvider`, `sequencedATSProvider`, and `mockSanitizerProvider` to satisfy `llm.Completer` / `core.LLMProvider` directly, removing all test casts to `UsageResumeExtractor` and `ModelCatalogCompleter`.

---

### Task 5: Remove Anonymous Inline Assertions in `services/branch` & Update Worker Fakes

**Files:**
- Modify: `services/branch/branch.go`, `services/branch/branch_test.go`
- Modify: `services/worker/worker_test.go`

- [x] **Step 1 (`services/branch/branch.go`):** Replace inline `GenerateGapAnalysis` and `SimulateATS` interface assertions (and dead fallback branch in `AnalyzeGaps`) with direct calls on `s.llmService`, and add `var _ Service = (*service)(nil)`.
- [x] **Step 2 (`services/branch/branch_test.go`):** Add `templateRepo core.TemplateRepository` to `testBranchEnv` to eliminate `env.branchRepo.(core.TemplateRepository)` casts, and implement `SimulateATS` on `stubLLMService`.
- [x] **Step 3 (`services/worker/worker_test.go`):** Implement `SimulateATS` on `mockLLM`, `blockingMockLLM`, and `callbackLLM`, and implement `ExtractResumeWithUsage` on `callbackResumeExtractor`.

---

### Task 6: Replace Cross-Repository Smuggling in `seed/seed.go`

**Files:**
- Modify: `seed/seed.go`
- Modify: `memory/memory.go`, `memory/seed_test.go`

- [x] **Step 1 (`seed/seed.go`):** Update `SeedDefaultData(ctx context.Context, store core.Store, defaultUserID ...string) error` and add `SeedDefaultDataRepos(ctx context.Context, profileRepo core.ProfileRepository, branchRepo core.BranchRepository, templateRepo core.TemplateRepository, presetRepo core.PresetRepository, userRepo core.UserRepository, defaultUserID ...string) error`, removing `profileRepo.(core.UserRepository)`.
- [x] **Step 2 (`memory/memory.go` & `memory/seed_test.go`):** Update `SeedDefaultData` callers to pass `store` (`core.Store`), and add `TestSeedDefaultDataRepos_DecoupledUserRepository` verifying user provisioning works when `profileRepo` does not implement `core.UserRepository`.

---

### Task 7: Verification, Commit, Push & PR

- [x] **Step 1:** Run `GOWORK=off make verify` in `.worktrees/consolidate-interfaces` and verify 0 failures and 0 lint issues.
- [x] **Step 2:** Verify root checkout (`main`) has clean `git status --short`.
- [x] **Step 3:** Mark tasks complete in this plan, commit cleanly, push `refactor/consolidate-interfaces`, and create the PR via `gh pr create`.
