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
	tailorSystemTmpl = template.Must(template.ParseFS(templateFS, "templates/tailor_system.tmpl"))

	// tailorUserTmpl renders the user prompt for resume tailoring.
	tailorUserTmpl = template.Must(template.ParseFS(templateFS, "templates/tailor_user.tmpl"))

	// gapAnalysisSystemTmpl renders the system prompt for qualification gap analysis.
	gapAnalysisSystemTmpl = template.Must(template.ParseFS(templateFS, "templates/gap_analysis_system.tmpl"))

	// gapAnalysisUserTmpl renders the user prompt for qualification gap analysis.
	gapAnalysisUserTmpl = template.Must(template.ParseFS(templateFS, "templates/gap_analysis_user.tmpl"))

	// resumeExtractSystemTmpl renders the system prompt for raw resume extraction.
	resumeExtractSystemTmpl = template.Must(template.ParseFS(templateFS, "templates/resume_extract_system.tmpl"))

	// resumeExtractUserTmpl renders the user prompt for raw resume extraction.
	resumeExtractUserTmpl = template.Must(template.ParseFS(templateFS, "templates/resume_extract_user.tmpl"))

	// atsExtractSystemTmpl renders the system prompt for Stage 1 ATS extraction.
	atsExtractSystemTmpl = template.Must(template.ParseFS(templateFS, "templates/ats_extract_system.tmpl"))

	// atsExtractUserTmpl renders the user prompt for Stage 1 ATS extraction.
	atsExtractUserTmpl = template.Must(template.ParseFS(templateFS, "templates/ats_extract_user.tmpl"))

	// atsMatchSystemTmpl renders the system prompt for Stage 2 ATS job matching.
	atsMatchSystemTmpl = template.Must(template.ParseFS(templateFS, "templates/ats_match_system.tmpl"))

	// atsMatchUserTmpl renders the user prompt for Stage 2 ATS job matching.
	atsMatchUserTmpl = template.Must(template.ParseFS(templateFS, "templates/ats_match_user.tmpl"))
)

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

// PromptOptions configures whether compiled prompts rely on provider-native structured output
// enforcement or append an inline <response_schema> fallback block to the system prompt.
type PromptOptions struct {
	NativeStructuredOutput bool
}

// PromptCompilerOption configures a PromptCompiler instance.
type PromptCompilerOption func(*PromptCompiler)

// WithPromptRegistry binds a provider Registry to PromptCompiler so CompilePrompt dynamically
// omits or appends <response_schema> according to resolved client capabilities.
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

// CompilePrompt builds the tailoring prompt payload and returns a core.PromptPreview.
func (c *PromptCompiler) CompilePrompt(req core.GenerationRequest) (*core.PromptPreview, error) {
	native := c != nil && c.nativeStructuredOutput
	if c != nil && c.registry != nil {
		client, model, err := c.registry.Resolve(req, req.Preset, requestProfile(req))
		if err == nil && client != nil {
			native = client.SupportsStructuredOutput(model)
		} else {
			native = false
		}
	}

	payload, err := BuildPromptWithOptions(req, PromptOptions{NativeStructuredOutput: native})
	if err != nil {
		return nil, err
	}
	return &core.PromptPreview{
		SystemPrompt: payload.SystemPrompt,
		UserPrompt:   payload.UserPrompt,
	}, nil
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

	ctx, err := buildTailorPromptContext(obfReq, idMap)
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
func buildTailorPromptContext(obfReq core.GenerationRequest, idMap map[string]string) (tailorPromptContext, error) {
	priorJSON, err := serializePriorVersion(obfReq.PriorVersion, idMap)
	if err != nil {
		return tailorPromptContext{}, err
	}
	profileJSON, err := serializeProjectedMainProfile(obfReq.MainProfile, idMap)
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
		GlobalRules:        strings.TrimSpace(obfReq.GlobalRules),
		PresetInstructions: strings.TrimSpace(obfReq.PresetInstructions),
		TargetLanguage:     validatedLanguage,
		TargetRole:         strings.TrimSpace(obfReq.BranchContext.TargetRole),
		TargetCompany:      strings.TrimSpace(obfReq.BranchContext.TargetCompany),
		TargetRequirements: strings.TrimSpace(obfReq.BranchContext.TargetRequirements),
		BranchInstructions: strings.TrimSpace(obfReq.BranchContext.BranchInstructions),
		PinnedEntities:     projectShortIDList(obfReq.BranchContext.PinnedEntityIDs, idMap),
		ExcludedEntities:   projectShortIDList(obfReq.BranchContext.ExcludedEntityIDs, idMap),
		PriorVersionJSON:   priorJSON,
		MainProfileJSON:    profileJSON,
	}, nil
}

// serializePriorVersion projects prior to short IDs and marshals it as formatted JSON.
func serializePriorVersion(prior *core.TailoredContent, idMap map[string]string) (string, error) {
	if prior == nil {
		return "", nil
	}
	projected := projectPriorVersion(prior, idMap)
	raw, err := json.MarshalIndent(projected, "", "  ")
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

// BuildGapAnalysisPromptWithOptions constructs a gap-analysis prompt payload and omits
// the inline <response_schema> block when opts.NativeStructuredOutput is true.
func BuildGapAnalysisPromptWithOptions(req core.GenerationRequest, opts PromptOptions) (*PromptPayload, error) {
	session := sanitizer.NewSessionFromSnapshot(req.MainProfile)
	obfProfile := session.ObfuscateSnapshot(req.MainProfile)
	idMap := buildShortIDMap(obfProfile)

	systemPrompt, err := renderSystemPrompt(gapAnalysisSystemTmpl, 1024, opts, GapAnalysisResponseSchemaJSON)
	if err != nil {
		return nil, fmt.Errorf("failed to render gap analysis system prompt template: %w", err)
	}

	profileJSON, err := serializeProjectedMainProfile(obfProfile, idMap)
	if err != nil {
		return nil, err
	}

	ctx := gapAnalysisPromptContext{
		TargetRole:         session.ObfuscateString(strings.TrimSpace(req.BranchContext.TargetRole)),
		TargetCompany:      strings.TrimSpace(req.BranchContext.TargetCompany),
		TargetRequirements: session.ObfuscateString(strings.TrimSpace(req.BranchContext.TargetRequirements)),
		BranchInstructions: session.ObfuscateString(strings.TrimSpace(req.BranchContext.BranchInstructions)),
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

	ctx := resumeExtractPromptContext{
		RawText: strings.TrimSpace(rawText),
	}

	var userBuilder strings.Builder
	userBuilder.Grow(2048 + len(rawText))
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

// atsPromptTagRegex matches internal prompt XML delimiter tags to prevent tag smuggling from untrusted inputs.
var atsPromptTagRegex = regexp.MustCompile(`(?i)</?\s*(?:resume_document|target_job_description|extracted_candidate_data|ats_extraction_rules|ats_matching_rules|response_schema|rules_hierarchy|tier_1_global_rules|tier_2_strategy_instructions|tier_3_branch_directives|main_career_profile|custom_instructions)\b[^>]*>`)

// sanitizeATSPromptValue strips matching outer tags and escapes embedded XML delimiter tags in untrusted text.
func sanitizeATSPromptValue(content, stripOuterTag string) string {
	trimmed := strings.TrimSpace(content)
	if stripOuterTag != "" {
		openTag := "<" + stripOuterTag + ">"
		closeTag := "</" + stripOuterTag + ">"
		if strings.HasPrefix(strings.ToLower(trimmed), strings.ToLower(openTag)) &&
			strings.HasSuffix(strings.ToLower(trimmed), strings.ToLower(closeTag)) &&
			len(trimmed) >= len(openTag)+len(closeTag) {
			trimmed = strings.TrimSpace(trimmed[len(openTag) : len(trimmed)-len(closeTag)])
		}
	}
	return atsPromptTagRegex.ReplaceAllStringFunc(trimmed, func(m string) string {
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
	obfResumeText := sanitizeATSPromptValue(session.ObfuscateString(strings.TrimSpace(req.ResumeText)), "resume_document")

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

	candidateJSONBytes, err := serializeExtractedCandidate(extracted)
	if err != nil {
		return nil, err
	}

	targetRole := strings.TrimSpace(b.TargetRole)
	targetCompany := strings.TrimSpace(b.TargetCompany)
	targetReqs := strings.TrimSpace(b.TargetRequirements)
	if session != nil {
		targetRole = session.ObfuscateString(targetRole)
		targetReqs = session.ObfuscateString(targetReqs)
	}

	ctx := atsMatchPromptContext{
		TargetRole:             sanitizeATSPromptValue(targetRole, ""),
		TargetCompany:          sanitizeATSPromptValue(targetCompany, ""),
		TargetRequirements:     sanitizeATSPromptValue(targetReqs, "target_job_description"),
		ExtractedCandidateJSON: sanitizeATSPromptValue(string(candidateJSONBytes), "extracted_candidate_data"),
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

// serializeExtractedCandidate normalizes and JSON-marshals a Stage 1 extracted candidate payload.
func serializeExtractedCandidate(extracted *core.ATSExtractedCandidate) ([]byte, error) {
	candidate := extracted
	if candidate == nil {
		candidate = &core.ATSExtractedCandidate{}
	}
	candidate.Normalize()

	raw, err := json.MarshalIndent(candidate, "", "  ")
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
func serializeProjectedMainProfile(snapshot *core.MainProfileSnapshot, idMap map[string]string) (string, error) {
	if snapshot == nil {
		return "", nil
	}
	leanProfile := ProjectLeanMainProfile(snapshot, idMap)
	profileJSON, err := json.MarshalIndent(leanProfile, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize main profile snapshot: %w", err)
	}
	return string(profileJSON), nil
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
