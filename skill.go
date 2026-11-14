package core

import (
	"strings"

	"github.com/google/uuid"
)

// SkillUsage documents bidirectional skill references across the career graph snapshot,
// detailing which experiences, projects, or facts associate with the skill directly or effectively.
type SkillUsage struct {
	Skill                  Skill    `json:"skill"`
	DirectInExperiences    []string `json:"direct_in_experiences,omitempty"`
	DirectInProjects       []string `json:"direct_in_projects,omitempty"`
	DirectInFacts          []string `json:"direct_in_facts,omitempty"`
	EffectiveInExperiences []string `json:"effective_in_experiences,omitempty"`
	EffectiveInProjects    []string `json:"effective_in_projects,omitempty"`
}

// dedupeDirectAndFactSkills collects skills attached directly and through child facts, then deduplicates them.
func dedupeDirectAndFactSkills(direct []Skill, facts []Fact) []Skill {
	skillsCount := len(direct)
	for _, f := range facts {
		skillsCount += len(f.Skills)
	}
	all := make([]Skill, 0, skillsCount)
	all = append(all, direct...)
	for _, f := range facts {
		all = append(all, f.Skills...)
	}
	return DeduplicateSkills(all)
}

// EffectiveSkills returns the deduplicated union of direct skills and all skills
// associated with nested facts belonging to this WorkExperience.
func (e WorkExperience) EffectiveSkills() []Skill {
	return dedupeDirectAndFactSkills(e.Skills, e.Facts)
}

// EffectiveSkills returns the deduplicated union of direct skills and all skills
// associated with nested facts belonging to this Project.
func (p Project) EffectiveSkills() []Skill {
	return dedupeDirectAndFactSkills(p.Skills, p.Facts)
}

// EffectiveSkills returns the deduplicated skills associated with nested facts belonging to this Education.
func (d Education) EffectiveSkills() []Skill {
	return dedupeDirectAndFactSkills(nil, d.Facts)
}

// EffectiveSkills returns the direct skills assigned to this Fact.
func (f Fact) EffectiveSkills() []Skill {
	if len(f.Skills) == 0 {
		return []Skill{}
	}
	return DeduplicateSkills(f.Skills)
}

// forEachSkill visits every skill in the snapshot across root skills, experiences,
// projects, education facts, and standalone facts in a deterministic order.
func (s *MainProfileSnapshot) forEachSkill(fn func(Skill)) {
	if s == nil {
		return
	}
	for _, skill := range s.Skills {
		fn(skill)
	}
	for _, exp := range s.Experiences {
		for _, skill := range exp.Skills {
			fn(skill)
		}
		for _, f := range exp.Facts {
			for _, skill := range f.Skills {
				fn(skill)
			}
		}
	}
	for _, proj := range s.Projects {
		for _, skill := range proj.Skills {
			fn(skill)
		}
		for _, f := range proj.Facts {
			for _, skill := range f.Skills {
				fn(skill)
			}
		}
	}
	for _, edu := range s.Education {
		for _, f := range edu.Facts {
			for _, skill := range f.Skills {
				fn(skill)
			}
		}
	}
	for _, f := range s.Facts {
		for _, skill := range f.Skills {
			fn(skill)
		}
	}
}

// AllSkills aggregates, canonicalizes, and deduplicates every skill present across the entire snapshot:
// root skills, work experiences (direct & fact skills), projects (direct & fact skills), education fact skills,
// and standalone facts.
func (s *MainProfileSnapshot) AllSkills() []Skill {
	if s == nil {
		return []Skill{}
	}

	var all []Skill
	s.forEachSkill(func(sk Skill) {
		all = append(all, sk)
	})

	return DeduplicateSkills(all)
}

// ConsolidateSkills returns a shallow clone of the snapshot whose Skills field is guaranteed
// to contain the complete, deduplicated set of all skills present anywhere in the career graph.
func (s *MainProfileSnapshot) ConsolidateSkills() *MainProfileSnapshot {
	if s == nil {
		return nil
	}
	clone := *s
	clone.Skills = s.AllSkills()
	return &clone
}

// SkillByID searches all skills in the snapshot (root, experiences, projects, and facts)
// for a skill matching the given ID or ShortID.
func (s *MainProfileSnapshot) SkillByID(id string) (Skill, bool) {
	if s == nil || strings.TrimSpace(id) == "" {
		return Skill{}, false
	}
	target := strings.TrimSpace(id)
	for _, skill := range s.AllSkills() {
		if skill.ID == target || (skill.ShortID != "" && skill.ShortID == target) || skill.EffectiveShortID() == target {
			return skill, true
		}
	}
	return Skill{}, false
}

// SkillsByCategory groups all consolidated skills in the snapshot by their Category.
// Uncategorized skills are grouped under "General Skills".
func (s *MainProfileSnapshot) SkillsByCategory() map[string][]Skill {
	grouped := make(map[string][]Skill)
	if s == nil {
		return grouped
	}
	for _, skill := range s.AllSkills() {
		cat := strings.TrimSpace(skill.Category)
		if cat == "" {
			cat = "General Skills"
		}
		grouped[cat] = append(grouped[cat], skill)
	}
	return grouped
}

// appendUniqueID appends val to slice only when not already present.
func appendUniqueID(slice []string, val string) []string {
	for _, item := range slice {
		if item == val {
			return slice
		}
	}
	return append(slice, val)
}

// recordStandaloneFactsUsage records direct fact references for facts without a parent experience or project.
func recordStandaloneFactsUsage(usages map[string]SkillUsage, facts []Fact) {
	for _, f := range facts {
		for _, sk := range f.Skills {
			if u, exists := usages[sk.ID]; exists {
				u.DirectInFacts = appendUniqueID(u.DirectInFacts, f.ID)
				usages[sk.ID] = u
			}
		}
	}
}

// SkillUsages computes bidirectional associations between each unique skill in the snapshot
// and the entities (experiences, projects, facts) that reference it.
func (s *MainProfileSnapshot) SkillUsages() map[string]SkillUsage {
	usages := make(map[string]SkillUsage)
	if s == nil {
		return usages
	}

	for _, sk := range s.AllSkills() {
		usages[sk.ID] = SkillUsage{
			Skill:                  sk,
			DirectInExperiences:    []string{},
			DirectInProjects:       []string{},
			DirectInFacts:          []string{},
			EffectiveInExperiences: []string{},
			EffectiveInProjects:    []string{},
		}
	}

	for _, exp := range s.Experiences {
		for _, sk := range exp.Skills {
			if u, exists := usages[sk.ID]; exists {
				u.DirectInExperiences = appendUniqueID(u.DirectInExperiences, exp.ID)
				u.EffectiveInExperiences = appendUniqueID(u.EffectiveInExperiences, exp.ID)
				usages[sk.ID] = u
			}
		}
		for _, f := range exp.Facts {
			for _, sk := range f.Skills {
				if u, exists := usages[sk.ID]; exists {
					u.DirectInFacts = appendUniqueID(u.DirectInFacts, f.ID)
					u.EffectiveInExperiences = appendUniqueID(u.EffectiveInExperiences, exp.ID)
					usages[sk.ID] = u
				}
			}
		}
	}

	for _, proj := range s.Projects {
		for _, sk := range proj.Skills {
			if u, exists := usages[sk.ID]; exists {
				u.DirectInProjects = appendUniqueID(u.DirectInProjects, proj.ID)
				u.EffectiveInProjects = appendUniqueID(u.EffectiveInProjects, proj.ID)
				usages[sk.ID] = u
			}
		}
		for _, f := range proj.Facts {
			for _, sk := range f.Skills {
				if u, exists := usages[sk.ID]; exists {
					u.DirectInFacts = appendUniqueID(u.DirectInFacts, f.ID)
					u.EffectiveInProjects = appendUniqueID(u.EffectiveInProjects, proj.ID)
					usages[sk.ID] = u
				}
			}
		}
	}

	for _, edu := range s.Education {
		recordStandaloneFactsUsage(usages, edu.Facts)
	}
	recordStandaloneFactsUsage(usages, s.Facts)

	return usages
}

// DeduplicateSkills removes duplicate skills from a slice based on their canonical ID,
// preserving the original slice order of first appearance.
func DeduplicateSkills(skills []Skill) []Skill {
	if len(skills) == 0 {
		return []Skill{}
	}
	seen := make(map[string]struct{}, len(skills))
	result := make([]Skill, 0, len(skills))
	for _, s := range skills {
		id := strings.TrimSpace(s.ID)
		if id == "" {
			// If ID is missing, fall back to normalized name
			id = "name:" + NormalizeSkillName(s.Name)
		}
		if _, exists := seen[id]; !exists {
			seen[id] = struct{}{}
			result = append(result, s)
		}
	}
	return result
}

// MergeUniqueSkills combines multiple slices of skills into a single slice,
// deduplicating by ID and preserving order of first appearance.
func MergeUniqueSkills(skillSlices ...[]Skill) []Skill {
	total := 0
	for _, sl := range skillSlices {
		total += len(sl)
	}
	combined := make([]Skill, 0, total)
	for _, sl := range skillSlices {
		combined = append(combined, sl...)
	}
	return DeduplicateSkills(combined)
}

// NormalizeSkillName converts a skill name to lowercase and trims leading/trailing whitespace.
func NormalizeSkillName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// FindSkillByName performs a case-insensitive search in a slice of skills by name.
func FindSkillByName(skills []Skill, name string) (Skill, bool) {
	target := NormalizeSkillName(name)
	if target == "" {
		return Skill{}, false
	}
	for _, s := range skills {
		if NormalizeSkillName(s.Name) == target {
			return s, true
		}
	}
	return Skill{}, false
}

// ParseSkillsInput converts raw skill identifier/name strings from user payloads into Skill instances.
// Tokens matching UUID formats (with or without prefixes) route to Skill{ID: ...}.
// Plain text names (e.g. "Kubernetes", "Go") route to Skill{Name: ...} for canonical resolution.
func ParseSkillsInput(skillIDs []string) []Skill {
	if len(skillIDs) == 0 {
		return []Skill{}
	}
	skills := make([]Skill, 0, len(skillIDs))
	for _, s := range skillIDs {
		trimmed := strings.TrimSpace(s)
		if trimmed == "" {
			continue
		}
		rawUUID := strings.TrimPrefix(trimmed, "skill-")
		rawUUID = strings.TrimPrefix(rawUUID, "sk-")
		if _, err := uuid.Parse(rawUUID); err == nil {
			skills = append(skills, Skill{ID: trimmed})
		} else {
			skills = append(skills, Skill{Name: trimmed})
		}
	}
	return skills
}
