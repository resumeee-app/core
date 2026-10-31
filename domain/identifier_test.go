package domain_test

import (
	"strings"
	"testing"

	"resumeee/internal/domain"
)

// TestGenerateShortID_Prefixes verifies that GenerateShortID attaches the correct entity prefixes,
// satisfies length bounds (6-8 alphanumeric characters plus prefix), and generates unique outputs.
func TestGenerateShortID_Prefixes(t *testing.T) {
	prefixes := []string{"exp", "fact", "skill", "proj", "edu", "cert"}
	seen := make(map[string]bool)

	for _, prefix := range prefixes {
		t.Run("Prefix_"+prefix, func(t *testing.T) {
			id := domain.GenerateShortID(prefix)
			if !strings.HasPrefix(id, prefix+"-") {
				t.Fatalf("expected prefix %q-, got %q", prefix, id)
			}
			// Length should be prefix length + 1 (hyphen) + 6-8 chars
			expectedMinLen := len(prefix) + 1 + 6
			expectedMaxLen := len(prefix) + 1 + 8
			if len(id) < expectedMinLen || len(id) > expectedMaxLen {
				t.Fatalf("unexpected length for short ID %q: got %d, expected between %d and %d", id, len(id), expectedMinLen, expectedMaxLen)
			}
			if seen[id] {
				t.Fatalf("duplicate short ID generated: %q", id)
			}
			seen[id] = true
		})
	}
}

// TestGenerateID_Prefixes verifies that GenerateID constructs canonical identifiers composed of
// the entity prefix and a 36-character hyphenated UUIDv4.
func TestGenerateID_Prefixes(t *testing.T) {
	prefixes := []string{"exp", "fact", "skill", "proj", "edu", "cert"}
	for _, prefix := range prefixes {
		t.Run("Prefix_"+prefix, func(t *testing.T) {
			id := domain.GenerateID(prefix)
			if !strings.HasPrefix(id, prefix+"-") {
				t.Fatalf("expected prefix %q-, got %q", prefix, id)
			}
			// UUID is 36 chars, plus prefix and hyphen
			expectedLen := len(prefix) + 1 + 36
			if len(id) != expectedLen {
				t.Fatalf("expected length %d for canonical ID %q, got %d", expectedLen, id, len(id))
			}
		})
	}
}

// TestShortenID validates the deterministic shortening behavior across various input formats
// including empty strings, already compact IDs, custom short slugs, prefixed long UUIDs, and bare UUIDs.
func TestShortenID(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Empty",
			input:    "",
			expected: "",
		},
		{
			name:     "AlreadyShort",
			input:    "exp-1",
			expected: "exp-1",
		},
		{
			name:     "CustomShortSlug",
			input:    "skill-golang",
			expected: "skill-golang",
		},
		{
			name:     "PrefixedLongUUID",
			input:    "exp-9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
			expected: "exp-9b1deb4d",
		},
		{
			name:     "PrefixedLongUUID_Fact",
			input:    "fact-3db884b2-f155-442b-9e0a-ef5f8b96d997",
			expected: "fact-3db884b2",
		},
		{
			name:     "BareUUID",
			input:    "9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
			expected: "9b1deb4d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := domain.ShortenID(tt.input)
			if actual != tt.expected {
				t.Fatalf("ShortenID(%q) = %q; expected %q", tt.input, actual, tt.expected)
			}
		})
	}
}

// TestEffectiveShortID verifies that EffectiveShortID returns the explicit ShortID when present,
// and correctly falls back to ShortenID(ID) for all career entity types implementing ShortIdentifiable.
func TestEffectiveShortID(t *testing.T) {
	tests := []struct {
		name     string
		entity   domain.ShortIdentifiable
		expected string
	}{
		{
			name: "WorkExperience_ExplicitShortID",
			entity: domain.WorkExperience{
				ID:      "exp-9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
				ShortID: "exp-1",
			},
			expected: "exp-1",
		},
		{
			name: "WorkExperience_FallbackUUID",
			entity: domain.WorkExperience{
				ID: "exp-9b1deb4d-3b7d-4bad-9bdd-2b0d7b3dcb6d",
			},
			expected: "exp-9b1deb4d",
		},
		{
			name: "WorkExperience_FallbackShort",
			entity: domain.WorkExperience{
				ID: "exp-1",
			},
			expected: "exp-1",
		},
		{
			name: "Fact_ExplicitShortID",
			entity: domain.Fact{
				ID:      "fact-3db884b2-f155-442b-9e0a-ef5f8b96d997",
				ShortID: "fact-42",
			},
			expected: "fact-42",
		},
		{
			name: "Fact_FallbackUUID",
			entity: domain.Fact{
				ID: "fact-3db884b2-f155-442b-9e0a-ef5f8b96d997",
			},
			expected: "fact-3db884b2",
		},
		{
			name: "Skill_ExplicitShortID",
			entity: domain.Skill{
				ID:      "skill-87654321-3b7d-4bad-9bdd-2b0d7b3dcb6d",
				ShortID: "skill-go",
			},
			expected: "skill-go",
		},
		{
			name: "Project_ExplicitShortID",
			entity: domain.Project{
				ID:      "proj-11112222-3b7d-4bad-9bdd-2b0d7b3dcb6d",
				ShortID: "proj-resumeee",
			},
			expected: "proj-resumeee",
		},
		{
			name: "Education_ExplicitShortID",
			entity: domain.Education{
				ID:      "edu-33334444-3b7d-4bad-9bdd-2b0d7b3dcb6d",
				ShortID: "edu-mit",
			},
			expected: "edu-mit",
		},
		{
			name: "Certification_ExplicitShortID",
			entity: domain.Certification{
				ID:      "cert-55556666-3b7d-4bad-9bdd-2b0d7b3dcb6d",
				ShortID: "cert-cka",
			},
			expected: "cert-cka",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual := tt.entity.EffectiveShortID()
			if actual != tt.expected {
				t.Fatalf("EffectiveShortID() = %q; expected %q", actual, tt.expected)
			}
		})
	}
}
