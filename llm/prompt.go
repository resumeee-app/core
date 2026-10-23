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

// BuildPrompt constructs a structured prompt payload enforcing the 3-tier rules hierarchy,
// anti-hallucination grounding constraints ("No Lies" invariant), prior active version continuity (ADR-002),
// and serialized Master Profile snapshot data.
func BuildPrompt(req domain.GenerationRequest) (*PromptPayload, error) {
	// 1. Build System Prompt
	var sysBuilder strings.Builder
	sysBuilder.WriteString("You are Resumeee AI, an expert career strategist and resume compiler.\n")
	sysBuilder.WriteString("Your task is to tailor a candidate's resume to a specific target role by selecting, highlighting, ")
	sysBuilder.WriteString("and phrasing achievements grounded STRICTLY in the candidate's verified Master Profile.\n\n")

	sysBuilder.WriteString("<grounding_and_provenance_rules>\n")
	sysBuilder.WriteString("1. Anti-Hallucination (\"No Lies\" Invariant): You must NOT invent, fabricate, or extrapolate any facts, ")
	sysBuilder.WriteString("metrics, dates, companies, titles, degrees, certifications, or skills not explicitly documented in the Master Profile.\n")
	sysBuilder.WriteString("2. Citation Provenance:\n")
	sysBuilder.WriteString("   - Every accomplishment bullet in experiences and projects MUST cite \"source_fact_ids\" containing the exact UUIDs of the Facts from the corresponding parent entity.\n")
	sysBuilder.WriteString("   - You may cite related \"source_skill_ids\" with exact UUIDs of Skills from the Master Profile.\n")
	sysBuilder.WriteString("   - Every TailoredExperience MUST set \"source_entity_id\" to the exact UUID of the corresponding WorkExperience.\n")
	sysBuilder.WriteString("   - Every TailoredProject MUST set \"source_entity_id\" to the exact UUID of the corresponding Project.\n")
	sysBuilder.WriteString("   - Every TailoredEducation MUST set \"source_entity_id\" to the exact UUID of the corresponding Education.\n")
	sysBuilder.WriteString("   - Every TailoredCert MUST set \"source_entity_id\" to the exact UUID of the corresponding Certification.\n")
	sysBuilder.WriteString("   - Any bullet or entity citing an invalid, fabricated, or cross-entity UUID will be deterministically stripped by the backend provenance validator.\n")
	sysBuilder.WriteString("3. Entity Exclusions & Pinnings:\n")
	sysBuilder.WriteString("   - Any entity ID listed under Excluded Entity IDs MUST NOT appear in the tailored resume.\n")
	sysBuilder.WriteString("   - Any entity ID listed under Pinned Entity IDs MUST be included in the tailored resume if present in the Master Profile.\n")
	sysBuilder.WriteString("4. Objective Gap Analysis: In \"gap_report\", objectively assess the candidate's Master Profile against target requirements. ")
	sysBuilder.WriteString("Identify missing or weak qualifications, assign importance (\"critical\", \"preferred\", or \"nice_to_have\"), ")
	sysBuilder.WriteString("and formulate clarifying prompts to help the candidate provide missing experience or metrics.\n")
	sysBuilder.WriteString("</grounding_and_provenance_rules>\n")

	// 2. Build User Prompt with 3-tier rules hierarchy, prior context, and profile data
	var userBuilder strings.Builder

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
		pinned = strings.Join(req.BranchContext.PinnedEntityIDs, ", ")
	}
	fmt.Fprintf(&userBuilder, "Pinned Entity IDs (Must Include): %s\n", pinned)

	excluded := "None"
	if len(req.BranchContext.ExcludedEntityIDs) > 0 {
		excluded = strings.Join(req.BranchContext.ExcludedEntityIDs, ", ")
	}
	fmt.Fprintf(&userBuilder, "Excluded Entity IDs (Must Omit): %s\n", excluded)
	userBuilder.WriteString("</rules_hierarchy>\n\n")

	// Prior Context Continuity (ADR-002)
	userBuilder.WriteString("<prior_active_version>\n")
	if req.PriorVersion != nil {
		userBuilder.WriteString("A prior active tailored version exists for this branch. The user may have manually refined bullet phrasing, word choice, or structure. You must preserve the phrasing, tone, and specific wordings of the prior version bullets whenever they remain grounded in the Master Profile, while updating content to incorporate newly added Master Profile achievements or address revised target requirements.\n\n")
		priorJSON, err := json.MarshalIndent(req.PriorVersion, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("failed to serialize prior version: %w", err)
		}
		userBuilder.WriteString(string(priorJSON))
	} else {
		userBuilder.WriteString("None (Initial generation)")
	}
	userBuilder.WriteString("\n</prior_active_version>\n\n")

	// Master Profile Graph Data
	userBuilder.WriteString("<master_profile_data>\n")
	if req.MasterProfile != nil {
		profileJSON, err := json.MarshalIndent(req.MasterProfile, "", "  ")
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
