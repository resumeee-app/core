package core_test

import (
	"reflect"
	"testing"

	"github.com/resumeee-app/resumeee-core"
)

func TestCareerGraphSnapshotAlias(t *testing.T) {
	cgs := core.CareerGraphSnapshot{
		MainRevision: 42,
		Skills: []core.Skill{
			{ID: "sk-alias", Name: "Go"},
		},
	}
	mps := cgs
	if mps.MainRevision != 42 {
		t.Fatalf("expected MainRevision 42, got %d", mps.MainRevision)
	}
	if len(mps.Skills) != 1 || mps.Skills[0].Name != "Go" {
		t.Fatalf("expected 1 skill Go, got %+v", mps.Skills)
	}
	if len(cgs.AllSkills()) != 1 {
		t.Fatalf("expected AllSkills on CareerGraphSnapshot to return 1 skill, got %d", len(cgs.AllSkills()))
	}
}

func TestWorkExperience_EffectiveSkills(t *testing.T) {
	skillGo := core.Skill{ID: "skill-1", ShortID: "sk-go", Name: "Go"}
	skillDocker := core.Skill{ID: "skill-2", ShortID: "sk-doc", Name: "Docker"}
	skillK8s := core.Skill{ID: "skill-3", ShortID: "sk-k8s", Name: "Kubernetes"}

	tests := []struct {
		name     string
		exp      core.WorkExperience
		expected []string // expected skill IDs
	}{
		{
			name:     "empty skills and empty facts",
			exp:      core.WorkExperience{ID: "exp-1"},
			expected: []string{},
		},
		{
			name: "only direct skills",
			exp: core.WorkExperience{
				ID:     "exp-2",
				Skills: []core.Skill{skillGo, skillDocker},
			},
			expected: []string{"skill-1", "skill-2"},
		},
		{
			name: "only fact skills",
			exp: core.WorkExperience{
				ID: "exp-3",
				Facts: []core.Fact{
					{ID: "fact-1", Skills: []core.Skill{skillDocker}},
					{ID: "fact-2", Skills: []core.Skill{skillK8s}},
				},
			},
			expected: []string{"skill-2", "skill-3"},
		},
		{
			name: "overlapping direct and fact skills deduplicated",
			exp: core.WorkExperience{
				ID:     "exp-4",
				Skills: []core.Skill{skillGo, skillDocker},
				Facts: []core.Fact{
					{ID: "fact-1", Skills: []core.Skill{skillDocker, skillK8s}},
					{ID: "fact-2", Skills: []core.Skill{skillGo}},
				},
			},
			expected: []string{"skill-1", "skill-2", "skill-3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			effective := tc.exp.EffectiveSkills()
			gotIDs := make([]string, 0, len(effective))
			for _, s := range effective {
				gotIDs = append(gotIDs, s.ID)
			}
			if !reflect.DeepEqual(gotIDs, tc.expected) {
				t.Fatalf("expected skill IDs %v, got %v", tc.expected, gotIDs)
			}
		})
	}
}

func TestProject_EffectiveSkills(t *testing.T) {
	skillReact := core.Skill{ID: "skill-react", Name: "React"}
	skillTS := core.Skill{ID: "skill-ts", Name: "TypeScript"}
	skillVite := core.Skill{ID: "skill-vite", Name: "Vite"}

	proj := core.Project{
		ID:     "proj-1",
		Skills: []core.Skill{skillReact, skillTS},
		Facts: []core.Fact{
			{ID: "fact-p1", Skills: []core.Skill{skillTS, skillVite}},
		},
	}

	effective := proj.EffectiveSkills()
	expectedIDs := []string{"skill-react", "skill-ts", "skill-vite"}
	gotIDs := make([]string, 0, len(effective))
	for _, s := range effective {
		gotIDs = append(gotIDs, s.ID)
	}

	if !reflect.DeepEqual(gotIDs, expectedIDs) {
		t.Fatalf("expected project effective skill IDs %v, got %v", expectedIDs, gotIDs)
	}
}

func TestEducation_EffectiveSkills(t *testing.T) {
	skillMath := core.Skill{ID: "skill-math", Name: "Mathematics"}
	skillAlgo := core.Skill{ID: "skill-algo", Name: "Algorithms"}

	edu := core.Education{
		ID: "edu-1",
		Facts: []core.Fact{
			{ID: "fact-e1", Skills: []core.Skill{skillMath}},
			{ID: "fact-e2", Skills: []core.Skill{skillAlgo, skillMath}},
		},
	}

	effective := edu.EffectiveSkills()
	expectedIDs := []string{"skill-math", "skill-algo"}
	gotIDs := make([]string, 0, len(effective))
	for _, s := range effective {
		gotIDs = append(gotIDs, s.ID)
	}

	if !reflect.DeepEqual(gotIDs, expectedIDs) {
		t.Fatalf("expected education effective skill IDs %v, got %v", expectedIDs, gotIDs)
	}
}

func TestFact_EffectiveSkills(t *testing.T) {
	skillSQL := core.Skill{ID: "skill-sql", Name: "SQL"}
	fact := core.Fact{
		ID:     "fact-1",
		Skills: []core.Skill{skillSQL},
	}

	effective := fact.EffectiveSkills()
	if len(effective) != 1 || effective[0].ID != "skill-sql" {
		t.Fatalf("expected fact effective skills to return direct skills, got: %+v", effective)
	}
}

func TestMainProfileSnapshot_AllSkills_And_Consolidation(t *testing.T) {
	skillTop1 := core.Skill{ID: "sk-top-1", ShortID: "sk-1", Name: "Go", Category: "Languages"}
	skillTop2 := core.Skill{ID: "sk-top-2", ShortID: "sk-2", Name: "Rust", Category: "Languages"}
	skillExpDirect := core.Skill{ID: "sk-exp-dir", ShortID: "sk-3", Name: "PostgreSQL", Category: "Databases"}
	skillExpFact := core.Skill{ID: "sk-exp-fact", ShortID: "sk-4", Name: "Kafka", Category: "Messaging"}
	skillProjDirect := core.Skill{ID: "sk-proj-dir", ShortID: "sk-5", Name: "React", Category: "Frontend"}
	skillProjFact := core.Skill{ID: "sk-proj-fact", ShortID: "sk-6", Name: "TailwindCSS", Category: "Frontend"}
	skillEduFact := core.Skill{ID: "sk-edu-fact", ShortID: "sk-7", Name: "Distributed Systems", Category: "Theory"}
	skillStandaloneFact := core.Skill{ID: "sk-stand-fact", ShortID: "sk-8", Name: "Linux", Category: "OS"}

	snap := &core.MainProfileSnapshot{
		Skills: []core.Skill{skillTop1, skillTop2},
		Experiences: []core.WorkExperience{
			{
				ID:     "exp-1",
				Skills: []core.Skill{skillTop1, skillExpDirect}, // skillTop1 is repeated
				Facts: []core.Fact{
					{ID: "fact-1", Skills: []core.Skill{skillExpFact, skillTop2}},
				},
			},
		},
		Projects: []core.Project{
			{
				ID:     "proj-1",
				Skills: []core.Skill{skillProjDirect},
				Facts: []core.Fact{
					{ID: "fact-2", Skills: []core.Skill{skillProjFact}},
				},
			},
		},
		Education: []core.Education{
			{
				ID: "edu-1",
				Facts: []core.Fact{
					{ID: "fact-3", Skills: []core.Skill{skillEduFact}},
				},
			},
		},
		Facts: []core.Fact{
			{ID: "fact-4", Skills: []core.Skill{skillStandaloneFact}},
		},
	}

	all := snap.AllSkills()
	expectedIDs := []string{
		"sk-top-1", "sk-top-2", "sk-exp-dir", "sk-exp-fact",
		"sk-proj-dir", "sk-proj-fact", "sk-edu-fact", "sk-stand-fact",
	}

	gotIDs := make([]string, 0, len(all))
	for _, s := range all {
		gotIDs = append(gotIDs, s.ID)
	}

	if !reflect.DeepEqual(gotIDs, expectedIDs) {
		t.Fatalf("expected all skill IDs %v, got %v", expectedIDs, gotIDs)
	}

	// Test SkillByID
	s, found := snap.SkillByID("sk-top-1")
	if !found || s.Name != "Go" {
		t.Fatalf("expected to find sk-top-1 by canonical ID, got %+v (found=%v)", s, found)
	}
	s, found = snap.SkillByID("sk-4") // by short ID
	if !found || s.Name != "Kafka" {
		t.Fatalf("expected to find sk-exp-fact by short ID sk-4, got %+v (found=%v)", s, found)
	}
	_, found = snap.SkillByID("non-existent")
	if found {
		t.Fatalf("expected false for non-existent skill ID")
	}

	// Test SkillsByCategory
	byCat := snap.SkillsByCategory()
	if len(byCat["Languages"]) != 2 {
		t.Fatalf("expected 2 Languages skills, got %v", byCat["Languages"])
	}
	if len(byCat["Frontend"]) != 2 {
		t.Fatalf("expected 2 Frontend skills, got %v", byCat["Frontend"])
	}

	// Test ConsolidateSkills
	consolidated := snap.ConsolidateSkills()
	if len(consolidated.Skills) != len(expectedIDs) {
		t.Fatalf("expected consolidated snapshot to have %d skills, got %d", len(expectedIDs), len(consolidated.Skills))
	}
}

func TestMainProfileSnapshot_SkillUsages(t *testing.T) {
	skillGo := core.Skill{ID: "skill-go", Name: "Go"}
	skillDB := core.Skill{ID: "skill-db", Name: "PostgreSQL"}

	snap := &core.MainProfileSnapshot{
		Skills: []core.Skill{skillGo, skillDB},
		Experiences: []core.WorkExperience{
			{
				ID:     "exp-1",
				Skills: []core.Skill{skillGo},
				Facts: []core.Fact{
					{ID: "fact-1", OwnerID: "exp-1", Skills: []core.Skill{skillDB}},
				},
			},
		},
		Projects: []core.Project{
			{
				ID:     "proj-1",
				Skills: []core.Skill{skillDB},
				Facts: []core.Fact{
					{ID: "fact-2", OwnerID: "proj-1", Skills: []core.Skill{skillGo}},
				},
			},
		},
		Facts: []core.Fact{
			{ID: "fact-standalone", Skills: []core.Skill{skillGo}},
		},
	}

	usages := snap.SkillUsages()

	goUsage, exists := usages["skill-go"]
	if !exists {
		t.Fatalf("expected skill-go usage to be tracked")
	}
	if !reflect.DeepEqual(goUsage.DirectInExperiences, []string{"exp-1"}) {
		t.Errorf("expected DirectInExperiences [exp-1], got %v", goUsage.DirectInExperiences)
	}
	if !reflect.DeepEqual(goUsage.EffectiveInProjects, []string{"proj-1"}) {
		t.Errorf("expected EffectiveInProjects [proj-1], got %v", goUsage.EffectiveInProjects)
	}
	if len(goUsage.DirectInFacts) != 2 { // fact-2 and fact-standalone
		t.Errorf("expected 2 DirectInFacts for skill-go, got %v", goUsage.DirectInFacts)
	}

	dbUsage, exists := usages["skill-db"]
	if !exists {
		t.Fatalf("expected skill-db usage to be tracked")
	}
	if !reflect.DeepEqual(dbUsage.EffectiveInExperiences, []string{"exp-1"}) {
		t.Errorf("expected EffectiveInExperiences [exp-1] for db, got %v", dbUsage.EffectiveInExperiences)
	}
	if !reflect.DeepEqual(dbUsage.DirectInProjects, []string{"proj-1"}) {
		t.Errorf("expected DirectInProjects [proj-1] for db, got %v", dbUsage.DirectInProjects)
	}
}

func TestSkillUtilities(t *testing.T) {
	s1 := core.Skill{ID: "1", Name: " Go "}
	s2 := core.Skill{ID: "2", Name: "Rust"}
	s3 := core.Skill{ID: "1", Name: "Go Duplicate"}

	// DeduplicateSkills
	dedup := core.DeduplicateSkills([]core.Skill{s1, s2, s3})
	if len(dedup) != 2 || dedup[0].ID != "1" || dedup[1].ID != "2" {
		t.Fatalf("unexpected DeduplicateSkills result: %+v", dedup)
	}

	// MergeUniqueSkills
	merged := core.MergeUniqueSkills([]core.Skill{s1}, []core.Skill{s2, s3})
	if len(merged) != 2 || merged[0].ID != "1" || merged[1].ID != "2" {
		t.Fatalf("unexpected MergeUniqueSkills result: %+v", merged)
	}

	// NormalizeSkillName
	if core.NormalizeSkillName("  Go Lang  ") != "go lang" {
		t.Fatalf("unexpected NormalizeSkillName result")
	}

	// FindSkillByName
	found, ok := core.FindSkillByName([]core.Skill{s1, s2}, "go")
	if !ok || found.ID != "1" {
		t.Fatalf("expected to find skill by case-insensitive name, got %+v (ok=%v)", found, ok)
	}
	_, ok = core.FindSkillByName([]core.Skill{s1, s2}, "nonexistent")
	if ok {
		t.Fatalf("expected false for nonexistent skill name")
	}
}

func TestMainProfileSnapshot_NilReceiverSafety(t *testing.T) {
	var nilSnap *core.MainProfileSnapshot

	all := nilSnap.AllSkills()
	if all == nil || len(all) != 0 {
		t.Fatalf("expected empty non-nil slice from nil AllSkills(), got %v", all)
	}

	byCat := nilSnap.SkillsByCategory()
	if byCat == nil || len(byCat) != 0 {
		t.Fatalf("expected empty non-nil map from nil SkillsByCategory(), got %v", byCat)
	}

	usages := nilSnap.SkillUsages()
	if usages == nil || len(usages) != 0 {
		t.Fatalf("expected empty non-nil map from nil SkillUsages(), got %v", usages)
	}

	if nilSnap.ConsolidateSkills() != nil {
		t.Fatalf("expected nil from nil ConsolidateSkills()")
	}

	_, found := nilSnap.SkillByID("any")
	if found {
		t.Fatalf("expected false from nil SkillByID()")
	}
}

func TestMainProfileSnapshot_EducationFacts_And_GeneralCategoryFallback(t *testing.T) {
	skillUncat := core.Skill{ID: "sk-uncat", Name: "Leadership", Category: ""}
	skillEdu := core.Skill{ID: "sk-edu", Name: "Linear Algebra", Category: "Math"}

	snap := &core.MainProfileSnapshot{
		Skills: []core.Skill{skillUncat},
		Education: []core.Education{
			{
				ID: "edu-1",
				Facts: []core.Fact{
					{ID: "fact-edu-1", Skills: []core.Skill{skillEdu}},
				},
			},
		},
	}

	// SkillsByCategory fallback to General Skills
	byCat := snap.SkillsByCategory()
	if len(byCat["General Skills"]) != 1 || byCat["General Skills"][0].ID != "sk-uncat" {
		t.Fatalf("expected sk-uncat under General Skills, got %v", byCat["General Skills"])
	}

	// SkillUsages tracks education facts
	usages := snap.SkillUsages()
	eduUsage, exists := usages["sk-edu"]
	if !exists {
		t.Fatalf("expected sk-edu usage to be tracked")
	}
	if !reflect.DeepEqual(eduUsage.DirectInFacts, []string{"fact-edu-1"}) {
		t.Fatalf("expected DirectInFacts [fact-edu-1], got %v", eduUsage.DirectInFacts)
	}
}
