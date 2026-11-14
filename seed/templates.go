package seed

import (
	_ "embed"
	"time"

	"github.com/resumeee-app/resumeee-core"
)

// Embedded built-in HTML, CSS, Markdown, and plain-text template sources.
var (
	// standardCleanCSS is the embedded CSS stylesheet for the Standard Clean template.
	//go:embed standard_clean_css.tmpl
	standardCleanCSS string
	// standardCleanHTML is the embedded HTML template for the Standard Clean template.
	//go:embed standard_clean_html.tmpl
	standardCleanHTML string
	// standardCleanMarkdown is the embedded Markdown template for the Standard Clean template.
	//go:embed standard_clean_markdown.tmpl
	standardCleanMarkdown string
	// standardCleanText is the embedded plain-text template for the Standard Clean template.
	//go:embed standard_clean_text.tmpl
	standardCleanText string

	// detailedImmigrationCVCSS is the embedded CSS stylesheet for the Detailed Immigration CV template.
	//go:embed detailed_immigration_cv_css.tmpl
	detailedImmigrationCVCSS string
	// detailedImmigrationCVHTML is the embedded HTML template for the Detailed Immigration CV template.
	//go:embed detailed_immigration_cv_html.tmpl
	detailedImmigrationCVHTML string
	// detailedImmigrationCVMarkdown is the embedded Markdown template for the Detailed Immigration CV template.
	//go:embed detailed_immigration_cv_markdown.tmpl
	detailedImmigrationCVMarkdown string
	// detailedImmigrationCVText is the embedded plain-text template for the Detailed Immigration CV template.
	//go:embed detailed_immigration_cv_text.tmpl
	detailedImmigrationCVText string

	// atsMinimalCSS is the embedded CSS stylesheet for the ATS Minimal template.
	//go:embed ats_minimal_css.tmpl
	atsMinimalCSS string
	// atsMinimalHTML is the embedded HTML template for the ATS Minimal template.
	//go:embed ats_minimal_html.tmpl
	atsMinimalHTML string
	// atsMinimalMarkdown is the embedded Markdown template for the ATS Minimal template.
	//go:embed ats_minimal_markdown.tmpl
	atsMinimalMarkdown string
	// atsMinimalText is the embedded plain-text template for the ATS Minimal template.
	//go:embed ats_minimal_text.tmpl
	atsMinimalText string

	// atsHybridTechCSS is the embedded CSS stylesheet for the ATS Hybrid Tech template.
	//go:embed ats_hybrid_tech_css.tmpl
	atsHybridTechCSS string
	// atsHybridTechHTML is the embedded HTML template for the ATS Hybrid Tech template.
	//go:embed ats_hybrid_tech_html.tmpl
	atsHybridTechHTML string
	// atsHybridTechMarkdown is the embedded Markdown template for the ATS Hybrid Tech template.
	//go:embed ats_hybrid_tech_markdown.tmpl
	atsHybridTechMarkdown string
	// atsHybridTechText is the embedded plain-text template for the ATS Hybrid Tech template.
	//go:embed ats_hybrid_tech_text.tmpl
	atsHybridTechText string

	// atsExecutiveCleanCSS is the embedded CSS stylesheet for the ATS Executive Clean template.
	//go:embed ats_executive_clean_css.tmpl
	atsExecutiveCleanCSS string
	// atsExecutiveCleanHTML is the embedded HTML template for the ATS Executive Clean template.
	//go:embed ats_executive_clean_html.tmpl
	atsExecutiveCleanHTML string
	// atsExecutiveCleanMarkdown is the embedded Markdown template for the ATS Executive Clean template.
	//go:embed ats_executive_clean_markdown.tmpl
	atsExecutiveCleanMarkdown string
	// atsExecutiveCleanText is the embedded plain-text template for the ATS Executive Clean template.
	//go:embed ats_executive_clean_text.tmpl
	atsExecutiveCleanText string
)

// StandardCleanTemplate returns the production default modern tech resume template set.
func StandardCleanTemplate() core.TemplateSet {
	now := time.Now().UTC()
	return core.TemplateSet{
		ID:               core.TemplateStandardCleanID,
		UserID:           core.SystemUserID,
		Name:             "Standard Clean",
		Description:      "Modern tech resume with clean typography, blue accent rules, categorized skill tags, and chronological experience.",
		IsBuiltIn:        true,
		CSSStyles:        standardCleanCSS,
		HTMLTemplate:     standardCleanHTML,
		MarkdownTemplate: standardCleanMarkdown,
		TextTemplate:     standardCleanText,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// DetailedImmigrationCVTemplate returns the unabridged formal curriculum vitae template set.
func DetailedImmigrationCVTemplate() core.TemplateSet {
	now := time.Now().UTC()
	return core.TemplateSet{
		ID:               core.TemplateDetailedImmigrationCVID,
		UserID:           core.SystemUserID,
		Name:             "Detailed Immigration / CV",
		Description:      "Complete formal career history template with hours per week, company location, supervisor contact, and duties summary for visa compliance.",
		IsBuiltIn:        true,
		CSSStyles:        detailedImmigrationCVCSS,
		HTMLTemplate:     detailedImmigrationCVHTML,
		MarkdownTemplate: detailedImmigrationCVMarkdown,
		TextTemplate:     detailedImmigrationCVText,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// ATSMinimalTemplate returns an ultra-minimalist, single-column resume template set optimized for ATS parsing and clean ASCII plain text.
func ATSMinimalTemplate() core.TemplateSet {
	now := time.Now().UTC()
	return core.TemplateSet{
		ID:               core.TemplateATSMinimalTxtID,
		UserID:           core.SystemUserID,
		Name:             "ATS Minimal Plain-Text & Clean HTML",
		Description:      "Ultra-minimalist, single-column layout strictly optimized for ATS text extraction parsers and clean ASCII plain-text exports.",
		IsBuiltIn:        true,
		CSSStyles:        atsMinimalCSS,
		HTMLTemplate:     atsMinimalHTML,
		MarkdownTemplate: atsMinimalMarkdown,
		TextTemplate:     atsMinimalText,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// ATSHybridTechTemplate returns a modern tech resume template optimized for engineering roles, technical skill taxonomy, and clean ATS parsing.
func ATSHybridTechTemplate() core.TemplateSet {
	now := time.Now().UTC()
	return core.TemplateSet{
		ID:               core.TemplateATSHybridTechID,
		UserID:           core.SystemUserID,
		Name:             "ATS Modern Tech",
		Description:      "Optimized for software engineers and technical practitioners with prominent competency taxonomy, tabular experience, and clean parsing.",
		IsBuiltIn:        true,
		CSSStyles:        atsHybridTechCSS,
		HTMLTemplate:     atsHybridTechHTML,
		MarkdownTemplate: atsHybridTechMarkdown,
		TextTemplate:     atsHybridTechText,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// ATSExecutiveCleanTemplate returns an executive-grade resume template set with slate palette, leadership hierarchy, and authoritative ASCII dividers.
func ATSExecutiveCleanTemplate() core.TemplateSet {
	now := time.Now().UTC()
	return core.TemplateSet{
		ID:               core.TemplateATSExecutiveCleanID,
		UserID:           core.SystemUserID,
		Name:             "ATS Executive Clean",
		Description:      "Executive single-column resume with leadership competency focus, generous whitespace, slate palette, and authoritative ASCII delimiters.",
		IsBuiltIn:        true,
		CSSStyles:        atsExecutiveCleanCSS,
		HTMLTemplate:     atsExecutiveCleanHTML,
		MarkdownTemplate: atsExecutiveCleanMarkdown,
		TextTemplate:     atsExecutiveCleanText,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
}

// DefaultTemplateSets returns the list of built-in production templates.
func DefaultTemplateSets() []core.TemplateSet {
	return []core.TemplateSet{
		StandardCleanTemplate(),
		DetailedImmigrationCVTemplate(),
		ATSMinimalTemplate(),
		ATSHybridTechTemplate(),
		ATSExecutiveCleanTemplate(),
	}
}
