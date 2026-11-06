package llm

import (
	"encoding/json"
	"fmt"
	"strings"

	"resumeee/internal/domain"
)

// PromptPayload wraps the assembled system and user prompts ready for LLM consumption.
type PromptPayload struct {
	SystemPrompt string
	UserPrompt   string
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
	idMap := make(map[string]string)
	if req.MasterProfile != nil {
		recordSkills(idMap, req.MasterProfile.AllSkills())
		for _, exp := range req.MasterProfile.Experiences {
			recordShortID(idMap, exp.ID, exp.EffectiveShortID())
			recordFacts(idMap, exp.Facts)
		}
		for _, proj := range req.MasterProfile.Projects {
			recordShortID(idMap, proj.ID, proj.EffectiveShortID())
			recordFacts(idMap, proj.Facts)
		}
		for _, edu := range req.MasterProfile.Education {
			recordShortID(idMap, edu.ID, edu.EffectiveShortID())
			recordFacts(idMap, edu.Facts)
		}
		for _, cert := range req.MasterProfile.Certifications {
			recordShortID(idMap, cert.ID, cert.EffectiveShortID())
		}
		recordFacts(idMap, req.MasterProfile.Facts)
	}

	// 2. Build System Prompt
	var sysBuilder strings.Builder
	sysBuilder.Grow(2048)
	sysBuilder.WriteString("You are Resumeee AI, an expert career strategist and resume compiler.\n")
	sysBuilder.WriteString("Your task is to tailor a candidate's resume to a specific target role by selecting, highlighting, ")
	sysBuilder.WriteString("and phrasing achievements grounded STRICTLY in the candidate's verified Master Profile.\n\n")

	sysBuilder.WriteString("<grounding_and_provenance_rules>\n")
	sysBuilder.WriteString("1. Anti-Hallucination (\"No Lies\" Invariant): You must NOT invent, fabricate, or extrapolate any facts, ")
	sysBuilder.WriteString("metrics, dates, companies, titles, degrees, certifications, or skills not explicitly documented in the Master Profile.\n")
	sysBuilder.WriteString("2. Citation Provenance:\n")
	sysBuilder.WriteString("   - Every accomplishment bullet in experiences and projects MUST cite \"source_fact_ids\" containing the short identifiers (e.g., fact-1, fact-2) of the Facts from the corresponding parent entity.\n")
	sysBuilder.WriteString("   - You may cite related \"source_skill_ids\" with short identifiers (e.g., skill-1) of Skills from the Master Profile.\n")
	sysBuilder.WriteString("   - Every TailoredExperience MUST set \"source_entity_id\" to the short identifier (e.g., exp-1) of the corresponding WorkExperience.\n")
	sysBuilder.WriteString("   - Every TailoredProject MUST set \"source_entity_id\" to the short identifier (e.g., proj-1) of the corresponding Project.\n")
	sysBuilder.WriteString("   - Every TailoredEducation MUST set \"source_entity_id\" to the short identifier (e.g., edu-1) of the corresponding Education.\n")
	sysBuilder.WriteString("   - Every TailoredCert MUST set \"source_entity_id\" to the short identifier (e.g., cert-1) of the corresponding Certification.\n")
	sysBuilder.WriteString("   - Any bullet or entity citing an invalid, fabricated, or cross-entity short identifier will be deterministically stripped by the backend provenance validator.\n")
	sysBuilder.WriteString("3. Entity Exclusions & Pinnings:\n")
	sysBuilder.WriteString("   - Any entity ID listed under Excluded Entity IDs MUST NOT appear in the tailored resume.\n")
	sysBuilder.WriteString("   - Any entity ID listed under Pinned Entity IDs MUST be included in the tailored resume if present in the Master Profile.\n")
	sysBuilder.WriteString("4. Objective Gap Analysis: In \"gap_report\", objectively assess the candidate's Master Profile against target requirements.\n")
	sysBuilder.WriteString("   - Compare target requirements strictly against the candidate's verified Master Profile graph.\n")
	sysBuilder.WriteString("   - Do NOT flag a qualification as missing if it is present or directly implied by an existing Fact or Skill in the Master Profile.\n")
	sysBuilder.WriteString("   - Do NOT invent or extrapolate requirements that were not stated or clearly implied by the Target Requirements.\n")
	sysBuilder.WriteString("   - For every gap, provide a \"missing_area\" category, an \"importance\" (\"critical\", \"preferred\", or \"nice_to_have\"), a \"clarifying_prompt\", and an actionable \"recommended_action\".\n")
	sysBuilder.WriteString("</grounding_and_provenance_rules>\n")

	// 3. Build User Prompt with 3-tier rules hierarchy, prior context, and profile data
	var userBuilder strings.Builder
	userBuilder.Grow(8192)

	userBuilder.WriteString("<rules_hierarchy>\n")
	userBuilder.WriteString("Tier 1: Global Writing Rules:\n")
	if strings.TrimSpace(req.GlobalRules) != "" {
		userBuilder.WriteString(strings.TrimSpace(req.GlobalRules))
	} else {
		userBuilder.WriteString("None")
	}
	userBuilder.WriteString("\n\n")

	userBuilder.WriteString("Tier 2: Preset Formatting Instructions:\n")
	if strings.TrimSpace(req.PresetInstructions) != "" {
		userBuilder.WriteString(strings.TrimSpace(req.PresetInstructions))
	} else {
		userBuilder.WriteString("None")
	}
	userBuilder.WriteString("\n\n")

	userBuilder.WriteString("Tier 3: Branch Target & Custom Instructions:\n")
	targetRole := strings.TrimSpace(req.BranchContext.TargetRole)
	if targetRole == "" {
		targetRole = "Not specified"
	}
	fmt.Fprintf(&userBuilder, "Target Role: %s\n", targetRole)

	targetCompany := strings.TrimSpace(req.BranchContext.TargetCompany)
	if targetCompany == "" {
		targetCompany = "Not specified"
	}
	fmt.Fprintf(&userBuilder, "Target Company: %s\n", targetCompany)

	userBuilder.WriteString("Target Requirements:\n")
	if strings.TrimSpace(req.BranchContext.TargetRequirements) != "" {
		userBuilder.WriteString(strings.TrimSpace(req.BranchContext.TargetRequirements))
	} else {
		userBuilder.WriteString("None")
	}
	userBuilder.WriteString("\n")

	userBuilder.WriteString("Custom Branch Instructions:\n")
	if strings.TrimSpace(req.BranchContext.BranchInstructions) != "" {
		userBuilder.WriteString(strings.TrimSpace(req.BranchContext.BranchInstructions))
	} else {
		userBuilder.WriteString("None")
	}
	userBuilder.WriteString("\n")

	pinned := "None"
	if len(req.BranchContext.PinnedEntityIDs) > 0 {
		projectedPinned := make([]string, len(req.BranchContext.PinnedEntityIDs))
		for i, id := range req.BranchContext.PinnedEntityIDs {
			projectedPinned[i] = resolveShortID(id, idMap)
		}
		pinned = strings.Join(projectedPinned, ", ")
	}
	fmt.Fprintf(&userBuilder, "Pinned Entity IDs (Must Include): %s\n", pinned)

	excluded := "None"
	if len(req.BranchContext.ExcludedEntityIDs) > 0 {
		projectedExcluded := make([]string, len(req.BranchContext.ExcludedEntityIDs))
		for i, id := range req.BranchContext.ExcludedEntityIDs {
			projectedExcluded[i] = resolveShortID(id, idMap)
		}
		excluded = strings.Join(projectedExcluded, ", ")
	}
	fmt.Fprintf(&userBuilder, "Excluded Entity IDs (Must Omit): %s\n", excluded)
	userBuilder.WriteString("</rules_hierarchy>\n\n")

	// Prior Context Continuity (ADR-002)
	userBuilder.WriteString("<prior_active_version>\n")
	if req.PriorVersion != nil {
		userBuilder.WriteString("A prior active tailored version exists for this branch. The user may have manually refined bullet phrasing, word choice, or structure. You must preserve the phrasing, tone, and specific wordings of the prior version bullets whenever they remain grounded in the Master Profile, while updating content to incorporate newly added Master Profile achievements or address revised target requirements.\n\n")
		projectedPrior := projectPriorVersion(req.PriorVersion, idMap)
		priorJSON, err := json.MarshalIndent(projectedPrior, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to serialize prior version: %w", err)
		}
		userBuilder.WriteString(string(priorJSON))
	} else {
		userBuilder.WriteString("None (Initial generation)")
	}
	userBuilder.WriteString("\n</prior_active_version>\n\n")

	// Master Profile Graph Data projected with short identifiers
	userBuilder.WriteString("<master_profile_data>\n")
	if req.MasterProfile != nil {
		projectedProfile := projectMasterProfileSnapshot(req.MasterProfile, idMap)
		profileJSON, err := json.MarshalIndent(projectedProfile, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to serialize master profile snapshot: %w", err)
		}
		userBuilder.WriteString(string(profileJSON))
	} else {
		userBuilder.WriteString("{}\n")
	}
	userBuilder.WriteString("\n</master_profile_data>\n\n")

	userBuilder.WriteString("Produce a valid JSON object matching the required schema with tailored_content and gap_report. Adhere strictly to the No Lies grounding rules.")

	return &PromptPayload{
		SystemPrompt: sysBuilder.String(),
		UserPrompt:   userBuilder.String(),
	}, nil
}

// BuildGapAnalysisPrompt constructs a targeted prompt payload designed exclusively for evaluating
// candidate qualifications against target job requirements, avoiding full resume generation overhead.
func BuildGapAnalysisPrompt(req domain.GenerationRequest) (*PromptPayload, error) {
	idMap := make(map[string]string)
	if req.MasterProfile != nil {
		recordSkills(idMap, req.MasterProfile.AllSkills())
		for _, exp := range req.MasterProfile.Experiences {
			recordShortID(idMap, exp.ID, exp.EffectiveShortID())
			recordFacts(idMap, exp.Facts)
		}
		for _, proj := range req.MasterProfile.Projects {
			recordShortID(idMap, proj.ID, proj.EffectiveShortID())
			recordFacts(idMap, proj.Facts)
		}
		for _, edu := range req.MasterProfile.Education {
			recordShortID(idMap, edu.ID, edu.EffectiveShortID())
			recordFacts(idMap, edu.Facts)
		}
		for _, cert := range req.MasterProfile.Certifications {
			recordShortID(idMap, cert.ID, cert.EffectiveShortID())
		}
		recordFacts(idMap, req.MasterProfile.Facts)
	}

	var sysBuilder strings.Builder
	sysBuilder.Grow(1024)
	sysBuilder.WriteString("You are Resumeee AI, an expert career strategist and technical recruiter.\n")
	sysBuilder.WriteString("Your task is to perform an objective, rigorous gap analysis comparing a candidate's verified Master Profile against specific target role requirements.\n\n")

	sysBuilder.WriteString("<grounding_and_provenance_rules>\n")
	sysBuilder.WriteString("1. Anti-Hallucination (\"No Lies\" Invariant): You must evaluate the candidate strictly against their verified Master Profile graph.\n")
	sysBuilder.WriteString("2. Objective Gap Analysis: In \"gap_report\", objectively assess the candidate's Master Profile against target requirements.\n")
	sysBuilder.WriteString("   - Compare target requirements strictly against the candidate's verified Master Profile graph.\n")
	sysBuilder.WriteString("   - Do NOT flag a qualification as missing if it is present or directly implied by an existing Fact or Skill in the Master Profile.\n")
	sysBuilder.WriteString("   - Do NOT invent or extrapolate requirements that were not stated or clearly implied by the Target Requirements.\n")
	sysBuilder.WriteString("   - For every gap, provide a \"missing_area\" category, an \"importance\" (\"critical\", \"preferred\", or \"nice_to_have\"), a \"clarifying_prompt\", and an actionable \"recommended_action\".\n")
	sysBuilder.WriteString("   - In \"overall_match_summary\", summarize alignment with the target requirements.\n")
	sysBuilder.WriteString("   - In \"strategic_recommendations\", provide actionable advice for addressing gaps or framing experience.\n")
	sysBuilder.WriteString("</grounding_and_provenance_rules>\n")

	var userBuilder strings.Builder
	userBuilder.Grow(4096)

	userBuilder.WriteString("<target_context>\n")
	targetRole := strings.TrimSpace(req.BranchContext.TargetRole)
	if targetRole == "" {
		targetRole = "Not specified"
	}
	fmt.Fprintf(&userBuilder, "Target Role: %s\n", targetRole)

	targetCompany := strings.TrimSpace(req.BranchContext.TargetCompany)
	if targetCompany == "" {
		targetCompany = "Not specified"
	}
	fmt.Fprintf(&userBuilder, "Target Company: %s\n", targetCompany)

	userBuilder.WriteString("Target Requirements:\n")
	if strings.TrimSpace(req.BranchContext.TargetRequirements) != "" {
		userBuilder.WriteString(strings.TrimSpace(req.BranchContext.TargetRequirements))
	} else {
		userBuilder.WriteString("None")
	}
	userBuilder.WriteString("\n")

	if strings.TrimSpace(req.BranchContext.BranchInstructions) != "" {
		userBuilder.WriteString("Custom Branch Instructions:\n")
		userBuilder.WriteString(strings.TrimSpace(req.BranchContext.BranchInstructions))
		userBuilder.WriteString("\n")
	}
	userBuilder.WriteString("</target_context>\n\n")

	userBuilder.WriteString("<master_profile_data>\n")
	if req.MasterProfile != nil {
		projectedProfile := projectMasterProfileSnapshot(req.MasterProfile, idMap)
		profileJSON, err := json.MarshalIndent(projectedProfile, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to serialize master profile snapshot: %w", err)
		}
		userBuilder.WriteString(string(profileJSON))
	} else {
		userBuilder.WriteString("{}\n")
	}
	userBuilder.WriteString("\n</master_profile_data>\n\n")

	userBuilder.WriteString("Produce a valid JSON object matching the required schema with gap_report. Adhere strictly to the No Lies grounding rules.")

	return &PromptPayload{
		SystemPrompt: sysBuilder.String(),
		UserPrompt:   userBuilder.String(),
	}, nil
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
