# AGENTS.md

Idiomatic Go standards for `resumeee-core`. Run `make verify` after every change.

## Documentation

- **Package comments**: Place a single `Package <name> ...` doc comment in `<name>.go` (enforced by `godoc_test.go`).
- **Every symbol documented**: Write a Go doc comment starting with the exact identifier name for every exported *and* unexported type, function, method, constant, and package-level `var`.
- **Constants**: Document both grouped `const (...)` blocks and individual non-obvious constants with their unit, invariant, or purpose.
- **Small functions & why-comments**: Keep functions short, focused, and shallowly nested—extract well-named helpers rather than writing long multi-phase functions. Write inline comments to explain *why* (invariants, edge cases, ordering), never to narrate syntax.

## Interfaces & Type Safety

- **Consumer-side interfaces**: Define small, single-purpose interfaces in the package that *consumes* them. Keep implementations decoupled from central interface files.
- **Accept interfaces, return structs**: Accept interfaces in constructors/functions when abstraction is needed, and return concrete struct pointers (`*Store`, `*Client`).
- **Compile-time implementation checks**: Verify interface satisfaction at compile time with `var _ Interface = (*ConcreteType)(nil)` in the implementing package.
- **Typed contracts over assertions**: Pass concrete types or typed interfaces instead of `any` with runtime type assertions (`v.(T)`). Restrict `any` and type switches strictly to dynamic JSON unmarshaling or template boundaries.

## Naming & Package Structure

- **Organize by domain, not technical layer**: Group code into cohesive domain packages (`branch`, `profile`, `resume`, `pdf`, `llm`). Avoid generic or layered buckets (`utils`, `helpers`, `common`, `lib`, `tools`, `interfaces`, `models`, `services`).
- **File names**: Name files after the specific concept they implement (`store.go`, `tailor.go`, `schema.go`). Avoid generic catch-all files (`interfaces.go`, `util.go`, `helpers.go`, `types.go`). When refactoring existing catch-all files, split them into focused domain files.
- **Package names & stuttering**: Use short, lowercase, singular nouns matching the directory name. Omit the package name from exported identifiers (`branch.Store`, not `branch.BranchStore`).
- **Identifiers**:
  - Apply Go initialisms consistently (`ID`, `URL`, `JSON`, `PDF`, `LLM`, `ATS` — e.g. `UserID`, `parsePDF`).
  - Omit `Get` on simple getters (`Profile()` rather than `GetProfile()`).
  - Name constructors `New` or `New<Type>`, and sentinel errors `Err<Name>`.
  - Use a consistent 1–2 letter receiver name derived from the type across all methods (never `this` or `self`).
- **Dependency boundaries**: Keep core domain packages free of database, ORM, HTTP, and third-party SDK imports (enforced by `import_boundary_test.go`); inject external capabilities through consumer-defined interfaces.

## Verification

- Run `make verify` (`gofmt`, `go test -race`, `golangci-lint`) before concluding any task and report only observed results.
