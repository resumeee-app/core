// Package core defines the domain models, persistence store contracts, error sentinels,
// and business invariants for the Resumeee resume tailoring engine.
//
// Core domain entities include:
//   - Profile: Candidate biographical details, contact info, and main revision counters
//   - MainProfileSnapshot: Comprehensive career graph containing experiences, skills, education,
//     certifications, projects, and arbitrary career facts
//   - Branch: Targeted resume branch configuration and target role metadata
//   - BranchVersion: Snapshot of tailored resume content, prompt history, and generation metadata
//   - TailoredContent: Section-by-section tailored resume content with citation mappings
//   - TemplateSet: Multi-format rendering templates (HTML, Markdown, Plain Text)
//
// Persistence adapters, LLM providers, worker pools, and HTTP handlers depend on these domain
// types without coupling core packages to external storage, transport, or SDK implementations.
package core

// Normalizable represents any domain entity that requires normalization or alias synchronization prior to persistence.
type Normalizable interface {
	Normalize() error
}
