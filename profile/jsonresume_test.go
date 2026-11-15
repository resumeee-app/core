package profile_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core"
	"github.com/resumeee-app/resumeee-core/profile"
)

func setupTestService(t *testing.T) (*profile.Service, core.ProfileStore, core.BranchStore) {
	t.Helper()
	store := newFakeProfileStore()
	svc := profile.New(store, store, store)
	return svc, store, store
}

func sampleOfficialJSONResume() string {
	return `{
  "$schema": "https://raw.githubusercontent.com/jsonresume/resume-schema/v1.0.0/schema.json",
  "basics": {
    "name": "Richard Hendriks",
    "label": "Programmer",
    "image": "https://piedpiper.test/richard.jpg",
    "email": "richard.hendriks@piedpiper.test",
    "phone": "(912) 555-4321",
    "url": "http://richardhendriks.example.com",
    "summary": "Richard hails from Tulsa. He has earned degrees from the University of Oklahoma and Stanford.",
    "location": {
      "address": "2712 Broadway St",
      "postalCode": "94115",
      "city": "San Francisco",
      "countryCode": "US",
      "region": "California"
    },
    "profiles": [
      {
        "network": "Twitter",
        "username": "neutralthoughts",
        "url": "https://twitter.com/neutralthoughts"
      },
      {
        "network": "SoundCloud",
        "username": "dandymusicnl",
        "url": "https://soundcloud.com/dandymusicnl"
      }
    ]
  },
  "work": [
    {
      "name": "Pied Piper",
      "position": "CEO/President",
      "url": "http://piedpiper.example.com",
      "startDate": "2013-12-01",
      "endDate": "2014-12-01",
      "summary": "Pied Piper is a multi-platform technology company based on a proprietary compression algorithm.",
      "highlights": [
        "Architected middle-out compression engine in Go and C",
        "Reduced streaming bandwidth consumption by 60% with Kubernetes microservices"
      ],
      "location": "Palo Alto, CA"
    }
  ],
  "volunteer": [
    {
      "organization": "CoderDojo",
      "position": "Teacher",
      "url": "http://coderdojo.example.com/",
      "startDate": "2012-01-01",
      "endDate": "2013-01-01",
      "summary": "Global movement of free coding clubs for young people.",
      "highlights": [
        "Awarded Teacher of the Month"
      ]
    }
  ],
  "education": [
    {
      "institution": "University of Oklahoma",
      "url": "https://www.ou.edu/",
      "area": "Information Technology",
      "studyType": "Bachelor",
      "startDate": "2011-06-01",
      "endDate": "2014-01-01",
      "score": "4.0",
      "courses": [
        "DB1101 - Basic SQL",
        "CS2001 - Distributed Systems"
      ]
    }
  ],
  "awards": [
    {
      "title": "TechCrunch Disrupt Winner",
      "date": "2014-11-01",
      "awarder": "TechCrunch",
      "summary": "Won the TechCrunch Disrupt cup with middle-out compression."
    }
  ],
  "certificates": [
    {
      "name": "Certified Kubernetes Administrator",
      "date": "2020-05-15",
      "issuer": "CNCF",
      "url": "https://cncf.io/cert/cka-123"
    }
  ],
  "publications": [
    {
      "name": "Middle-Out Compression in Modern Networks",
      "publisher": "IEEE Distributed Computing",
      "releaseDate": "2015-08-01",
      "url": "https://ieee.example.com/middle-out",
      "summary": "Deep dive into real-time lossless multi-stream compression."
    }
  ],
  "skills": [
    {
      "name": "Go",
      "level": "Master",
      "keywords": [
        "Golang",
        "Concurrency",
        "Goroutines"
      ]
    },
    {
      "name": "Kubernetes",
      "level": "Advanced",
      "keywords": [
        "K8s",
        "Containers",
        "DevOps"
      ]
    }
  ],
  "languages": [
    {
      "language": "English",
      "fluency": "Native speaker"
    }
  ],
  "interests": [
    {
      "name": "Wildlife",
      "keywords": [
        "Ferrets",
        "Unicorns"
      ]
    }
  ],
  "references": [
    {
      "name": "Erlich Bachman",
      "reference": "Richard is an eccentric genius who built Pied Piper in my incubator."
    }
  ],
  "projects": [
    {
      "name": "Middle-Out Engine",
      "description": "High throughput lossless compression library.",
      "highlights": [
        "Benchmarked 2.8 Weissman score in Go",
        "Deployed to 10,000 edge nodes"
      ],
      "keywords": [
        "Compression",
        "Algorithms",
        "Go"
      ],
      "startDate": "2014-01-01",
      "endDate": "2014-05-01",
      "url": "https://github.com/piedpiper/engine",
      "roles": [
        "Lead Architect",
        "Maintainer"
      ],
      "entity": "Pied Piper Inc",
      "type": "Open Source"
    }
  ],
  "meta": {
    "canonical": "https://jsonresume.org/richard",
    "version": "v1.0.0",
    "lastModified": "2026-10-23T12:00:00Z"
  },
  "customVendorExtension": {
    "badgeId": "gold-tier-123",
    "verified": true
  }
}`
}

func TestJSONResume_SchemaValidationAndParsing(t *testing.T) {
	t.Run("successfully unmarshals official schema sample", func(t *testing.T) {
		var resume core.JSONResume
		raw := sampleOfficialJSONResume()
		if err := json.Unmarshal([]byte(raw), &resume); err != nil {
			t.Fatalf("failed to unmarshal JSON Resume: %v", err)
		}

		if resume.Basics.Name != "Richard Hendriks" {
			t.Errorf("expected name 'Richard Hendriks', got %q", resume.Basics.Name)
		}
		if len(resume.Work) != 1 || resume.Work[0].CompanyName() != "Pied Piper" {
			t.Errorf("work mapping unexpected: %+v", resume.Work)
		}
		if len(resume.Skills) != 2 {
			t.Errorf("expected 2 skills, got %d", len(resume.Skills))
		}
		if len(resume.Volunteer) != 1 || resume.Volunteer[0].Organization != "CoderDojo" {
			t.Errorf("volunteer mapping unexpected: %+v", resume.Volunteer)
		}
		if len(resume.Awards) != 1 || resume.Awards[0].Title != "TechCrunch Disrupt Winner" {
			t.Errorf("awards mapping unexpected: %+v", resume.Awards)
		}
		if len(resume.Certificates) != 1 || resume.Certificates[0].Name != "Certified Kubernetes Administrator" {
			t.Errorf("certificates mapping unexpected: %+v", resume.Certificates)
		}
		if len(resume.Publications) != 1 || resume.Publications[0].Publisher != "IEEE Distributed Computing" {
			t.Errorf("publications mapping unexpected: %+v", resume.Publications)
		}
		if len(resume.Languages) != 1 || resume.Languages[0].Language != "English" {
			t.Errorf("languages mapping unexpected: %+v", resume.Languages)
		}
		if len(resume.Interests) != 1 || resume.Interests[0].Name != "Wildlife" {
			t.Errorf("interests mapping unexpected: %+v", resume.Interests)
		}
		if len(resume.References) != 1 || resume.References[0].Name != "Erlich Bachman" {
			t.Errorf("references mapping unexpected: %+v", resume.References)
		}
		if len(resume.Projects) != 1 || resume.Projects[0].Name != "Middle-Out Engine" {
			t.Errorf("projects mapping unexpected: %+v", resume.Projects)
		}
		if resume.Meta == nil || resume.Meta.Canonical != "https://jsonresume.org/richard" {
			t.Errorf("meta mapping unexpected: %+v", resume.Meta)
		}
		if len(resume.Unmapped) != 1 {
			t.Fatalf("expected 1 unmapped property (customVendorExtension), got %d", len(resume.Unmapped))
		}
		if _, ok := resume.Unmapped["customVendorExtension"]; !ok {
			t.Errorf("missing customVendorExtension in unmapped properties")
		}
	})

	t.Run("supports 'company' synonym in work history", func(t *testing.T) {
		payload := `{
			"basics": {"name": "Synonym Tester"},
			"work": [{"company": "Hooli", "position": "VP"}]
		}`
		var resume core.JSONResume
		if err := json.Unmarshal([]byte(payload), &resume); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if resume.Work[0].CompanyName() != "Hooli" {
			t.Errorf("expected CompanyName() to return 'Hooli', got %q", resume.Work[0].CompanyName())
		}
	})

	t.Run("rejects malformed and empty JSON", func(t *testing.T) {
		svc, _, _ := setupTestService(t)
		ctx := context.Background()

		_, err := svc.ImportJSONResume(ctx, "user-1", []byte(""))
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for empty payload, got %v", err)
		}

		_, err = svc.ImportJSONResume(ctx, "user-1", []byte("{not-json}"))
		if !errors.Is(err, core.ErrValidation) {
			t.Errorf("expected ErrValidation for malformed JSON, got %v", err)
		}
	})
}

func TestJSONResume_EntityAndPolymorphicFactMapping(t *testing.T) {
	raw := sampleOfficialJSONResume()
	var resume core.JSONResume
	if err := json.Unmarshal([]byte(raw), &resume); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}

	snap, err := profile.ToSnapshot(&resume, "user-richard", nil, 1)
	if err != nil {
		t.Fatalf("failed ToSnapshot: %v", err)
	}

	// 1. Verify Profile basics
	if snap.Profile.FullName != "Richard Hendriks" {
		t.Errorf("expected name 'Richard Hendriks', got %q", snap.Profile.FullName)
	}
	if snap.Profile.Email != "richard.hendriks@piedpiper.test" {
		t.Errorf("expected email 'richard.hendriks@piedpiper.test', got %q", snap.Profile.Email)
	}
	if snap.Profile.Location.City != "San Francisco" {
		t.Errorf("expected city 'San Francisco', got %q", snap.Profile.Location.City)
	}
	if len(snap.Profile.Profiles) != 2 {
		t.Errorf("expected 2 social links, got %d", len(snap.Profile.Profiles))
	}

	// 2. Verify Skills
	if len(snap.Skills) != 2 {
		t.Fatalf("expected 2 skills, got %d", len(snap.Skills))
	}
	skillGo := snap.Skills[0]
	if skillGo.Name != "Go" || skillGo.Proficiency != "Master" {
		t.Errorf("unexpected skillGo: %+v", skillGo)
	}

	// 3. Verify Work Experiences & Child Facts
	if len(snap.Experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(snap.Experiences))
	}
	exp := snap.Experiences[0]
	if exp.Company != "Pied Piper" || exp.RoleTitle != "CEO/President" {
		t.Errorf("unexpected exp company/role: %+v", exp)
	}
	if exp.IsCurrent {
		t.Errorf("expected IsCurrent false since endDate is provided")
	}
	if len(exp.Facts) != 2 {
		t.Fatalf("expected 2 child facts for work experience, got %d", len(exp.Facts))
	}

	// Check Fact 1: "Architected middle-out compression engine in Go and C"
	fact1 := exp.Facts[0]
	if fact1.OwnerType != core.OwnerTypeWorkExperience || fact1.OwnerID != exp.ID {
		t.Errorf("fact1 polymorphic association invalid: ownerType=%s, ownerID=%s", fact1.OwnerType, fact1.OwnerID)
	}
	if len(fact1.Skills) == 0 || fact1.Skills[0].Name != "Go" {
		t.Errorf("expected Go skill linked to fact1, got %+v", fact1.Skills)
	}

	// Check Fact 2: "Reduced streaming bandwidth consumption by 60% with Kubernetes microservices"
	fact2 := exp.Facts[1]
	if len(fact2.Skills) == 0 || fact2.Skills[0].Name != "Kubernetes" {
		t.Errorf("expected Kubernetes skill linked to fact2, got %+v", fact2.Skills)
	}

	// Check WorkExperience linked skills (should have both Go and Kubernetes)
	if len(exp.Skills) < 2 {
		t.Errorf("expected at least 2 linked skills on WorkExperience, got %d", len(exp.Skills))
	}

	// 4. Verify Projects & Child Facts
	if len(snap.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(snap.Projects))
	}
	proj := snap.Projects[0]
	if proj.Name != "Middle-Out Engine" {
		t.Errorf("expected project name 'Middle-Out Engine', got %q", proj.Name)
	}
	if len(proj.Facts) != 2 {
		t.Fatalf("expected 2 child facts for project, got %d", len(proj.Facts))
	}
	projFact1 := proj.Facts[0]
	if projFact1.OwnerType != core.OwnerTypeProject || projFact1.OwnerID != proj.ID {
		t.Errorf("projFact1 polymorphic association invalid: ownerType=%s, ownerID=%s", projFact1.OwnerType, projFact1.OwnerID)
	}
	if len(projFact1.Skills) == 0 || projFact1.Skills[0].Name != "Go" {
		t.Errorf("expected Go skill linked to projFact1, got %+v", projFact1.Skills)
	}

	// 5. Verify Education & Certifications
	if len(snap.Education) != 1 || snap.Education[0].Institution != "University of Oklahoma" {
		t.Errorf("unexpected education: %+v", snap.Education)
	}
	if len(snap.Certifications) != 1 || snap.Certifications[0].Name != "Certified Kubernetes Administrator" {
		t.Errorf("unexpected certification: %+v", snap.Certifications)
	}

	// 6. Verify Total Snapshot Facts
	if len(snap.Facts) != 4 {
		t.Errorf("expected 4 total facts across work and projects, got %d", len(snap.Facts))
	}

	// 7. Verify Skill matching word boundary precision
	// "Go" must NOT match "Google" or "going"
	testMatcherSkills := []core.Skill{
		{ID: "sk-go", Name: "Go", Keywords: []string{"Golang"}},
	}
	snapWithGo, err := profile.ToSnapshot(&core.JSONResume{
		Basics: core.JSONResumeBasics{Name: "Boundary Tester"},
		Skills: []core.JSONResumeSkill{{Name: "Go", Keywords: []string{"Golang"}}},
		Work: []core.JSONResumeWork{
			{
				Name: "TestCorp",
				Highlights: []string{
					"Google Cloud deployment going great without Go",
					"Worked with Google Cloud Platform and going to conferences",
					"Built backend service using Golang and Go",
				},
			},
		},
	}, "user-boundary", nil, 1)
	if err != nil {
		t.Fatalf("failed ToSnapshot: %v", err)
	}
	facts := snapWithGo.Experiences[0].Facts
	// Fact 0 has "Google", "going", and "Go" -> should match because of "Go"
	if len(facts[0].Skills) != 1 {
		t.Errorf("expected 1 skill for fact 0 ('Go'), got %d", len(facts[0].Skills))
	}
	// Fact 1 has "Google", "going" -> should NOT match "Go"
	if len(facts[1].Skills) != 0 {
		t.Errorf("expected 0 skills for fact 1 (only contains 'Google' and 'going'), got %d", len(facts[1].Skills))
	}
	// Fact 2 has "Golang" and "Go" -> should match
	if len(facts[2].Skills) != 1 {
		t.Errorf("expected 1 skill for fact 2, got %d", len(facts[2].Skills))
	}
	_ = testMatcherSkills
}

func TestJSONResume_LosslessRoundTrip(t *testing.T) {
	rawInput := sampleOfficialJSONResume()
	var originalResume core.JSONResume
	if err := json.Unmarshal([]byte(rawInput), &originalResume); err != nil {
		t.Fatalf("failed to unmarshal original JSON Resume: %v", err)
	}

	// Ingest to domain snapshot
	snapshot, err := profile.ToSnapshot(&originalResume, "user-lossless", nil, 1)
	if err != nil {
		t.Fatalf("failed ToSnapshot: %v", err)
	}

	// Export back from snapshot
	exportedResume, err := profile.FromSnapshot(snapshot)
	if err != nil {
		t.Fatalf("failed FromSnapshot: %v", err)
	}

	// Assertions comparing originalResume and exportedResume
	if exportedResume.Basics.Name != originalResume.Basics.Name {
		t.Errorf("Basics.Name mismatch: got %q, want %q", exportedResume.Basics.Name, originalResume.Basics.Name)
	}
	if exportedResume.Basics.Email != originalResume.Basics.Email {
		t.Errorf("Basics.Email mismatch: got %q, want %q", exportedResume.Basics.Email, originalResume.Basics.Email)
	}
	if exportedResume.Basics.Image != originalResume.Basics.Image {
		t.Errorf("Basics.Image mismatch: got %q, want %q", exportedResume.Basics.Image, originalResume.Basics.Image)
	}
	if exportedResume.Basics.URL != originalResume.Basics.URL {
		t.Errorf("Basics.URL mismatch: got %q, want %q", exportedResume.Basics.URL, originalResume.Basics.URL)
	}
	if exportedResume.Basics.Location.City != originalResume.Basics.Location.City {
		t.Errorf("Basics.Location.City mismatch: got %q, want %q", exportedResume.Basics.Location.City, originalResume.Basics.Location.City)
	}
	if len(exportedResume.Basics.Profiles) != len(originalResume.Basics.Profiles) {
		t.Errorf("Basics.Profiles count mismatch: got %d, want %d", len(exportedResume.Basics.Profiles), len(originalResume.Basics.Profiles))
	}

	// Work Highlights
	if len(exportedResume.Work) != len(originalResume.Work) {
		t.Fatalf("Work count mismatch: got %d, want %d", len(exportedResume.Work), len(originalResume.Work))
	}
	if exportedResume.Work[0].CompanyName() != originalResume.Work[0].CompanyName() {
		t.Errorf("Work company mismatch: got %q, want %q", exportedResume.Work[0].CompanyName(), originalResume.Work[0].CompanyName())
	}
	if len(exportedResume.Work[0].Highlights) != len(originalResume.Work[0].Highlights) {
		t.Fatalf("Work highlights count mismatch: got %d, want %d", len(exportedResume.Work[0].Highlights), len(originalResume.Work[0].Highlights))
	}
	for i, h := range originalResume.Work[0].Highlights {
		if exportedResume.Work[0].Highlights[i] != h {
			t.Errorf("Work highlight %d mismatch: got %q, want %q", i, exportedResume.Work[0].Highlights[i], h)
		}
	}

	// Projects
	if len(exportedResume.Projects) != len(originalResume.Projects) {
		t.Fatalf("Projects count mismatch: got %d, want %d", len(exportedResume.Projects), len(originalResume.Projects))
	}
	if exportedResume.Projects[0].Name != originalResume.Projects[0].Name {
		t.Errorf("Projects name mismatch: got %q, want %q", exportedResume.Projects[0].Name, originalResume.Projects[0].Name)
	}
	if len(exportedResume.Projects[0].Highlights) != len(originalResume.Projects[0].Highlights) {
		t.Fatalf("Projects highlights count mismatch: got %d, want %d", len(exportedResume.Projects[0].Highlights), len(originalResume.Projects[0].Highlights))
	}
	for i, h := range originalResume.Projects[0].Highlights {
		if exportedResume.Projects[0].Highlights[i] != h {
			t.Errorf("Project highlight %d mismatch: got %q, want %q", i, exportedResume.Projects[0].Highlights[i], h)
		}
	}

	// Education
	if len(exportedResume.Education) != len(originalResume.Education) {
		t.Fatalf("Education count mismatch: got %d, want %d", len(exportedResume.Education), len(originalResume.Education))
	}
	if exportedResume.Education[0].Institution != originalResume.Education[0].Institution {
		t.Errorf("Education institution mismatch: got %q, want %q", exportedResume.Education[0].Institution, originalResume.Education[0].Institution)
	}
	if len(exportedResume.Education[0].Courses) != len(originalResume.Education[0].Courses) {
		t.Errorf("Education courses count mismatch: got %d, want %d", len(exportedResume.Education[0].Courses), len(originalResume.Education[0].Courses))
	}

	// Certificates
	if len(exportedResume.Certificates) != len(originalResume.Certificates) {
		t.Fatalf("Certificates count mismatch: got %d, want %d", len(exportedResume.Certificates), len(originalResume.Certificates))
	}
	if exportedResume.Certificates[0].Name != originalResume.Certificates[0].Name {
		t.Errorf("Certificates name mismatch: got %q, want %q", exportedResume.Certificates[0].Name, originalResume.Certificates[0].Name)
	}

	// Skills
	if len(exportedResume.Skills) != len(originalResume.Skills) {
		t.Fatalf("Skills count mismatch: got %d, want %d", len(exportedResume.Skills), len(originalResume.Skills))
	}

	// Unmapped preserved sections: Volunteer, Awards, Publications, Languages, Interests, References, Meta
	if len(exportedResume.Volunteer) != len(originalResume.Volunteer) || exportedResume.Volunteer[0].Organization != "CoderDojo" {
		t.Errorf("Volunteer not preserved: %+v", exportedResume.Volunteer)
	}
	if len(exportedResume.Awards) != len(originalResume.Awards) || exportedResume.Awards[0].Title != "TechCrunch Disrupt Winner" {
		t.Errorf("Awards not preserved: %+v", exportedResume.Awards)
	}
	if len(exportedResume.Publications) != len(originalResume.Publications) || exportedResume.Publications[0].Publisher != "IEEE Distributed Computing" {
		t.Errorf("Publications not preserved: %+v", exportedResume.Publications)
	}
	if len(exportedResume.Languages) != len(originalResume.Languages) || exportedResume.Languages[0].Language != "English" {
		t.Errorf("Languages not preserved: %+v", exportedResume.Languages)
	}
	if len(exportedResume.Interests) != len(originalResume.Interests) || exportedResume.Interests[0].Name != "Wildlife" {
		t.Errorf("Interests not preserved: %+v", exportedResume.Interests)
	}
	if len(exportedResume.References) != len(originalResume.References) || exportedResume.References[0].Name != "Erlich Bachman" {
		t.Errorf("References not preserved: %+v", exportedResume.References)
	}
	if exportedResume.Meta == nil || exportedResume.Meta.Canonical != originalResume.Meta.Canonical {
		t.Errorf("Meta not preserved: %+v", exportedResume.Meta)
	}

	// Custom vendor root extension
	if len(exportedResume.Unmapped) == 0 {
		t.Fatalf("expected Unmapped root properties to be preserved")
	}
	if val, ok := exportedResume.Unmapped["customVendorExtension"]; !ok || len(val) == 0 {
		t.Errorf("customVendorExtension missing in exported resume unmapped: %+v", exportedResume.Unmapped)
	}

	// Final JSON Marshalling Round-trip
	exportedJSON, err := json.Marshal(exportedResume)
	if err != nil {
		t.Fatalf("failed to marshal exported JSON Resume: %v", err)
	}
	var reParsed core.JSONResume
	if err := json.Unmarshal(exportedJSON, &reParsed); err != nil {
		t.Fatalf("failed to re-unmarshal exported JSON: %v", err)
	}
	if reParsed.Basics.Name != originalResume.Basics.Name {
		t.Errorf("reParsed Basics.Name mismatch: got %q, want %q", reParsed.Basics.Name, originalResume.Basics.Name)
	}
	if _, ok := reParsed.Unmapped["customVendorExtension"]; !ok {
		t.Errorf("customVendorExtension missing after JSON round trip")
	}
}

func TestJSONResume_ServiceImportAndAtomicRevisionBump(t *testing.T) {
	svc, profileRepo, branchRepo := setupTestService(t)
	ctx := context.Background()
	userID := fmt.Sprintf("user-rev-%d", time.Now().UnixNano())

	// 1. First Import: Sets revision = 1
	snap1, err := svc.ImportJSONResume(ctx, userID, []byte(sampleOfficialJSONResume()))
	if err != nil {
		t.Fatalf("first import failed: %v", err)
	}
	if snap1.MainRevision != 1 {
		t.Errorf("expected MainRevision=1 after first import, got %d", snap1.MainRevision)
	}
	if len(snap1.Experiences) != 1 || len(snap1.Facts) != 4 {
		t.Errorf("expected 1 exp and 4 facts in snap1, got %d exps, %d facts", len(snap1.Experiences), len(snap1.Facts))
	}

	// Verify persistence in DB
	profInDB, err := profileRepo.Profile(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get profile from DB: %v", err)
	}
	if profInDB.MainRevision != 1 {
		t.Errorf("expected DB profile MainRevision=1, got %d", profInDB.MainRevision)
	}

	// 2. Add Tier-1 LLM writing rules to profile to verify preservation across subsequent imports
	profInDB.GlobalWritingRules = "Never use passive voice; write impact-first bullets."
	if err := profileRepo.UpsertProfile(ctx, profInDB); err != nil {
		t.Fatalf("failed to upsert profile rules: %v", err)
	}

	// UpsertProfile bumped MainRevision from 1 to 2
	profAfterUpsert, err := profileRepo.Profile(ctx, userID)
	if err != nil {
		t.Fatalf("failed to get profile after upsert: %v", err)
	}
	if profAfterUpsert.MainRevision != 2 {
		t.Errorf("expected MainRevision=2 after UpsertProfile, got %d", profAfterUpsert.MainRevision)
	}

	// 3. Create a branch synced with current MainRevision (2)
	branch := &core.Branch{
		ID:                     "br-test-stale",
		UserID:                 userID,
		Name:                   "Staff Systems Engineer",
		TemplateSetID:          "tmpl-1",
		LastSyncedMainRevision: profAfterUpsert.MainRevision,
	}
	if err := branchRepo.CreateBranch(ctx, branch); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	// At this point, branch is not stale (2 == 2)
	if branch.IsStaleRevision(profAfterUpsert.MainRevision) {
		t.Errorf("branch should not be stale when revision matches")
	}

	// 4. Second Import: Atomically bumps revision to 3
	secondPayload := strings.Replace(sampleOfficialJSONResume(), "Programmer", "Chief Technology Officer", 1)
	snap2, err := svc.ImportJSONResume(ctx, userID, []byte(secondPayload))
	if err != nil {
		t.Fatalf("second import failed: %v", err)
	}
	if snap2.MainRevision != 3 {
		t.Errorf("expected MainRevision=3 after second import, got %d", snap2.MainRevision)
	}
	if snap2.Profile.Headline != "Chief Technology Officer" {
		t.Errorf("expected updated headline 'Chief Technology Officer', got %q", snap2.Profile.Headline)
	}

	// Verify Tier-1 writing rules were preserved
	if snap2.Profile.GlobalWritingRules != "Never use passive voice; write impact-first bullets." {
		t.Errorf("GlobalWritingRules not preserved: %q", snap2.Profile.GlobalWritingRules)
	}

	// 5. Branch staleness check: branch synced at revision 2 is now stale (2 < 3)!
	if !branch.IsStaleRevision(snap2.MainRevision) {
		t.Errorf("expected branch to be marked stale when last_synced_main_revision (2) < current (3)")
	}

	// 6. Third Import: Atomically bumps revision to 4
	snap3, err := svc.ImportJSONResume(ctx, userID, []byte(secondPayload))
	if err != nil {
		t.Fatalf("third import failed: %v", err)
	}
	if snap3.MainRevision != 4 {
		t.Errorf("expected MainRevision=4 after third import, got %d", snap3.MainRevision)
	}
}

func TestJSONResume_ExportFromTailored(t *testing.T) {
	baseProfile := &core.Profile{
		FullName: "Tailored Candidate",
		Headline: "Distributed Systems Specialist",
		Email:    "tailored@example.com",
		Summary:  "Base career summary that should be overridden.",
		Location: core.Location{
			City: "Austin",
		},
		Profiles: []core.SocialLink{
			{Network: "GitHub", Username: "tailored", URL: "https://github.com/tailored"},
		},
	}

	endDate := core.NewDate(2024, 1, 1)
	startDateExp := core.NewDate(2021, 1, 1)
	startDateEdu := core.NewDate(2017, 9, 1)
	certDate := core.NewDate(2023, 8, 1)
	tailoredContent := &core.TailoredContent{
		Summary: "Tailored summary targeting Cloud Infrastructure Architect role.",
		SkillGroups: []core.TailoredSkillGroup{
			{
				Category: "Cloud & Distributed Systems",
				Skills:   []string{"Go", "Kubernetes", "gRPC", "Raft"},
			},
			{
				Category: "Databases & Storage",
				Skills:   []string{"PostgreSQL", "RocksDB", "Redis"},
			},
		},
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Acme Cloud",
				RoleTitle:      "Staff Infrastructure Engineer",
				StartDate:      startDateExp,
				EndDate:        &endDate,
				DutiesSummary:  "Architected distributed control planes.",
				Location:       core.Location{City: "Austin, TX"},
				Bullets: []core.TailoredBullet{
					{Text: "Designed multi-region Raft cluster achieving 99.999% availability."},
					{Text: "Eliminated cross-AZ egress costs by 35% through topology-aware routing."},
				},
			},
		},
		Projects: []core.TailoredProject{
			{
				SourceEntityID: "proj-1",
				Name:           "FastQueue",
				Summary:        "Low latency message broker written in Go.",
				Bullets: []core.TailoredBullet{
					{Text: "Processed 1.2M messages/sec with sub-millisecond p99 latency."},
				},
			},
		},
		Education: []core.TailoredEducation{
			{
				SourceEntityID: "edu-1",
				Institution:    "MIT",
				Degree:         "B.S.",
				Field:          "Computer Science",
				StartDate:      &startDateEdu,
				EndDate:        &endDate,
			},
		},
		Certifications: []core.TailoredCert{
			{
				SourceEntityID: "cert-1",
				Name:           "AWS Certified Solutions Architect - Professional",
				Issuer:         "AWS",
				Date:           &certDate,
			},
		},
	}

	resume, err := profile.FromTailored(baseProfile, tailoredContent)
	if err != nil {
		t.Fatalf("FromTailored failed: %v", err)
	}

	// 1. Basics summary should be overridden by tailored summary
	if resume.Basics.Summary != "Tailored summary targeting Cloud Infrastructure Architect role." {
		t.Errorf("expected tailored summary to override basics summary, got %q", resume.Basics.Summary)
	}
	if resume.Basics.Name != "Tailored Candidate" {
		t.Errorf("expected name 'Tailored Candidate', got %q", resume.Basics.Name)
	}

	// 2. Work bullets converted to highlights
	if len(resume.Work) != 1 {
		t.Fatalf("expected 1 work item, got %d", len(resume.Work))
	}
	if len(resume.Work[0].Highlights) != 2 {
		t.Fatalf("expected 2 highlights, got %d", len(resume.Work[0].Highlights))
	}
	if resume.Work[0].Highlights[0] != "Designed multi-region Raft cluster achieving 99.999% availability." {
		t.Errorf("highlight 0 mismatch: %q", resume.Work[0].Highlights[0])
	}
	if resume.Work[0].EndDate != "2024-01-01" {
		t.Errorf("expected work EndDate '2024-01-01', got %q", resume.Work[0].EndDate)
	}

	// 3. Project bullets converted to highlights
	if len(resume.Projects) != 1 || len(resume.Projects[0].Highlights) != 1 {
		t.Fatalf("unexpected project highlights: %+v", resume.Projects)
	}
	if resume.Projects[0].Highlights[0] != "Processed 1.2M messages/sec with sub-millisecond p99 latency." {
		t.Errorf("project highlight mismatch: %q", resume.Projects[0].Highlights[0])
	}

	// 4. Skills derived from SkillGroups
	if len(resume.Skills) != 2 {
		t.Fatalf("expected 2 categorized skill entries, got %d", len(resume.Skills))
	}
	if resume.Skills[0].Name != "Cloud & Distributed Systems" {
		t.Errorf("expected Skill[0].Name 'Cloud & Distributed Systems', got %q", resume.Skills[0].Name)
	}
	if len(resume.Skills[0].Keywords) != 4 {
		t.Errorf("expected 4 keywords in Skill[0], got %d", len(resume.Skills[0].Keywords))
	}
}

func TestProfileService_ExportJSONResume_WithVersionID(t *testing.T) {
	svc, _, branchRepo := setupTestService(t)
	ctx := context.Background()
	userID := fmt.Sprintf("user-export-%d", time.Now().UnixNano())

	// 1. Ingest Main Profile
	_, err := svc.ImportJSONResume(ctx, userID, []byte(sampleOfficialJSONResume()))
	if err != nil {
		t.Fatalf("failed to import JSON resume: %v", err)
	}

	// 2. Export Mode A: When version_id is nil, exports MainProfileSnapshot
	mainExport, err := svc.ExportJSONResume(ctx, userID, nil)
	if err != nil {
		t.Fatalf("export with nil version_id failed: %v", err)
	}
	if mainExport.Basics.Name != "Richard Hendriks" {
		t.Errorf("expected main export name 'Richard Hendriks', got %q", mainExport.Basics.Name)
	}
	if len(mainExport.Work) != 1 || mainExport.Work[0].CompanyName() != "Pied Piper" {
		t.Errorf("unexpected main export work: %+v", mainExport.Work)
	}

	// 3. Create a Branch and a BranchVersion with TailoredContent
	branch := &core.Branch{
		ID:                     "br-tailored-exp",
		UserID:                 userID,
		Name:                   "Specialist Branch",
		TemplateSetID:          "tmpl-1",
		LastSyncedMainRevision: 1,
	}
	if err := branchRepo.CreateBranch(ctx, branch); err != nil {
		t.Fatalf("failed to create branch: %v", err)
	}

	tailoredSummary := "Custom tailored summary for high-performance computing."
	version := &core.BranchVersion{
		ID:               "ver-tailored-1",
		BranchID:         branch.ID,
		VersionNumber:    1,
		Status:           core.VersionStatusCompleted,
		MainRevisionUsed: 1,
		TailoredContent: &core.TailoredContent{
			Summary: tailoredSummary,
			Experiences: []core.TailoredExperience{
				{
					SourceEntityID: "exp-1",
					Company:        "Pied Piper Tailored",
					RoleTitle:      "Chief Compression Officer",
					StartDate:      core.NewDate(2014, 1, 1),
					Bullets: []core.TailoredBullet{
						{Text: "Tailored bullet for HPC workloads."},
					},
				},
			},
		},
	}
	if err := branchRepo.CreateVersion(ctx, version); err != nil {
		t.Fatalf("failed to create branch version: %v", err)
	}

	// 4. Export Mode B: When version_id is provided, exports tailored content
	verID := "ver-tailored-1"
	tailoredExport, err := svc.ExportJSONResume(ctx, userID, &verID)
	if err != nil {
		t.Fatalf("export with version_id failed: %v", err)
	}
	if tailoredExport.Basics.Summary != tailoredSummary {
		t.Errorf("expected tailored summary %q, got %q", tailoredSummary, tailoredExport.Basics.Summary)
	}
	if len(tailoredExport.Work) != 1 || tailoredExport.Work[0].CompanyName() != "Pied Piper Tailored" {
		t.Errorf("expected tailored work company 'Pied Piper Tailored', got %q", tailoredExport.Work[0].CompanyName())
	}
	if len(tailoredExport.Work[0].Highlights) != 1 || tailoredExport.Work[0].Highlights[0] != "Tailored bullet for HPC workloads." {
		t.Errorf("unexpected tailored highlights: %+v", tailoredExport.Work[0].Highlights)
	}

	// 5. Querying non-existent version_id returns core.ErrNotFound
	invalidVerID := "ver-non-existent"
	_, err = svc.ExportJSONResume(ctx, userID, &invalidVerID)
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("expected ErrNotFound for nonexistent version_id, got %v", err)
	}

	// 6. Cross-tenant access: Querying valid version_id with different user returns ErrNotFound
	_, err = svc.ExportJSONResume(ctx, "different-user-id", &verID)
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("expected ErrNotFound for other user's version_id, got %v", err)
	}
}

func TestJSONResume_Import_GeneratesShortIDs(t *testing.T) {
	svc, _, _ := setupTestService(t)
	ctx := context.Background()
	userID := "import-user-shortids"

	raw := sampleOfficialJSONResume()
	snap, err := svc.ImportJSONResume(ctx, userID, []byte(raw))
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}

	if len(snap.Skills) == 0 {
		t.Fatalf("expected imported skills")
	}
	for _, s := range snap.Skills {
		if s.ShortID == "" {
			t.Errorf("expected skill %q to have non-empty ShortID", s.Name)
		}
		if !strings.HasPrefix(s.ShortID, "skill-") {
			t.Errorf("expected skill short_id to start with 'skill-', got %q", s.ShortID)
		}
	}

	if len(snap.Experiences) == 0 {
		t.Fatalf("expected imported experiences")
	}
	for _, exp := range snap.Experiences {
		if exp.ShortID == "" {
			t.Errorf("expected experience %q to have non-empty ShortID", exp.Company)
		}
		if !strings.HasPrefix(exp.ShortID, "exp-") {
			t.Errorf("expected exp short_id to start with 'exp-', got %q", exp.ShortID)
		}
		for _, f := range exp.Facts {
			if f.ShortID == "" {
				t.Errorf("expected fact to have non-empty ShortID")
			}
			if !strings.HasPrefix(f.ShortID, "fact-") {
				t.Errorf("expected fact short_id to start with 'fact-', got %q", f.ShortID)
			}
		}
	}

	if len(snap.Projects) == 0 {
		t.Fatalf("expected imported projects")
	}
	for _, proj := range snap.Projects {
		if proj.ShortID == "" {
			t.Errorf("expected project %q to have non-empty ShortID", proj.Name)
		}
		if !strings.HasPrefix(proj.ShortID, "proj-") {
			t.Errorf("expected proj short_id to start with 'proj-', got %q", proj.ShortID)
		}
	}

	if len(snap.Education) == 0 {
		t.Fatalf("expected imported education")
	}
	for _, edu := range snap.Education {
		if edu.ShortID == "" {
			t.Errorf("expected education %q to have non-empty ShortID", edu.Institution)
		}
		if !strings.HasPrefix(edu.ShortID, "edu-") {
			t.Errorf("expected edu short_id to start with 'edu-', got %q", edu.ShortID)
		}
	}

	if len(snap.Certifications) == 0 {
		t.Fatalf("expected imported certifications")
	}
	for _, cert := range snap.Certifications {
		if cert.ShortID == "" {
			t.Errorf("expected cert %q to have non-empty ShortID", cert.Name)
		}
		if !strings.HasPrefix(cert.ShortID, "cert-") {
			t.Errorf("expected cert short_id to start with 'cert-', got %q", cert.ShortID)
		}
	}
}

func TestJSONResume_ImportYearOnlyAndExpectedDates(t *testing.T) {
	// Extraction schemas permit "YYYY" and "Expected YYYY" dates; a past role with a year-only
	// end date must not be misclassified as current, and year-only dates must not be dropped.
	rawImport := []byte(`{
		"basics": { "name": "Year Only" },
		"work": [
			{ "name": "Past Corp", "position": "Engineer", "startDate": "2018", "endDate": "2021" }
		],
		"projects": [
			{ "name": "Side Project", "startDate": "2019", "endDate": "2020" }
		],
		"education": [
			{ "institution": "State U", "studyType": "BSc", "area": "CS", "startDate": "2022", "endDate": "Expected 2026" }
		],
		"certificates": [
			{ "name": "CKA", "issuer": "CNCF", "date": "2021" }
		]
	}`)

	var parsed core.JSONResume
	if err := json.Unmarshal(rawImport, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	snap, err := profile.ToSnapshot(&parsed, "user-1", nil, 1)
	if err != nil {
		t.Fatalf("ToSnapshot failed: %v", err)
	}

	if len(snap.Experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(snap.Experiences))
	}
	exp := snap.Experiences[0]
	if exp.IsCurrent {
		t.Error("expected past role with year-only end date to not be current")
	}
	if exp.StartDate.String() != "2018-01-01" {
		t.Errorf("expected experience StartDate '2018-01-01', got %q", exp.StartDate.String())
	}
	if exp.EndDate == nil || exp.EndDate.String() != "2021-01-01" {
		t.Errorf("expected experience EndDate '2021-01-01', got %v", exp.EndDate)
	}

	if len(snap.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(snap.Projects))
	}
	if snap.Projects[0].StartDate == nil || snap.Projects[0].StartDate.String() != "2019-01-01" {
		t.Errorf("expected project StartDate '2019-01-01', got %v", snap.Projects[0].StartDate)
	}
	if snap.Projects[0].EndDate == nil || snap.Projects[0].EndDate.String() != "2020-01-01" {
		t.Errorf("expected project EndDate '2020-01-01', got %v", snap.Projects[0].EndDate)
	}

	if len(snap.Education) != 1 {
		t.Fatalf("expected 1 education, got %d", len(snap.Education))
	}
	if snap.Education[0].EndDate == nil || snap.Education[0].EndDate.String() != "2026-01-01" {
		t.Errorf("expected education EndDate '2026-01-01' from 'Expected 2026', got %v", snap.Education[0].EndDate)
	}

	if len(snap.Certifications) != 1 {
		t.Fatalf("expected 1 certification, got %d", len(snap.Certifications))
	}
	if snap.Certifications[0].Date == nil || snap.Certifications[0].Date.String() != "2021-01-01" {
		t.Errorf("expected certification Date '2021-01-01', got %v", snap.Certifications[0].Date)
	}
}

func TestJSONResume_EndDatePresentHandling(t *testing.T) {
	// 1. Verify Import: JSON Resume with "Present" end date is parsed as ongoing (nil EndDate, isCurrent = true)
	rawImport := []byte(`{
		"basics": { "name": "Current Worker" },
		"work": [
			{
				"name": "Ongoing Corp",
				"position": "Senior Dev",
				"startDate": "2022-01-15",
				"endDate": "Present"
			}
		],
		"projects": [
			{
				"name": "Ongoing Project",
				"startDate": "2023-05-01",
				"endDate": "present"
			}
		],
		"education": [
			{
				"institution": "Ongoing University",
				"startDate": "2021-09-01",
				"endDate": ""
			}
		]
	}`)

	var parsed core.JSONResume
	if err := json.Unmarshal(rawImport, &parsed); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}

	snap, err := profile.ToSnapshot(&parsed, "user-1", nil, 1)
	if err != nil {
		t.Fatalf("ToSnapshot failed: %v", err)
	}

	if len(snap.Experiences) != 1 {
		t.Fatalf("expected 1 experience, got %d", len(snap.Experiences))
	}
	exp := snap.Experiences[0]
	if !exp.IsCurrent {
		t.Errorf("expected exp.IsCurrent to be true when endDate was 'Present'")
	}
	if exp.EndDate != nil {
		t.Errorf("expected exp.EndDate to be nil, got %v", exp.EndDate)
	}
	if exp.StartDate.String() != "2022-01-15" {
		t.Errorf("expected StartDate '2022-01-15', got %q", exp.StartDate.String())
	}

	if len(snap.Projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(snap.Projects))
	}
	if snap.Projects[0].EndDate != nil {
		t.Errorf("expected project EndDate to be nil, got %v", snap.Projects[0].EndDate)
	}

	if len(snap.Education) != 1 {
		t.Fatalf("expected 1 education, got %d", len(snap.Education))
	}
	if snap.Education[0].EndDate != nil {
		t.Errorf("expected education EndDate to be nil, got %v", snap.Education[0].EndDate)
	}

	// 2. Verify Export: Exporting snapshot with ongoing records must NEVER produce "Present" in endDate
	exported, err := profile.FromSnapshot(snap)
	if err != nil {
		t.Fatalf("FromSnapshot failed: %v", err)
	}

	if exported.Work[0].EndDate != "" {
		t.Errorf("expected empty EndDate in exported work, got %q", exported.Work[0].EndDate)
	}
	if exported.Projects[0].EndDate != "" {
		t.Errorf("expected empty EndDate in exported projects, got %q", exported.Projects[0].EndDate)
	}
	if exported.Education[0].EndDate != "" {
		t.Errorf("expected empty EndDate in exported education, got %q", exported.Education[0].EndDate)
	}

	// Also verify JSON serialization does not contain "Present"
	data, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	if strings.Contains(string(data), `"Present"`) || strings.Contains(string(data), `"present"`) {
		t.Errorf("exported JSON Resume must not contain 'Present' in output: %s", string(data))
	}

	// 3. Verify Export from TailoredContent as well
	tailored := &core.TailoredContent{
		Experiences: []core.TailoredExperience{
			{
				SourceEntityID: "exp-1",
				Company:        "Ongoing Corp",
				RoleTitle:      "Senior Dev",
				StartDate:      core.NewDate(2022, 1, 15),
				EndDate:        nil, // ongoing
			},
		},
	}
	exportedTailored, err := profile.FromTailored(&snap.Profile, tailored)
	if err != nil {
		t.Fatalf("FromTailored failed: %v", err)
	}
	if exportedTailored.Work[0].EndDate != "" {
		t.Errorf("expected empty EndDate in tailored exported work, got %q", exportedTailored.Work[0].EndDate)
	}
}

func assertExportedEducationHighlights(t *testing.T, exported *core.JSONResume, wantHighlights []string, wantKeyPresent bool) {
	t.Helper()

	if len(exported.Education) != 1 {
		t.Fatalf("expected 1 exported education entry, got %d", len(exported.Education))
	}
	direct := exported.Education[0].Highlights
	if len(direct) != len(wantHighlights) {
		t.Fatalf("exported.Education[0].Highlights len = %d, want %d (got=%v)", len(direct), len(wantHighlights), direct)
	}
	for i, want := range wantHighlights {
		if direct[i] != want {
			t.Errorf("exported.Education[0].Highlights[%d] = %q, want %q", i, direct[i], want)
		}
	}

	data, err := json.Marshal(exported)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	var rawDoc struct {
		Education []map[string]json.RawMessage `json:"education"`
	}
	if err := json.Unmarshal(data, &rawDoc); err != nil {
		t.Fatalf("json.Unmarshal failed: %v", err)
	}
	if len(rawDoc.Education) != 1 {
		t.Fatalf("expected 1 serialized education entry, got %d", len(rawDoc.Education))
	}

	rawHighlights, keyPresent := rawDoc.Education[0]["highlights"]
	if keyPresent != wantKeyPresent {
		t.Fatalf("highlights key presence = %v, want %v (payload=%s)", keyPresent, wantKeyPresent, string(data))
	}
	if !wantKeyPresent {
		return
	}

	var got []string
	if err := json.Unmarshal(rawHighlights, &got); err != nil {
		t.Fatalf("failed to unmarshal highlights: %v", err)
	}
	if len(got) != len(wantHighlights) {
		t.Fatalf("serialized highlights len = %d, want %d (got=%v)", len(got), len(wantHighlights), got)
	}
	for i, want := range wantHighlights {
		if got[i] != want {
			t.Errorf("serialized highlight[%d] = %q, want %q", i, got[i], want)
		}
	}
}

func TestEducationHighlights_RoundTripAndFactsMapping(t *testing.T) {
	t.Run("Case A ToSnapshot and JSON round-trip", func(t *testing.T) {
		raw := `{
			"basics": {"name": "Ada Lovelace"},
			"skills": [{"name": "go"}],
			"education": [
				{
					"institution": "University of Oxford",
					"url": "https://www.ox.ac.uk/",
					"area": "Mathematics & Computing",
					"studyType": "M.Sc.",
					"startDate": "2018-09-01",
					"endDate": "2020-06-01",
					"score": "4.0",
					"courses": ["CS500 - Distributed Consensus"],
					"highlights": [
						"Summa Cum Laude",
						"Published thesis on Distributed Raft in Go"
					]
				}
			]
		}`

		var resume core.JSONResume
		if err := json.Unmarshal([]byte(raw), &resume); err != nil {
			t.Fatalf("failed to unmarshal JSON Resume: %v", err)
		}

		wantHighlights := []string{
			"Summa Cum Laude",
			"Published thesis on Distributed Raft in Go",
		}
		if len(resume.Education) != 1 || len(resume.Education[0].Highlights) != len(wantHighlights) {
			t.Fatalf("expected unmarshaled education[0].highlights %v, got %+v", wantHighlights, resume.Education)
		}

		// Verify JSON round-trip retains education[0].highlights.
		roundTrippedBytes, err := json.Marshal(resume)
		if err != nil {
			t.Fatalf("failed to marshal JSON Resume: %v", err)
		}
		var roundTripped core.JSONResume
		if err := json.Unmarshal(roundTrippedBytes, &roundTripped); err != nil {
			t.Fatalf("failed to unmarshal round-tripped JSON Resume: %v", err)
		}
		assertExportedEducationHighlights(t, &roundTripped, wantHighlights, true)

		globalSkills := []core.Skill{
			{ID: "global-go", Name: "Go", Slug: "go", IsGlobal: true},
		}
		snap, err := profile.ToSnapshotWithGlobalSkills(&resume, "user-edu", nil, 1, globalSkills)
		if err != nil {
			t.Fatalf("ToSnapshotWithGlobalSkills failed: %v", err)
		}

		if len(snap.Education) != 1 {
			t.Fatalf("expected 1 education, got %d", len(snap.Education))
		}
		edu := snap.Education[0]
		if edu.Score != "4.0" || edu.StartDate == nil || edu.StartDate.String() != "2018-09-01" || edu.EndDate == nil || edu.EndDate.String() != "2020-06-01" {
			t.Errorf("unexpected education scalar/date fields: %+v", edu)
		}
		if edu.Metadata["url"] != "https://www.ox.ac.uk/" {
			t.Errorf("expected education url metadata preserved, got %v", edu.Metadata["url"])
		}
		if courses, ok := edu.Metadata["courses"].([]string); !ok || len(courses) != 1 || courses[0] != "CS500 - Distributed Consensus" {
			t.Errorf("expected education courses metadata preserved, got %#v", edu.Metadata["courses"])
		}
		if len(edu.Facts) != 2 {
			t.Fatalf("expected 2 education facts, got %d", len(edu.Facts))
		}
		if edu.Facts[0].Statement != wantHighlights[0] || edu.Facts[1].Statement != wantHighlights[1] {
			t.Errorf("unexpected education fact statements: %+v", edu.Facts)
		}

		// Verify snap.Facts includes the OwnerTypeEducation facts linked to edu.ID.
		var eduSnapshotFacts []core.Fact
		for _, f := range snap.Facts {
			if f.OwnerType == core.OwnerTypeEducation && f.OwnerID == edu.ID {
				eduSnapshotFacts = append(eduSnapshotFacts, f)
			}
		}
		if len(eduSnapshotFacts) != 2 {
			t.Fatalf("expected 2 OwnerTypeEducation facts in snap.Facts with OwnerID %q, got %d", edu.ID, len(eduSnapshotFacts))
		}

		// Verify skill "Go" is matched on the thesis fact and not on the Latin honors fact.
		if len(edu.Facts[0].Skills) != 0 {
			t.Errorf("expected 0 skills on honors fact, got %+v", edu.Facts[0].Skills)
		}
		if len(edu.Facts[1].Skills) != 1 || edu.Facts[1].Skills[0].Name != "Go" {
			t.Errorf("expected skill 'Go' matched on thesis fact, got %+v", edu.Facts[1].Skills)
		}

		// Verify SnapshotToTailoredContent propagates Education facts to TailoredEducation.Bullets.
		tailored := core.SnapshotToTailoredContent(snap)
		if len(tailored.Education) != 1 {
			t.Fatalf("expected 1 tailored education, got %d", len(tailored.Education))
		}
		bullets := tailored.Education[0].Bullets
		if len(bullets) != 2 {
			t.Fatalf("expected 2 tailored education bullets, got %d", len(bullets))
		}
		for i, want := range wantHighlights {
			if bullets[i].Text != want {
				t.Errorf("tailored education bullet[%d] = %q, want %q", i, bullets[i].Text, want)
			}
		}

		exported, err := profile.FromSnapshot(snap)
		if err != nil {
			t.Fatalf("FromSnapshot failed: %v", err)
		}
		assertExportedEducationHighlights(t, exported, wantHighlights, true)
	})

	t.Run("Case B FromSnapshot education facts and fallback", func(t *testing.T) {
		tests := []struct {
			name           string
			snapshot       *core.MainProfileSnapshot
			wantHighlights []string
			wantKeyPresent bool
		}{
			{
				name: "nested Education.Facts populated",
				snapshot: &core.MainProfileSnapshot{
					Profile: core.Profile{FullName: "Ada Lovelace"},
					Education: []core.Education{
						{
							ID:          "edu-1",
							Institution: "University of Oxford",
							Degree:      "M.Sc.",
							Field:       "Mathematics & Computing",
							Facts: []core.Fact{
								{ID: "f-1", OwnerID: "edu-1", OwnerType: core.OwnerTypeEducation, Statement: "Summa Cum Laude"},
								{ID: "f-2", OwnerID: "edu-1", OwnerType: core.OwnerTypeEducation, Statement: "Published thesis on Distributed Raft in Go"},
							},
						},
					},
				},
				wantHighlights: []string{
					"Summa Cum Laude",
					"Published thesis on Distributed Raft in Go",
				},
				wantKeyPresent: true,
			},
			{
				name: "fallback to Snapshot.Facts when Education.Facts empty",
				snapshot: &core.MainProfileSnapshot{
					Profile: core.Profile{FullName: "Ada Lovelace"},
					Education: []core.Education{
						{
							ID:          "edu-1",
							Institution: "University of Oxford",
							Degree:      "M.Sc.",
							Field:       "Mathematics & Computing",
						},
					},
					Facts: []core.Fact{
						{ID: "f-work", OwnerID: "edu-1", OwnerType: core.OwnerTypeWorkExperience, Statement: "Should not leak into education"},
						{ID: "f-1", OwnerID: "edu-1", OwnerType: core.OwnerTypeEducation, Statement: "First Fallback Highlight"},
						{ID: "f-other", OwnerID: "edu-2", OwnerType: core.OwnerTypeEducation, Statement: "Belongs to another degree"},
						{ID: "f-2", OwnerID: "edu-1", OwnerType: core.OwnerTypeEducation, Statement: "Second Fallback Highlight"},
					},
				},
				wantHighlights: []string{
					"First Fallback Highlight",
					"Second Fallback Highlight",
				},
				wantKeyPresent: true,
			},
			{
				name: "empty facts omits highlights cleanly",
				snapshot: &core.MainProfileSnapshot{
					Profile: core.Profile{FullName: "Ada Lovelace"},
					Education: []core.Education{
						{
							ID:          "edu-1",
							Institution: "University of Oxford",
							Degree:      "M.Sc.",
							Field:       "Mathematics & Computing",
						},
					},
				},
				wantHighlights: nil,
				wantKeyPresent: false,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				exported, err := profile.FromSnapshot(tc.snapshot)
				if err != nil {
					t.Fatalf("FromSnapshot failed: %v", err)
				}
				assertExportedEducationHighlights(t, exported, tc.wantHighlights, tc.wantKeyPresent)
			})
		}
	})

	t.Run("Case C FromTailored education bullets", func(t *testing.T) {
		baseProfile := &core.Profile{FullName: "Ada Lovelace"}
		tests := []struct {
			name           string
			bullets        []core.TailoredBullet
			wantHighlights []string
			wantKeyPresent bool
		}{
			{
				name: "populated TailoredEducation.Bullets",
				bullets: []core.TailoredBullet{
					{Text: "Summa Cum Laude", SourceFactIDs: []string{"f-1"}},
					{Text: "Published thesis on Distributed Raft in Go", SourceFactIDs: []string{"f-2"}},
				},
				wantHighlights: []string{
					"Summa Cum Laude",
					"Published thesis on Distributed Raft in Go",
				},
				wantKeyPresent: true,
			},
			{
				name:           "nil Bullets omits highlights",
				bullets:        nil,
				wantHighlights: nil,
				wantKeyPresent: false,
			},
			{
				name:           "empty Bullets omits highlights",
				bullets:        []core.TailoredBullet{},
				wantHighlights: nil,
				wantKeyPresent: false,
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				tailored := &core.TailoredContent{
					Education: []core.TailoredEducation{
						{
							SourceEntityID: "edu-1",
							Institution:    "University of Oxford",
							Degree:         "M.Sc.",
							Field:          "Mathematics & Computing",
							Bullets:        tc.bullets,
						},
					},
				}

				exported, err := profile.FromTailored(baseProfile, tailored)
				if err != nil {
					t.Fatalf("FromTailored failed: %v", err)
				}
				assertExportedEducationHighlights(t, exported, tc.wantHighlights, tc.wantKeyPresent)
			})
		}
	})
}
