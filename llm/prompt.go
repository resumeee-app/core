package llm

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"resumeee/internal/domain"
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
	TargetRole         string
	TargetCompany      string
	TargetRequirements string
	BranchInstructions string
	PinnedEntities     string
	ExcludedEntities   string
	PriorVersionJSON   string
	MasterProfileJSON  string
}

type gapAnalysisPromptContext struct {
	TargetRole         string
	TargetCompany      string
	TargetRequirements string
	BranchInstructions string
	MasterProfileJSON  string
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
// and serialized Master Profile snapshot data projected with compact short identifiers.
//
// Parameters:
//   - req: GenerationRequest containing JobDescription, WritingRules, TargetRole, MasterProfile,
//     PriorActiveVersion, PinnedEntityIDs, and ExcludedEntityIDs.
//
// Returns:
//   - PromptPayload with populated SystemPrompt and UserPrompt strings, or an error if generation fails.
func BuildPrompt(req domain.GenerationRequest) (*PromptPayload, error) {
	// 1. Build idMap for projecting snapshot, pinned/excluded entities, and prior version
	idMap := buildShortIDMap(req.MasterProfile)

	// 2. Render System Prompt from template
	var sysBuilder strings.Builder
	sysBuilder.Grow(2048)
	if err := tailorSystemTmpl.Execute(&sysBuilder, nil); err != nil {
		return nil, fmt.Errorf("failed to render tailor system prompt template: %w", err)
	}

	// 3. Prepare data and render User Prompt from template
	var priorJSONStr string
	if req.PriorVersion != nil {
		projectedPrior := projectPriorVersion(req.PriorVersion, idMap)
		priorJSON, err := json.MarshalIndent(projectedPrior, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to serialize prior version: %w", err)
		}
		priorJSONStr = string(priorJSON)
	}

	profileJSONStr, err := serializeProjectedMasterProfile(req.MasterProfile, idMap)
	if err != nil {
		return nil, err
	}

	pinned := ""
	if len(req.BranchContext.PinnedEntityIDs) > 0 {
		projectedPinned := make([]string, len(req.BranchContext.PinnedEntityIDs))
		for i, id := range req.BranchContext.PinnedEntityIDs {
			projectedPinned[i] = resolveShortID(id, idMap)
		}
		pinned = strings.Join(projectedPinned, ", ")
	}

	excluded := ""
	if len(req.BranchContext.ExcludedEntityIDs) > 0 {
		projectedExcluded := make([]string, len(req.BranchContext.ExcludedEntityIDs))
		for i, id := range req.BranchContext.ExcludedEntityIDs {
			projectedExcluded[i] = resolveShortID(id, idMap)
		}
		excluded = strings.Join(projectedExcluded, ", ")
	}

	ctx := tailorPromptContext{
		GlobalRules:        strings.TrimSpace(req.GlobalRules),
		PresetInstructions: strings.TrimSpace(req.PresetInstructions),
		TargetRole:         strings.TrimSpace(req.BranchContext.TargetRole),
		TargetCompany:      strings.TrimSpace(req.BranchContext.TargetCompany),
		TargetRequirements: strings.TrimSpace(req.BranchContext.TargetRequirements),
		BranchInstructions: strings.TrimSpace(req.BranchContext.BranchInstructions),
		PinnedEntities:     pinned,
		ExcludedEntities:   excluded,
		PriorVersionJSON:   priorJSONStr,
		MasterProfileJSON:  profileJSONStr,
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
	idMap := buildShortIDMap(req.MasterProfile)

	var sysBuilder strings.Builder
	sysBuilder.Grow(1024)
	if err := gapAnalysisSystemTmpl.Execute(&sysBuilder, nil); err != nil {
		return nil, fmt.Errorf("failed to render gap analysis system prompt template: %w", err)
	}

	profileJSONStr, err := serializeProjectedMasterProfile(req.MasterProfile, idMap)
	if err != nil {
		return nil, err
	}

	ctx := gapAnalysisPromptContext{
		TargetRole:         strings.TrimSpace(req.BranchContext.TargetRole),
		TargetCompany:      strings.TrimSpace(req.BranchContext.TargetCompany),
		TargetRequirements: strings.TrimSpace(req.BranchContext.TargetRequirements),
		BranchInstructions: strings.TrimSpace(req.BranchContext.BranchInstructions),
		MasterProfileJSON:  profileJSONStr,
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
func buildShortIDMap(snapshot *domain.MasterProfileSnapshot) map[string]string {
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

// serializeProjectedMasterProfile projects the profile snapshot using short IDs and serializes it to formatted JSON.
func serializeProjectedMasterProfile(snapshot *domain.MasterProfileSnapshot, idMap map[string]string) (string, error) {
	if snapshot == nil {
		return "", nil
	}
	projectedProfile := projectMasterProfileSnapshot(snapshot, idMap)
	profileJSON, err := json.MarshalIndent(projectedProfile, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to serialize master profile snapshot: %w", err)
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

// projectPriorVersion deep-copies and transforms a prior TailoredContent version, rewriting all
// experience/project/education/certification source entity IDs and bullet fact/skill citations to
// their corresponding compact short identifiers. This prevents UUID pollution across generation revisions.
//
// Parameters:
//   - prior: The baseline tailored content to project. If nil, returns nil.
//   - idMap: Lookup map converting canonical UUIDs to short IDs.
//
// Returns:
//   - A new TailoredContent instance projected to compact short identifiers.
func projectPriorVersion(prior *domain.TailoredContent, idMap map[string]string) *domain.TailoredContent {
	if prior == nil {
		return nil
	}
	projected := *prior

	if len(prior.SkillGroups) > 0 {
		sgs := make([]domain.TailoredSkillGroup, len(prior.SkillGroups))
		for i, sg := range prior.SkillGroups {
			sgs[i] = sg
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
			exps[i] = exp
			exps[i].SourceEntityID = resolveShortID(exp.SourceEntityID, idMap)
			if len(exp.Bullets) > 0 {
				bullets := make([]domain.TailoredBullet, len(exp.Bullets))
				for j, b := range exp.Bullets {
					bullets[j] = b
					if len(b.SourceFactIDs) > 0 {
						fids := make([]string, len(b.SourceFactIDs))
						for k, fid := range b.SourceFactIDs {
							fids[k] = resolveShortID(fid, idMap)
						}
						bullets[j].SourceFactIDs = fids
					}
					if len(b.SourceSkillIDs) > 0 {
						sids := make([]string, len(b.SourceSkillIDs))
						for k, sid := range b.SourceSkillIDs {
							sids[k] = resolveShortID(sid, idMap)
						}
						bullets[j].SourceSkillIDs = sids
					}
				}
				exps[i].Bullets = bullets
			}
		}
		projected.Experiences = exps
	}

	if len(prior.Projects) > 0 {
		projs := make([]domain.TailoredProject, len(prior.Projects))
		for i, proj := range prior.Projects {
			projs[i] = proj
			projs[i].SourceEntityID = resolveShortID(proj.SourceEntityID, idMap)
			if len(proj.Bullets) > 0 {
				bullets := make([]domain.TailoredBullet, len(proj.Bullets))
				for j, b := range proj.Bullets {
					bullets[j] = b
					if len(b.SourceFactIDs) > 0 {
						fids := make([]string, len(b.SourceFactIDs))
						for k, fid := range b.SourceFactIDs {
							fids[k] = resolveShortID(fid, idMap)
						}
						bullets[j].SourceFactIDs = fids
					}
					if len(b.SourceSkillIDs) > 0 {
						sids := make([]string, len(b.SourceSkillIDs))
						for k, sid := range b.SourceSkillIDs {
							sids[k] = resolveShortID(sid, idMap)
						}
						bullets[j].SourceSkillIDs = sids
					}
				}
				projs[i].Bullets = bullets
			}
		}
		projected.Projects = projs
	}

	if len(prior.Education) > 0 {
		edus := make([]domain.TailoredEducation, len(prior.Education))
		for i, edu := range prior.Education {
			edus[i] = edu
			edus[i].SourceEntityID = resolveShortID(edu.SourceEntityID, idMap)
			if len(edu.Bullets) > 0 {
				bullets := make([]domain.TailoredBullet, len(edu.Bullets))
				for j, b := range edu.Bullets {
					bullets[j] = b
					if len(b.SourceFactIDs) > 0 {
						fids := make([]string, len(b.SourceFactIDs))
						for k, fid := range b.SourceFactIDs {
							fids[k] = resolveShortID(fid, idMap)
						}
						bullets[j].SourceFactIDs = fids
					}
					if len(b.SourceSkillIDs) > 0 {
						sids := make([]string, len(b.SourceSkillIDs))
						for k, sid := range b.SourceSkillIDs {
							sids[k] = resolveShortID(sid, idMap)
						}
						bullets[j].SourceSkillIDs = sids
					}
				}
				edus[i].Bullets = bullets
			}
		}
		projected.Education = edus
	}

	if len(prior.Certifications) > 0 {
		certs := make([]domain.TailoredCert, len(prior.Certifications))
		for i, cert := range prior.Certifications {
			certs[i] = cert
			certs[i].SourceEntityID = resolveShortID(cert.SourceEntityID, idMap)
		}
		projected.Certifications = certs
	}

	return &projected
}

// projectSkillsToShortIDs projects a slice of skills to their EffectiveShortID.
func projectSkillsToShortIDs(skills []domain.Skill) []domain.Skill {
	if len(skills) == 0 {
		return nil
	}
	res := make([]domain.Skill, len(skills))
	for i, s := range skills {
		res[i] = s
		shortID := s.EffectiveShortID()
		res[i].ID = shortID
		res[i].ShortID = shortID
	}
	return res
}

// projectFactsToShortIDs projects a slice of facts and their child skills to their EffectiveShortIDs.
func projectFactsToShortIDs(facts []domain.Fact, ownerShortID string) []domain.Fact {
	if len(facts) == 0 {
		return nil
	}
	res := make([]domain.Fact, len(facts))
	for i, f := range facts {
		res[i] = f
		fShort := f.EffectiveShortID()
		res[i].ID = fShort
		res[i].ShortID = fShort
		if ownerShortID != "" {
			res[i].OwnerID = ownerShortID
		}
		if len(f.Skills) > 0 {
			res[i].Skills = projectSkillsToShortIDs(f.Skills)
		}
	}
	return res
}

// projectMasterProfileSnapshot clones and transforms a MasterProfileSnapshot so that all entities
// and facts (skills, experiences, projects, education, certifications, and child facts) use their
// EffectiveShortID as their primary ID in the prompt payload serialized to the LLM.
//
// Parameters:
//   - snap: MasterProfileSnapshot representing the authoritative master profile state. If nil, returns nil.
//   - idMap: Lookup map associating canonical IDs with their short equivalent for resolving foreign keys (e.g. OwnerID).
//
// Returns:
//   - A new MasterProfileSnapshot instance with all entities and foreign key references projected to short IDs.
func projectMasterProfileSnapshot(snap *domain.MasterProfileSnapshot, idMap map[string]string) *domain.MasterProfileSnapshot {
	if snap == nil {
		return nil
	}
	projected := *snap

	if len(snap.Skills) > 0 {
		projected.Skills = projectSkillsToShortIDs(snap.Skills)
	}

	if len(snap.Experiences) > 0 {
		exps := make([]domain.WorkExperience, len(snap.Experiences))
		for i, exp := range snap.Experiences {
			shortID := exp.EffectiveShortID()
			exps[i] = exp
			exps[i].ID = shortID
			exps[i].ShortID = shortID
			if len(exp.Skills) > 0 {
				exps[i].Skills = projectSkillsToShortIDs(exp.Skills)
			}
			if len(exp.Facts) > 0 {
				exps[i].Facts = projectFactsToShortIDs(exp.Facts, shortID)
			}
		}
		projected.Experiences = exps
	}

	if len(snap.Projects) > 0 {
		projs := make([]domain.Project, len(snap.Projects))
		for i, proj := range snap.Projects {
			shortID := proj.EffectiveShortID()
			projs[i] = proj
			projs[i].ID = shortID
			projs[i].ShortID = shortID
			if len(proj.Skills) > 0 {
				projs[i].Skills = projectSkillsToShortIDs(proj.Skills)
			}
			if len(proj.Facts) > 0 {
				projs[i].Facts = projectFactsToShortIDs(proj.Facts, shortID)
			}
		}
		projected.Projects = projs
	}

	if len(snap.Education) > 0 {
		edus := make([]domain.Education, len(snap.Education))
		for i, edu := range snap.Education {
			shortID := edu.EffectiveShortID()
			edus[i] = edu
			edus[i].ID = shortID
			edus[i].ShortID = shortID
			if len(edu.Facts) > 0 {
				edus[i].Facts = projectFactsToShortIDs(edu.Facts, shortID)
			}
		}
		projected.Education = edus
	}

	if len(snap.Certifications) > 0 {
		certs := make([]domain.Certification, len(snap.Certifications))
		for i, cert := range snap.Certifications {
			shortID := cert.EffectiveShortID()
			certs[i] = cert
			certs[i].ID = shortID
			certs[i].ShortID = shortID
		}
		projected.Certifications = certs
	}

	if len(snap.Facts) > 0 {
		facts := make([]domain.Fact, len(snap.Facts))
		for i, f := range snap.Facts {
			facts[i] = f
			fShort := f.EffectiveShortID()
			facts[i].ID = fShort
			facts[i].ShortID = fShort
			facts[i].OwnerID = resolveShortID(f.OwnerID, idMap)
			if len(f.Skills) > 0 {
				facts[i].Skills = projectSkillsToShortIDs(f.Skills)
			}
		}
		projected.Facts = facts
	}

	return &projected
}
