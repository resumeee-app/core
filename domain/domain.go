// Package domain defines the core domain models, interfaces, entities, error sentinels,
// and business invariants for the Resumeee resume tailoring engine.
//
// Core domain entities include:
//   - Profile: Candidate biographical details, contact info, and master revision counters
//   - MasterProfileSnapshot: Comprehensive career graph containing experiences, skills, education,
//     certifications, projects, and arbitrary career facts
//   - Branch: Targeted resume branch configuration and target role metadata
//   - Version: Snapshot of tailored resume content, prompt history, and generation metadata
//   - TailoredContent: Section-by-section tailored resume content with citation mappings
//   - TemplateSet: Multi-format rendering templates (HTML, Markdown, Plain Text)
//
// All persistence repositories, LLM adapters, worker queues, and HTTP handlers adhere
// to the decoupled interface contracts defined in this package.
package domain
