package renderer

import "github.com/resumeee-app/resumeee-core"

const (
	// TemplateStandardCleanID is the canonical ID for the built-in Standard Clean template set.
	TemplateStandardCleanID = core.TemplateStandardCleanID

	// TemplateDetailedImmigrationCVID is the canonical ID for the built-in Detailed Immigration / CV template set.
	TemplateDetailedImmigrationCVID = core.TemplateDetailedImmigrationCVID

	// TemplateATSMinimalTxtID is the canonical ID for the built-in ATS Minimal Plain-Text & Clean HTML template set.
	TemplateATSMinimalTxtID = core.TemplateATSMinimalTxtID

	// TemplateATSHybridTechID is the canonical ID for the built-in ATS Modern Tech resume template set.
	TemplateATSHybridTechID = core.TemplateATSHybridTechID

	// TemplateATSExecutiveCleanID is the canonical ID for the built-in ATS Executive Clean resume template set.
	TemplateATSExecutiveCleanID = core.TemplateATSExecutiveCleanID
)

// StandardCleanTemplate returns the production default modern tech resume template set.
func StandardCleanTemplate() core.TemplateSet {
	return core.StandardCleanTemplate()
}

// DetailedImmigrationCVTemplate returns the unabridged academic/immigration CV template set.
func DetailedImmigrationCVTemplate() core.TemplateSet {
	return core.DetailedImmigrationCVTemplate()
}

// ATSMinimalTemplate returns the ATS minimal plain text and HTML template set.
func ATSMinimalTemplate() core.TemplateSet {
	return core.ATSMinimalTemplate()
}

// ATSHybridTechTemplate returns the ATS modern tech resume template set.
func ATSHybridTechTemplate() core.TemplateSet {
	return core.ATSHybridTechTemplate()
}

// ATSExecutiveCleanTemplate returns the ATS executive clean resume template set.
func ATSExecutiveCleanTemplate() core.TemplateSet {
	return core.ATSExecutiveCleanTemplate()
}

// DefaultTemplateSets returns the list of built-in production templates.
func DefaultTemplateSets() []core.TemplateSet {
	return core.DefaultTemplateSets()
}
