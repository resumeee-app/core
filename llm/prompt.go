package llm

import (
	"embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"text/template"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/branch"
	"github.com/resumeee-app/resumeee-core/sanitizer"
)

// templateFS embeds the built-in system and user prompt templates shipped with package llm.
//
//go:embed templates/*
var templateFS embed.FS

// Compiled prompt templates parsed at package initialization from embedded templateFS assets.
var (
	// tailorSystemTmpl renders the system prompt for resume tailoring.
	tailorSystemTmpl = parseScrubbedPromptTemplate("templates/tailor_system.tmpl")

	// tailorUserTmpl renders the user prompt for resume tailoring.
	tailorUserTmpl = parseScrubbedPromptTemplate("templates/tailor_user.tmpl")

	// gapAnalysisSystemTmpl renders the system prompt for qualification gap analysis.
	gapAnalysisSystemTmpl = parseScrubbedPromptTemplate("templates/gap_analysis_system.tmpl")

	// gapAnalysisUserTmpl renders the user prompt for qualification gap analysis.
	gapAnalysisUserTmpl = parseScrubbedPromptTemplate("templates/gap_analysis_user.tmpl")

	// resumeExtractSystemTmpl renders the system prompt for raw resume extraction.
	resumeExtractSystemTmpl = template.Must(template.ParseFS(templateFS, "templates/resume_extract_system.tmpl"))

	// resumeExtractUserTmpl renders the user prompt for raw resume extraction.
	resumeExtractUserTmpl = template.Must(template.ParseFS(templateFS, "templates/resume_extract_user.tmpl"))

	// atsExtractSystemTmpl renders the system prompt for Stage 1 ATS extraction.
	atsExtractSystemTmpl = parseScrubbedPromptTemplate("templates/ats_extract_system.tmpl")

	// atsExtractUserTmpl renders the user prompt for Stage 1 ATS extraction.
	atsExtractUserTmpl = parseScrubbedPromptTemplate("templates/ats_extract_user.tmpl")

	// atsMatchSystemTmpl renders the system prompt for Stage 2 ATS job matching.
	atsMatchSystemTmpl = parseScrubbedPromptTemplate("templates/ats_match_system.tmpl")

	// atsMatchUserTmpl renders the user prompt for Stage 2 ATS job matching.
	atsMatchUserTmpl = parseScrubbedPromptTemplate("templates/ats_match_user.tmpl")
)

// parseScrubbedPromptTemplate parses the embedded prompt template at path together with the
// shared PII placeholder partials, keeping path as the root template executed on render.
func parseScrubbedPromptTemplate(path string) *template.Template {
	return template.Must(template.ParseFS(templateFS, path, "templates/pii_placeholder_rules.tmpl"))
}

// ReadTemplateFile reads an embedded prompt template file by filename from templates/.
func ReadTemplateFile(name string) (string, error) {
	data, err := templateFS.ReadFile("templates/" + name)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// PromptPayload wraps the assembled system and user prompts ready for LLM consumption.
type PromptPayload struct {
	SystemPrompt string
	UserPrompt   string
}

// PromptOptions configures capability-aware prompt compilation behavior for both preview
// compilation and live outbound LLM completion requests.
type PromptOptions struct {
	// NativeStructuredOutput omits the inline <response_schema> fallback block when true.
	NativeStructuredOutput bool
	// CompactJSON omits whitespace indentation in serialized candidate and prior-version JSON payloads.
	CompactJSON bool
}

// PromptCompilerOption configures a PromptCompiler instance.
type PromptCompilerOption func(*PromptCompiler)

// WithPromptRegistry binds a provider Registry to PromptCompiler so preview compilation methods
// dynamically omit or append <response_schema> according to resolved client capabilities.
func WithPromptRegistry(reg *Registry) PromptCompilerOption {
	return func(c *PromptCompiler) {
		c.registry = reg
	}
}

// WithNativeStructuredOutput sets the default structured output capability assumed by PromptCompiler
// when no Registry is attached.
func WithNativeStructuredOutput(enabled bool) PromptCompilerOption {
	return func(c *PromptCompiler) {
		c.nativeStructuredOutput = enabled
	}
}

// PromptCompiler implements branch.PromptCompiler using capability-aware prompt assembly.
type PromptCompiler struct {
	registry               *Registry
	nativeStructuredOutput bool
}

// NewPromptCompiler constructs a new PromptCompiler with optional capability configuration.
func NewPromptCompiler(opts ...PromptCompilerOption) *PromptCompiler {
	c := &PromptCompiler{}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	return c
}

// resolveNativeStructuredOutput determines whether prompts should omit fallback JSON schemas
// based on resolved registry client capabilities or the compiler's default option.
func (c *PromptCompiler) resolveNativeStructuredOutput(req core.GenerationRequest) bool {
	native := c != nil && c.nativeStructuredOutput
	if c != nil && c.registry != nil {
		client, model, err := c.registry.Resolve(req, req.Preset, requestProfile(req))
		if err == nil && client != nil {
			native = client.SupportsStructuredOutput(model)
		} else {
			native = false
		}
	}
	return native
}

// toPromptPreview converts an internal PromptPayload into a domain core.PromptPreview.
func toPromptPreview(payload *PromptPayload) *core.PromptPreview {
	if payload == nil {
		return nil
	}
	return &core.PromptPreview{
		SystemPrompt: payload.SystemPrompt,
		UserPrompt:   payload.UserPrompt,
	}
}

// CompilePrompt builds the tailoring prompt payload and returns a core.PromptPreview.
func (c *PromptCompiler) CompilePrompt(req core.GenerationRequest) (*core.PromptPreview, error) {
	payload, err := BuildPromptWithOptions(req, PromptOptions{
		NativeStructuredOutput: c.resolveNativeStructuredOutput(req),
	})
	if err != nil {
		return nil, err
	}
	return toPromptPreview(payload), nil
}

// CompileGapAnalysisPrompt builds the qualification gap-analysis prompt payload and returns a core.PromptPreview.
func (c *PromptCompiler) CompileGapAnalysisPrompt(req core.GenerationRequest) (*core.PromptPreview, error) {
	payload, err := BuildGapAnalysisPromptWithOptions(req, PromptOptions{
		NativeStructuredOutput: c.resolveNativeStructuredOutput(req),
	})
	if err != nil {
		return nil, err
	}
	return toPromptPreview(payload), nil
}

// CompileATSExtractionPrompt builds Stage 1 ATS extraction prompts and returns a core.PromptPreview.
func (c *PromptCompiler) CompileATSExtractionPrompt(req core.ATSSimulationRequest) (*core.PromptPreview, error) {
	payload, _, err := BuildATSExtractionPromptWithOptions(req, PromptOptions{
		NativeStructuredOutput: c.resolveNativeStructuredOutput(atsToGenerationRequest(req)),
	})
	if err != nil {
		return nil, err
	}
	return toPromptPreview(payload), nil
}

// CompileATSMatchingPrompt builds Stage 2 ATS job matching prompts and returns a core.PromptPreview.
func (c *PromptCompiler) CompileATSMatchingPrompt(branch core.Branch, profile *core.MainProfileSnapshot, extracted *core.ATSExtractedCandidate) (*core.PromptPreview, error) {
	req := core.GenerationRequest{
		Model:          branch.ModelOverride,
		ReasoningLevel: branch.ReasoningLevel,
		MainProfile:    profile,
		BranchContext:  branch,
	}
	payload, err := BuildATSMatchingPromptWithOptions(branch, extracted, sanitizer.NewSessionFromSnapshot(profile), PromptOptions{
		NativeStructuredOutput: c.resolveNativeStructuredOutput(req),
	})
	if err != nil {
		return nil, err
	}
	return toPromptPreview(payload), nil
}

// CompileResumeExtractPrompt builds raw-resume extraction prompts and returns a core.PromptPreview.
func (c *PromptCompiler) CompileResumeExtractPrompt(rawText string) (*core.PromptPreview, error) {
	payload, err := BuildResumeExtractPromptWithOptions(rawText, PromptOptions{
		NativeStructuredOutput: c.resolveNativeStructuredOutput(core.GenerationRequest{}),
	})
	if err != nil {
		return nil, err
	}
	return toPromptPreview(payload), nil
}

// atsToGenerationRequest maps ATS simulation configuration fields into a GenerationRequest for provider resolution.
func atsToGenerationRequest(req core.ATSSimulationRequest) core.GenerationRequest {
	return core.GenerationRequest{
		Model:          req.Model,
		ReasoningLevel: req.ReasoningLevel,
		MainProfile:    req.MainProfile,
		BranchContext:  req.BranchContext,
	}
}

// renderSystemPrompt renders sysTmpl and appends an inline fallback schema unless NativeStructuredOutput is enabled.
func renderSystemPrompt(sysTmpl *template.Template, grow int, opts PromptOptions, schemaJSONFn func() (string, error)) (string, error) {
	var buf strings.Builder
	buf.Grow(grow)
	if err := sysTmpl.Execute(&buf, nil); err != nil {
		return "", err
	}
	return appendResponseSchemaIfNeeded(buf.String(), opts, schemaJSONFn)
}

// appendResponseSchemaIfNeeded appends a serialized fallback schema block when native structured output is off.
func appendResponseSchemaIfNeeded(baseSystemPrompt string, opts PromptOptions, schemaJSONFn func() (string, error)) (string, error) {
	trimmed := strings.TrimRight(baseSystemPrompt, "\n")
	if opts.NativeStructuredOutput {
		return trimmed, nil
	}
	schemaJSON, err := schemaJSONFn()
	if err != nil {
		return "", fmt.Errorf("failed to serialize fallback response schema: %w", err)
	}
	return AppendRawSchemaBlock(trimmed, schemaJSON), nil
}

// tailorPromptContext holds template substitution fields for tailor_user.tmpl.
type tailorPromptContext struct {
	GlobalRules        string
	PresetInstructions string
	TargetLanguage     string
	TargetRole         string
	TargetCompany      string
	TargetRequirements string
	BranchInstructions string
	PinnedEntities     string
	ExcludedEntities   string
	PriorVersionJSON   string
	MainProfileJSON    string
}

// gapAnalysisPromptContext holds template substitution fields for gap_analysis_user.tmpl.
type gapAnalysisPromptContext struct {
	TargetRole         string
	TargetCompany      string
	TargetRequirements string
	BranchInstructions string
	MainProfileJSON    string
}

// recordShortID records mappings from both canonical ID and short ID to shortID in idMap.
func recordShortID(idMap map[string]string, id, shortID string) {
	if id != "" {
		idMap[id] = shortID
	}
	if shortID != "" {
		idMap[shortID] = shortID
	}
}

// recordSkills adds short ID mappings for every skill in skills.
func recordSkills(idMap map[string]string, skills []core.Skill) {
	for _, s := range skills {
		recordShortID(idMap, s.ID, s.EffectiveShortID())
	}
}

// recordFacts adds short ID mappings for every fact and its linked skills in facts.
func recordFacts(idMap map[string]string, facts []core.Fact) {
	for _, f := range facts {
		recordShortID(idMap, f.ID, f.EffectiveShortID())
		recordSkills(idMap, f.Skills)
	}
}

// BuildPrompt constructs a tailoring prompt payload with an inline fallback response schema.
func BuildPrompt(req core.GenerationRequest) (*PromptPayload, error) {
	return BuildPromptWithOptions(req, PromptOptions{NativeStructuredOutput: false})
}

// BuildPromptWithOptions constructs a tailoring prompt payload and omits the inline <response_schema>
// block when opts.NativeStructuredOutput is true.
func BuildPromptWithOptions(req core.GenerationRequest, opts PromptOptions) (*PromptPayload, error) {
	session := sanitizer.NewSessionFromRequest(req)
	obfReq := session.ObfuscateRequest(req)
	idMap := buildShortIDMap(obfReq.MainProfile)

	systemPrompt, err := renderSystemPrompt(tailorSystemTmpl, 2048, opts, GenerationResponseSchemaJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to render tailor system prompt template: %w", err)
	}

	ctx, err := buildTailorPromptContext(obfReq, idMap, opts.CompactJSON)
	if err != nil {
		return nil, err
	}

	var userBuilder strings.Builder
	userBuilder.Grow(8192)
	if err := tailorUserTmpl.Execute(&userBuilder, ctx); err != nil {
		return nil, fmt.Errorf("failed to render tailor user prompt template: %w", err)
	}

	return &PromptPayload{
		SystemPrompt: systemPrompt,
		UserPrompt:   userBuilder.String(),
	}, nil
}

// buildTailorPromptContext prepares the projected profile, prior version, entity hints, and language context.
func buildTailorPromptContext(obfReq core.GenerationRequest, idMap map[string]string, compactJSON bool) (tailorPromptContext, error) {
	priorJSON, err := serializePriorVersion(obfReq.PriorVersion, idMap, compactJSON)
	if err != nil {
		return tailorPromptContext{}, err
	}
	profileJSON, err := serializeProjectedMainProfile(obfReq.MainProfile, idMap, compactJSON)
	if err != nil {
		return tailorPromptContext{}, err
	}

	targetLanguage := obfReq.TargetLanguage
	if strings.TrimSpace(targetLanguage) == "" && obfReq.Preset != nil {
		targetLanguage = obfReq.Preset.EffectiveLanguage()
	}
	validatedLanguage, err := core.ValidateLanguage(targetLanguage)
	if err != nil {
		return tailorPromptContext{}, fmt.Errorf("invalid prompt target language: %w", err)
	}

	return tailorPromptContext{
		GlobalRules:        sanitizePromptValue(obfReq.GlobalRules, ""),
		PresetInstructions: sanitizePromptValue(obfReq.PresetInstructions, ""),
		TargetLanguage:     validatedLanguage,
		TargetRole:         sanitizePromptValue(obfReq.BranchContext.TargetRole, ""),
		TargetCompany:      sanitizePromptValue(obfReq.BranchContext.TargetCompany, ""),
		TargetRequirements: sanitizePromptValue(obfReq.BranchContext.TargetRequirements, ""),
		BranchInstructions: sanitizePromptValue(obfReq.BranchContext.BranchInstructions, ""),
		PinnedEntities:     sanitizePromptValue(projectShortIDList(obfReq.BranchContext.PinnedEntityIDs, idMap), ""),
		ExcludedEntities:   sanitizePromptValue(projectShortIDList(obfReq.BranchContext.ExcludedEntityIDs, idMap), ""),
		PriorVersionJSON:   priorJSON,
		MainProfileJSON:    profileJSON,
	}, nil
}

// serializePriorVersion projects prior to short IDs and marshals it as compact or indented JSON.
func serializePriorVersion(prior *core.TailoredContent, idMap map[string]string, compact bool) (string, error) {
	if prior == nil {
		return "", nil
	}
	projected := projectPriorVersion(prior, idMap)
	raw, err := marshalPromptJSON(projected, compact)
	if err != nil {
		return "", fmt.Errorf("failed to serialize prior version: %w", err)
	}
	return string(raw), nil
}

// projectShortIDList joins resolved compact short IDs for a list of entity identifiers.
func projectShortIDList(ids []string, idMap map[string]string) string {
	if len(ids) == 0 {
		return ""
	}
	projected := make([]string, len(ids))
	for i, id := range ids {
		projected[i] = resolveShortID(id, idMap)
	}
	return strings.Join(projected, ", ")
}

// BuildGapAnalysisPrompt constructs a targeted prompt payload for evaluating candidate qualifications.
func BuildGapAnalysisPrompt(req core.GenerationRequest) (*PromptPayload, error) {
	return BuildGapAnalysisPromptWithOptions(req, PromptOptions{NativeStructuredOutput: false})
}

// BuildGapAnalysisPromptWithOptions constructs a qualification gap-analysis prompt and omits
// the inline <response_schema> block when opts.NativeStructuredOutput is true.
func BuildGapAnalysisPromptWithOptions(req core.GenerationRequest, opts PromptOptions) (*PromptPayload, error) {
	session := sanitizer.NewSessionFromRequest(req)
	obfProfile := session.ObfuscateSnapshot(req.MainProfile)
	obfBranch := session.ObfuscateBranchContext(req.BranchContext)
	idMap := buildShortIDMap(obfProfile)

	systemPrompt, err := renderSystemPrompt(gapAnalysisSystemTmpl, 1024, opts, GapAnalysisResponseSchemaJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to render gap analysis system prompt template: %w", err)
	}

	profileJSON, err := serializeProjectedMainProfile(obfProfile, idMap, opts.CompactJSON)
	if err != nil {
		return nil, err
	}

	ctx := gapAnalysisPromptContext{
		TargetRole:         sanitizePromptValue(obfBranch.TargetRole, ""),
		TargetCompany:      sanitizePromptValue(obfBranch.TargetCompany, ""),
		TargetRequirements: sanitizePromptValue(obfBranch.TargetRequirements, ""),
		BranchInstructions: sanitizePromptValue(obfBranch.BranchInstructions, ""),
		MainProfileJSON:    profileJSON,
	}

	var userBuilder strings.Builder
	userBuilder.Grow(4096)
	if err := gapAnalysisUserTmpl.Execute(&userBuilder, ctx); err != nil {
		return nil, fmt.Errorf("failed to render gap analysis user prompt template: %w", err)
	}

	return &PromptPayload{
		SystemPrompt: systemPrompt,
		UserPrompt:   userBuilder.String(),
	}, nil
}

// resumeExtractPromptContext holds template substitution fields for resume_extract_user.tmpl.
type resumeExtractPromptContext struct {
	RawText string
}

// BuildResumeExtractPrompt constructs system and user prompts for extracting unstructured resume text.
func BuildResumeExtractPrompt(rawText string) (*PromptPayload, error) {
	return BuildResumeExtractPromptWithOptions(rawText, PromptOptions{NativeStructuredOutput: false})
}

// BuildResumeExtractPromptWithOptions constructs resume-extraction prompts and omits
// the inline <response_schema> block when opts.NativeStructuredOutput is true.
func BuildResumeExtractPromptWithOptions(rawText string, opts PromptOptions) (*PromptPayload, error) {
	systemPrompt, err := renderSystemPrompt(resumeExtractSystemTmpl, 1024, opts, ResumeExtractResponseSchemaJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to render resume extract system prompt template: %w", err)
	}

	sanitizedRawText := sanitizePromptValue(rawText, "raw_resume_text")
	ctx := resumeExtractPromptContext{
		RawText: sanitizedRawText,
	}

	var userBuilder strings.Builder
	userBuilder.Grow(2048 + len(sanitizedRawText))
	if err := resumeExtractUserTmpl.Execute(&userBuilder, ctx); err != nil {
		return nil, fmt.Errorf("failed to render resume extract user prompt template: %w", err)
	}

	return &PromptPayload{
		SystemPrompt: systemPrompt,
		UserPrompt:   userBuilder.String(),
	}, nil
}

// atsExtractPromptContext holds template substitution fields for ats_extract_user.tmpl.
type atsExtractPromptContext struct {
	FormatUsed string
	ResumeText string
}

// atsMatchPromptContext holds template substitution fields for ats_match_user.tmpl.
type atsMatchPromptContext struct {
	TargetRole             string
	TargetCompany          string
	TargetRequirements     string
	ExtractedCandidateJSON string
}

// promptTagRegex matches internal prompt XML delimiter tags across all prompt templates
// to prevent embedded tag smuggling from untrusted inputs.
var promptTagRegex = regexp.MustCompile(`(?i)</?\s*(?:resume_document|raw_resume_text|target_job_description|extracted_candidate_data|ats_extraction_rules|ats_matching_rules|instructions|json_schema_hints|grounding_and_provenance_rules|gap_analysis_guidelines|schema_and_tailoring_hints|language_and_localization_rules|pii_placeholder_rules|target_context|main_profile_data|prior_active_version|response_schema|json_output_schema|rules_hierarchy|tier_1_global_rules|tier_2_strategy_instructions|tier_3_branch_directives|main_career_profile|custom_instructions)\b[^>]*>`)

// sanitizePromptValue strips matching outer wrapper tags and escapes embedded XML delimiter tags in untrusted text.
func sanitizePromptValue(content, stripOuterTag string) string {
	trimmed := strings.TrimSpace(content)
	if stripOuterTag != "" {
		openTag := "<" + stripOuterTag + ">"
		closeTag := "</" + stripOuterTag + ">"
		if len(trimmed) >= len(openTag)+len(closeTag) &&
			strings.EqualFold(trimmed[:len(openTag)], openTag) &&
			strings.EqualFold(trimmed[len(trimmed)-len(closeTag):], closeTag) {
			trimmed = strings.TrimSpace(trimmed[len(openTag) : len(trimmed)-len(closeTag)])
		}
	}
	return promptTagRegex.ReplaceAllStringFunc(trimmed, func(m string) string {
		escaped := strings.ReplaceAll(m, "<", "&lt;")
		return strings.ReplaceAll(escaped, ">", "&gt;")
	})
}

// BuildATSExtractionPrompt compiles Stage 1 ATS extraction prompts with candidate PII obfuscated.
func BuildATSExtractionPrompt(req core.ATSSimulationRequest) (*PromptPayload, *sanitizer.Session, error) {
	return BuildATSExtractionPromptWithOptions(req, PromptOptions{NativeStructuredOutput: false})
}

// BuildATSExtractionPromptWithOptions compiles Stage 1 ATS extraction prompts and omits the inline
// <response_schema> block when opts.NativeStructuredOutput is true.
func BuildATSExtractionPromptWithOptions(req core.ATSSimulationRequest, opts PromptOptions) (*PromptPayload, *sanitizer.Session, error) {
	formatUsed, err := core.NormalizeATSFormat(req.FormatUsed)
	if err != nil {
		return nil, nil, err
	}

	session := sanitizer.NewSessionFromSnapshot(req.MainProfile)
	obfResumeText := sanitizePromptValue(session.ObfuscateString(strings.TrimSpace(req.ResumeText)), "resume_document")

	systemPrompt, err := renderSystemPrompt(atsExtractSystemTmpl, 2048, opts, ATSExtractionResponseSchemaJSON)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to render ats extract system prompt template: %w", err)
	}

	ctx := atsExtractPromptContext{
		FormatUsed: formatUsed,
		ResumeText: obfResumeText,
	}

	var userBuilder strings.Builder
	userBuilder.Grow(1024 + len(obfResumeText))
	if err := atsExtractUserTmpl.Execute(&userBuilder, ctx); err != nil {
		return nil, nil, fmt.Errorf("failed to render ats extract user prompt template: %w", err)
	}

	return &PromptPayload{
		SystemPrompt: systemPrompt,
		UserPrompt:   userBuilder.String(),
	}, session, nil
}

// BuildATSMatchingPrompt compiles Stage 2 ATS job matching prompts from extracted candidate data.
func BuildATSMatchingPrompt(b core.Branch, extracted *core.ATSExtractedCandidate, session *sanitizer.Session) (*PromptPayload, error) {
	return BuildATSMatchingPromptWithOptions(b, extracted, session, PromptOptions{NativeStructuredOutput: false})
}

// BuildATSMatchingPromptWithOptions compiles Stage 2 ATS job matching prompts and omits the inline
// <response_schema> block when opts.NativeStructuredOutput is true.
func BuildATSMatchingPromptWithOptions(b core.Branch, extracted *core.ATSExtractedCandidate, session *sanitizer.Session, opts PromptOptions) (*PromptPayload, error) {
	systemPrompt, err := renderSystemPrompt(atsMatchSystemTmpl, 1024, opts, ATSMatchingResponseSchemaJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to render ats match system prompt template: %w", err)
	}

	candidateJSONBytes, err := serializeExtractedCandidate(extracted, session, opts.CompactJSON)
	if err != nil {
		return nil, err
	}

	obfBranch := session.ObfuscateBranchContext(b)

	ctx := atsMatchPromptContext{
		TargetRole:             sanitizePromptValue(obfBranch.TargetRole, ""),
		TargetCompany:          sanitizePromptValue(obfBranch.TargetCompany, ""),
		TargetRequirements:     sanitizePromptValue(obfBranch.TargetRequirements, "target_job_description"),
		ExtractedCandidateJSON: sanitizePromptValue(string(candidateJSONBytes), "extracted_candidate_data"),
	}

	var userBuilder strings.Builder
	userBuilder.Grow(2048 + len(candidateJSONBytes))
	if err := atsMatchUserTmpl.Execute(&userBuilder, ctx); err != nil {
		return nil, fmt.Errorf("failed to render ats match user prompt template: %w", err)
	}

	return &PromptPayload{
		SystemPrompt: systemPrompt,
		UserPrompt:   userBuilder.String(),
	}, nil
}

// serializeExtractedCandidate clones, obfuscates PII, normalizes, and JSON-marshals a Stage 1 extracted candidate payload.
func serializeExtractedCandidate(extracted *core.ATSExtractedCandidate, session *sanitizer.Session, compact bool) ([]byte, error) {
	candidate := session.ObfuscateExtractedCandidate(extracted)
	if candidate == nil {
		candidate = &core.ATSExtractedCandidate{}
	}
	candidate.Normalize()

	raw, err := marshalPromptJSON(candidate, compact)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize extracted candidate data: %w", err)
	}
	return raw, nil
}

// buildShortIDMap builds a lookup dictionary mapping canonical UUIDs to compact short IDs.
func buildShortIDMap(snapshot *core.MainProfileSnapshot) map[string]string {
	idMap := make(map[string]string)
	if snapshot == nil {
		return idMap
	}
	recordSkills(idMap, snapshot.AllSkills())
	for _, exp := range snapshot.Experiences {
		recordShortID(idMap, exp.ID, exp.EffectiveShortID())
		recordFacts(idMap, exp.Facts)
	}
	for _, proj := range snapshot.Projects {
		recordShortID(idMap, proj.ID, proj.EffectiveShortID())
		recordFacts(idMap, proj.Facts)
	}
	for _, edu := range snapshot.Education {
		recordShortID(idMap, edu.ID, edu.EffectiveShortID())
		recordFacts(idMap, edu.Facts)
	}
	for _, cert := range snapshot.Certifications {
		recordShortID(idMap, cert.ID, cert.EffectiveShortID())
	}
	recordFacts(idMap, snapshot.Facts)
	return idMap
}

// serializeProjectedMainProfile projects snapshot with short IDs into a LeanMainProfile JSON string.
func serializeProjectedMainProfile(snapshot *core.MainProfileSnapshot, idMap map[string]string, compact bool) (string, error) {
	if snapshot == nil {
		return "", nil
	}
	leanProfile := ProjectLeanMainProfile(snapshot, idMap)
	profileJSON, err := marshalPromptJSON(leanProfile, compact)
	if err != nil {
		return "", fmt.Errorf("failed to serialize main profile snapshot: %w", err)
	}
	return string(profileJSON), nil
}

// marshalPromptJSON encodes structured prompt payloads compactly for live LLM requests
// or with two-space indentation for readable prompt previews.
func marshalPromptJSON(v any, compact bool) ([]byte, error) {
	if compact {
		return json.Marshal(v)
	}
	return json.MarshalIndent(v, "", "  ")
}

// resolveShortID resolves a canonical identifier via idMap or falls back to core.ShortenID.
func resolveShortID(id string, idMap map[string]string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	if mapped, ok := idMap[trimmed]; ok && mapped != "" {
		return mapped
	}
	return core.ShortenID(trimmed)
}

// resolveShortIDs maps a slice of canonical entity or citation IDs into compact short identifiers.
func resolveShortIDs(ids []string, idMap map[string]string) []string {
	if len(ids) == 0 {
		return nil
	}
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = resolveShortID(id, idMap)
	}
	return out
}

// projectPriorBullets transforms a slice of TailoredBullets to compact short IDs.
func projectPriorBullets(bullets []core.TailoredBullet, idMap map[string]string) []core.TailoredBullet {
	if len(bullets) == 0 {
		return nil
	}
	res := make([]core.TailoredBullet, len(bullets))
	for i, b := range bullets {
		res[i] = core.TailoredBullet{
			Text:           b.Text,
			SourceFactIDs:  resolveShortIDs(b.SourceFactIDs, idMap),
			SourceSkillIDs: resolveShortIDs(b.SourceSkillIDs, idMap),
		}
	}
	return res
}

// projectPriorVersion projects a baseline TailoredContent version to compact short IDs while
// zeroing non-tailoring fields so metadata never leaks across prompt revisions.
func projectPriorVersion(prior *core.TailoredContent, idMap map[string]string) *core.TailoredContent {
	if prior == nil {
		return nil
	}
	return &core.TailoredContent{
		Summary:        prior.Summary,
		SkillGroups:    projectPriorSkillGroups(prior.SkillGroups, idMap),
		Experiences:    projectPriorExperiences(prior.Experiences, idMap),
		Projects:       projectPriorProjects(prior.Projects, idMap),
		Education:      projectPriorEducation(prior.Education, idMap),
		Certifications: projectPriorCertifications(prior.Certifications, idMap),
	}
}

// projectPriorSkillGroups projects skill group citations onto short IDs.
func projectPriorSkillGroups(groups []core.TailoredSkillGroup, idMap map[string]string) []core.TailoredSkillGroup {
	if len(groups) == 0 {
		return nil
	}
	out := make([]core.TailoredSkillGroup, len(groups))
	for i, sg := range groups {
		out[i] = core.TailoredSkillGroup{
			Category:       sg.Category,
			Skills:         append([]string(nil), sg.Skills...),
			SourceSkillIDs: resolveShortIDs(sg.SourceSkillIDs, idMap),
		}
	}
	return out
}

// projectPriorExperiences projects work experience source IDs and bullets onto short IDs.
func projectPriorExperiences(exps []core.TailoredExperience, idMap map[string]string) []core.TailoredExperience {
	if len(exps) == 0 {
		return nil
	}
	out := make([]core.TailoredExperience, len(exps))
	for i, exp := range exps {
		out[i] = core.TailoredExperience{
			SourceEntityID: resolveShortID(exp.SourceEntityID, idMap),
			Company:        exp.Company,
			RoleTitle:      exp.RoleTitle,
			StartDate:      exp.StartDate,
			EndDate:        exp.EndDate,
			DutiesSummary:  exp.DutiesSummary,
			Bullets:        projectPriorBullets(exp.Bullets, idMap),
		}
	}
	return out
}

// projectPriorProjects projects project source IDs and bullets onto short IDs.
func projectPriorProjects(projs []core.TailoredProject, idMap map[string]string) []core.TailoredProject {
	if len(projs) == 0 {
		return nil
	}
	out := make([]core.TailoredProject, len(projs))
	for i, proj := range projs {
		out[i] = core.TailoredProject{
			SourceEntityID: resolveShortID(proj.SourceEntityID, idMap),
			Name:           proj.Name,
			Role:           proj.Role,
			URL:            proj.URL,
			StartDate:      proj.StartDate,
			EndDate:        proj.EndDate,
			Summary:        proj.Summary,
			Bullets:        projectPriorBullets(proj.Bullets, idMap),
		}
	}
	return out
}

// projectPriorEducation projects education source IDs and bullets onto short IDs.
func projectPriorEducation(edus []core.TailoredEducation, idMap map[string]string) []core.TailoredEducation {
	if len(edus) == 0 {
		return nil
	}
	out := make([]core.TailoredEducation, len(edus))
	for i, edu := range edus {
		out[i] = core.TailoredEducation{
			SourceEntityID: resolveShortID(edu.SourceEntityID, idMap),
			Institution:    edu.Institution,
			Degree:         edu.Degree,
			Field:          edu.Field,
			StartDate:      edu.StartDate,
			EndDate:        edu.EndDate,
			Score:          edu.Score,
			Bullets:        projectPriorBullets(edu.Bullets, idMap),
		}
	}
	return out
}

// projectPriorCertifications projects certification source IDs onto short IDs.
func projectPriorCertifications(certs []core.TailoredCert, idMap map[string]string) []core.TailoredCert {
	if len(certs) == 0 {
		return nil
	}
	out := make([]core.TailoredCert, len(certs))
	for i, cert := range certs {
		out[i] = core.TailoredCert{
			SourceEntityID: resolveShortID(cert.SourceEntityID, idMap),
			Name:           cert.Name,
			Issuer:         cert.Issuer,
			Date:           cert.Date,
			URL:            cert.URL,
		}
	}
	return out
}

// _ enforces at compile time that PromptCompiler implements branch.PromptCompiler.
var _ branch.PromptCompiler = (*PromptCompiler)(nil)
