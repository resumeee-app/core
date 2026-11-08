package renderer

import "resumeee/internal/domain"

const (
	// TemplateStandardCleanID is the canonical ID for the built-in Standard Clean template set.
	TemplateStandardCleanID = domain.TemplateStandardCleanID

	// TemplateDetailedImmigrationCVID is the canonical ID for the built-in Detailed Immigration / CV template set.
	TemplateDetailedImmigrationCVID = domain.TemplateDetailedImmigrationCVID

	// TemplateATSMinimalTxtID is the canonical ID for the built-in ATS Minimal Plain-Text & Clean HTML template set.
	TemplateATSMinimalTxtID = domain.TemplateATSMinimalTxtID

	// TemplateATSHybridTechID is the canonical ID for the built-in ATS Modern Tech resume template set.
	TemplateATSHybridTechID = domain.TemplateATSHybridTechID

	// TemplateATSExecutiveCleanID is the canonical ID for the built-in ATS Executive Clean resume template set.
	TemplateATSExecutiveCleanID = domain.TemplateATSExecutiveCleanID
)

// StandardCleanTemplate returns the production default modern tech resume template set.
func StandardCleanTemplate() domain.TemplateSet {
	return domain.StandardCleanTemplate()
}

// DetailedImmigrationCVTemplate returns the unabridged academic/immigration CV template set.
func DetailedImmigrationCVTemplate() domain.TemplateSet {
	return domain.DetailedImmigrationCVTemplate()
}

// ATSMinimalTemplate returns the ATS minimal plain text and HTML template set.
func ATSMinimalTemplate() domain.TemplateSet {
	return domain.ATSMinimalTemplate()
}

// ATSHybridTechTemplate returns the ATS modern tech resume template set.
func ATSHybridTechTemplate() domain.TemplateSet {
	return domain.ATSHybridTechTemplate()
}

// ATSExecutiveCleanTemplate returns the ATS executive clean resume template set.
func ATSExecutiveCleanTemplate() domain.TemplateSet {
	return domain.ATSExecutiveCleanTemplate()
}

// DefaultTemplateSets returns the list of built-in production templates.
func DefaultTemplateSets() []domain.TemplateSet {
	return domain.DefaultTemplateSets()
}
