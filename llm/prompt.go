package llm

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"resumeee/internal/domain"
	"resumeee/internal/privacy"
)

//go:embed templates/*
var templateFS embed.FS

var (
	tailorSystemTmpl        = template.Must(template.ParseFS(templateFS, "templates/tailor_system.tmpl"))
	tailorUserTmpl          = template.Must(template.ParseFS(templateFS, "templates/tailor_user.tmpl"))
	gapAnalysisSystemTmpl   = template.Must(template.ParseFS(templateFS, "templates/gap_analysis_system.tmpl"))
	gapAnalysisUserTmpl     = template.Must(template.ParseFS(templateFS, "templates/gap_analysis_user.tmpl"))
	resumeExtractSystemTmpl = template.Must(template.ParseFS(templateFS, "templates/resume_extract_system.tmpl"))
	resumeExtractUserTmpl   = template.Must(template.ParseFS(templateFS, "templates/resume_extract_user.tmpl"))
)

// ReadTemplateFile reads a template file by name from the embedded templates directory.
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

type gapAnalysisPromptContext struct {
	TargetRole         string
	TargetCompany      string
	TargetRequirements string
	BranchInstructions string
	MainProfileJSON    string
}

func recordShortID(idMap map[string]string, id, shortID string) {
	if id != "" {
		idMap[id] = shortID
	}
	if shortID != "" {
		idMap[shortID] = shortID
	}
}

func recordSkills(idMap map[string]string, skills []domain.Skill) {
	for _, s := range skills {
		recordShortID(idMap, s.ID, s.EffectiveShortID())
	}
}

func recordFacts(idMap map[string]string, facts []domain.Fact) {
	for _, f := range facts {
		recordShortID(idMap, f.ID, f.EffectiveShortID())
		recordSkills(idMap, f.Skills)
	}
}

// BuildPrompt constructs a structured prompt payload enforcing the 3-tier rules hierarchy,
// anti-hallucination grounding constraints ("No Lies" invariant), prior active version continuity (ADR-002),
// and serialized Main Profile snapshot data projected with compact short identifiers and PII obfuscated.
//
// Parameters:
//   - req: GenerationRequest containing JobDescription, WritingRules, TargetRole, MainProfile,
//     PriorActiveVersion, PinnedEntityIDs, and ExcludedEntityIDs.
//
// Returns:
//   - PromptPayload with populated SystemPrompt and UserPrompt strings, or an error if generation fails.
func BuildPrompt(req domain.GenerationRequest) (*PromptPayload, error) {
	// Obfuscate candidate contact PII before compiling prompts
	session := privacy.NewSessionFromRequest(req)
	obfReq := session.ObfuscateRequest(req)

	// 1. Build idMap for projecting snapshot, pinned/excluded entities, and prior version
	idMap := buildShortIDMap(obfReq.MainProfile)

	// 2. Render System Prompt from template
	var sysBuilder strings.Builder
	sysBuilder.Grow(2048)
	if err := tailorSystemTmpl.Execute(&sysBuilder, nil); err != nil {
		return nil, fmt.Errorf("failed to render tailor system prompt template: %w", err)
	}

	// 3. Prepare data and render User Prompt from template
	var priorJSONStr string
	if obfReq.PriorVersion != nil {
		projectedPrior := projectPriorVersion(obfReq.PriorVersion, idMap)
		priorJSON, err := json.MarshalIndent(projectedPrior, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to serialize prior version: %w", err)
		}
		priorJSONStr = string(priorJSON)
	}

	profileJSONStr, err := serializeProjectedMainProfile(obfReq.MainProfile, idMap)
	if err != nil {
		return nil, err
	}

	pinned := ""
	if len(obfReq.BranchContext.PinnedEntityIDs) > 0 {
		projectedPinned := make([]string, len(obfReq.BranchContext.PinnedEntityIDs))
		for i, id := range obfReq.BranchContext.PinnedEntityIDs {
			projectedPinned[i] = resolveShortID(id, idMap)
		}
		pinned = strings.Join(projectedPinned, ", ")
	}

	excluded := ""
	if len(obfReq.BranchContext.ExcludedEntityIDs) > 0 {
		projectedExcluded := make([]string, len(obfReq.BranchContext.ExcludedEntityIDs))
		for i, id := range obfReq.BranchContext.ExcludedEntityIDs {
			projectedExcluded[i] = resolveShortID(id, idMap)
		}
		excluded = strings.Join(projectedExcluded, ", ")
	}

	targetLanguage := obfReq.TargetLanguage
	if strings.TrimSpace(targetLanguage) == "" && obfReq.Preset != nil {
		targetLanguage = obfReq.Preset.EffectiveLanguage()
	}
	validatedLanguage, err := domain.ValidateLanguage(targetLanguage)
	if err != nil {
		return nil, fmt.Errorf("invalid prompt target language: %w", err)
	}

	ctx := tailorPromptContext{
		GlobalRules:        strings.TrimSpace(obfReq.GlobalRules),
		PresetInstructions: strings.TrimSpace(obfReq.PresetInstructions),
		TargetLanguage:     validatedLanguage,
		TargetRole:         strings.TrimSpace(obfReq.BranchContext.TargetRole),
		TargetCompany:      strings.TrimSpace(obfReq.BranchContext.TargetCompany),
		TargetRequirements: strings.TrimSpace(obfReq.BranchContext.TargetRequirements),
		BranchInstructions: strings.TrimSpace(obfReq.BranchContext.BranchInstructions),
		PinnedEntities:     pinned,
		ExcludedEntities:   excluded,
		PriorVersionJSON:   priorJSONStr,
		MainProfileJSON:    profileJSONStr,
	}

	var userBuilder strings.Builder
	userBuilder.Grow(8192)
	if err := tailorUserTmpl.Execute(&userBuilder, ctx); err != nil {
		return nil, fmt.Errorf("failed to render tailor user prompt template: %w", err)
	}

	return &PromptPayload{
		SystemPrompt: sysBuilder.String(),
		UserPrompt:   userBuilder.String(),
	}, nil
}

// BuildGapAnalysisPrompt constructs a targeted prompt payload designed exclusively for evaluating
// candidate qualifications against target job requirements, avoiding full resume generation overhead.
func BuildGapAnalysisPrompt(req domain.GenerationRequest) (*PromptPayload, error) {
	// Obfuscate candidate contact PII before compiling prompts
	session := privacy.NewSessionFromRequest(req)
	obfReq := session.ObfuscateRequest(req)

	idMap := buildShortIDMap(obfReq.MainProfile)

	var sysBuilder strings.Builder
	sysBuilder.Grow(1024)
	if err := gapAnalysisSystemTmpl.Execute(&sysBuilder, nil); err != nil {
		return nil, fmt.Errorf("failed to render gap analysis system prompt template: %w", err)
	}

	profileJSONStr, err := serializeProjectedMainProfile(obfReq.MainProfile, idMap)
	if err != nil {
		return nil, err
	}

	ctx := gapAnalysisPromptContext{
		TargetRole:         strings.TrimSpace(obfReq.BranchContext.TargetRole),
		TargetCompany:      strings.TrimSpace(obfReq.BranchContext.TargetCompany),
		TargetRequirements: strings.TrimSpace(obfReq.BranchContext.TargetRequirements),
		BranchInstructions: strings.TrimSpace(obfReq.BranchContext.BranchInstructions),
		MainProfileJSON:    profileJSONStr,
	}

	var userBuilder strings.Builder
	userBuilder.Grow(4096)
	if err := gapAnalysisUserTmpl.Execute(&userBuilder, ctx); err != nil {
		return nil, fmt.Errorf("failed to render gap analysis user prompt template: %w", err)
	}

	return &PromptPayload{
		SystemPrompt: sysBuilder.String(),
		UserPrompt:   userBuilder.String(),
	}, nil
}

type resumeExtractPromptContext struct {
	RawText string
}

// BuildResumeExtractPrompt constructs structured system and user prompts for extracting unstructured resume text
// into standard JSON Resume format, rendering prompts from embedded template files.
func BuildResumeExtractPrompt(rawText string) (*PromptPayload, error) {
	var sysBuilder strings.Builder
	sysBuilder.Grow(1024)
	if err := resumeExtractSystemTmpl.Execute(&sysBuilder, nil); err != nil {
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
		SystemPrompt: sysBuilder.String(),
		UserPrompt:   userBuilder.String(),
	}, nil
}

// buildShortIDMap builds a fast lookup dictionary mapping canonical UUIDs to compact short IDs
// for all entities (skills, experiences, projects, education, certifications, facts) in a profile snapshot.
func buildShortIDMap(snapshot *domain.MainProfileSnapshot) map[string]string {
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

// serializeProjectedMainProfile projects the profile snapshot using short IDs into a lean main profile structure
// and serializes it to formatted JSON, preventing leakage of internal database and multi-tenancy attributes.
func serializeProjectedMainProfile(snapshot *domain.MainProfileSnapshot, idMap map[string]string) (string, error) {
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

// resolveShortID maps a canonical or existing identifier to its compact short equivalent.
// It checks the provided idMap lookup table first; if not found or empty, it falls back
// to deterministically deriving a short identifier via domain.ShortenID.
//
// Parameters:
//   - id: The canonical UUID or identifier string to resolve.
//   - idMap: Map associating canonical and short IDs with their effective short ID representation.
//
// Returns:
//   - The resolved short identifier string, or empty string if input is blank.
func resolveShortID(id string, idMap map[string]string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	if mapped, ok := idMap[trimmed]; ok && mapped != "" {
		return mapped
	}
	return domain.ShortenID(trimmed)
}

// projectPriorBullets transforms a slice of TailoredBullets to compact short IDs.
func projectPriorBullets(bullets []domain.TailoredBullet, idMap map[string]string) []domain.TailoredBullet {
	if len(bullets) == 0 {
		return nil
	}
	res := make([]domain.TailoredBullet, len(bullets))
	for i, b := range bullets {
		var fids []string
		if len(b.SourceFactIDs) > 0 {
			fids = make([]string, len(b.SourceFactIDs))
			for k, fid := range b.SourceFactIDs {
				fids[k] = resolveShortID(fid, idMap)
			}
		}
		var sids []string
		if len(b.SourceSkillIDs) > 0 {
			sids = make([]string, len(b.SourceSkillIDs))
			for k, sid := range b.SourceSkillIDs {
				sids[k] = resolveShortID(sid, idMap)
			}
		}
		res[i] = domain.TailoredBullet{
			Text:           b.Text,
			SourceFactIDs:  fids,
			SourceSkillIDs: sids,
		}
	}
	return res
}

// projectPriorVersion deep-copies and transforms a prior TailoredContent version,
// stripping all non-tailoring contact, supervisory, and location metadata while rewriting all
// experience/project/education/certification source entity IDs and bullet fact/skill citations to
// their corresponding compact short identifiers. Non-tailoring struct and pointer fields are zeroed,
// taking advantage of omitzero and omitempty tags on domain.TailoredContent to prevent data leakage
// across revisions without maintaining duplicate struct definitions.
//
// Parameters:
//   - prior: The baseline tailored content to project. If nil, returns nil.
//   - idMap: Lookup map converting canonical UUIDs to short IDs.
//
// Returns:
//   - A new domain.TailoredContent instance stripped of non-tailoring data and projected to compact short identifiers.
func projectPriorVersion(prior *domain.TailoredContent, idMap map[string]string) *domain.TailoredContent {
	if prior == nil {
		return nil
	}
	projected := &domain.TailoredContent{
		Summary: prior.Summary,
	}

	if len(prior.SkillGroups) > 0 {
		sgs := make([]domain.TailoredSkillGroup, len(prior.SkillGroups))
		for i, sg := range prior.SkillGroups {
			sgs[i] = domain.TailoredSkillGroup{
				Category: sg.Category,
				Skills:   append([]string(nil), sg.Skills...),
			}
			if len(sg.SourceSkillIDs) > 0 {
				sids := make([]string, len(sg.SourceSkillIDs))
				for j, sid := range sg.SourceSkillIDs {
					sids[j] = resolveShortID(sid, idMap)
				}
				sgs[i].SourceSkillIDs = sids
			}
		}
		projected.SkillGroups = sgs
	}

	if len(prior.Experiences) > 0 {
		exps := make([]domain.TailoredExperience, len(prior.Experiences))
		for i, exp := range prior.Experiences {
			exps[i] = domain.TailoredExperience{
				SourceEntityID: resolveShortID(exp.SourceEntityID, idMap),
				Company:        exp.Company,
				RoleTitle:      exp.RoleTitle,
				StartDate:      exp.StartDate,
				EndDate:        exp.EndDate,
				DutiesSummary:  exp.DutiesSummary,
				Bullets:        projectPriorBullets(exp.Bullets, idMap),
			}
		}
		projected.Experiences = exps
	}

	if len(prior.Projects) > 0 {
		projs := make([]domain.TailoredProject, len(prior.Projects))
		for i, proj := range prior.Projects {
			projs[i] = domain.TailoredProject{
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
		projected.Projects = projs
	}

	if len(prior.Education) > 0 {
		edus := make([]domain.TailoredEducation, len(prior.Education))
		for i, edu := range prior.Education {
			edus[i] = domain.TailoredEducation{
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
		projected.Education = edus
	}

	if len(prior.Certifications) > 0 {
		certs := make([]domain.TailoredCert, len(prior.Certifications))
		for i, cert := range prior.Certifications {
			certs[i] = domain.TailoredCert{
				SourceEntityID: resolveShortID(cert.SourceEntityID, idMap),
				Name:           cert.Name,
				Issuer:         cert.Issuer,
				Date:           cert.Date,
				URL:            cert.URL,
			}
		}
		projected.Certifications = certs
	}

	return projected
}


