// Package sanitizer provides deterministic PII obfuscation and deobfuscation for LLM interactions.
package sanitizer

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/resumeee-app/resumeee-core"
)

// Canonical deterministic PII placeholder constants.
const (
	// PlaceholderFullName replaces the candidate's full name in obfuscated prompts.
	PlaceholderFullName = "[John Doe]"
	// PlaceholderEmail replaces the candidate's email address in obfuscated prompts.
	PlaceholderEmail = "candidate@email.com"
	// PlaceholderPhone replaces the candidate's phone number in obfuscated prompts.
	PlaceholderPhone = "+1 111 111 1111"
	// PlaceholderWebsite replaces personal website URLs in obfuscated prompts.
	PlaceholderWebsite = "https://candidate.example.com"
	// PlaceholderSocialURLPrefix prefixes indexed synthetic social profile URLs.
	PlaceholderSocialURLPrefix = "https://social.example.com/profile-"
	// PlaceholderSocialUserPrefix prefixes indexed synthetic social handles.
	PlaceholderSocialUserPrefix = "candidate-social-"
)

// textRule pairs a compiled regex with its literal or capture-aware replacement.
type textRule struct {
	re      *regexp.Regexp
	replace string
}

// Session manages deterministic PII masking and unmasking for a generation lifecycle.
type Session struct {
	obfRules   []textRule
	deobfRules []textRule
}

// rawMapping captures an uncompiled source-to-placeholder substitution rule.
type rawMapping struct {
	source        string
	replacement   string
	wordBound     bool
	customPattern string
	customReplace string
}

// mappingBuilder accumulates forward and reverse PII replacement rules.
type mappingBuilder struct {
	forward []rawMapping
	reverse []rawMapping
}

// isWordRune reports whether r behaves as an ASCII/Unicode word boundary character.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

// isWordBounded reports whether s starts and ends with word runes so \b anchors match safely.
func isWordBounded(s string) bool {
	if s == "" {
		return false
	}
	runes := []rune(s)
	return isWordRune(runes[0]) && isWordRune(runes[len(runes)-1])
}

// countDigits returns the number of decimal digits in s.
func countDigits(s string) int {
	cnt := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			cnt++
		}
	}
	return cnt
}

// isValidURL performs a lightweight structural check before registering a URL for masking.
func isValidURL(raw string) bool {
	s := strings.TrimSpace(raw)
	if len(s) < 8 {
		return false
	}
	lower := strings.ToLower(s)
	if after, ok := strings.CutPrefix(lower, "http://"); ok {
		lower = after
	} else if after, ok := strings.CutPrefix(lower, "https://"); ok {
		lower = after
	}
	domainPart, _, _ := strings.Cut(lower, "/")
	return len(domainPart) >= 4 &&
		strings.Contains(domainPart, ".") &&
		!strings.HasPrefix(domainPart, ".") &&
		!strings.HasSuffix(domainPart, ".")
}

// addPair registers symmetric forward obfuscation and reverse deobfuscation rules.
func (b *mappingBuilder) addPair(orig, placeholder string, wordBound bool) {
	orig = strings.TrimSpace(orig)
	placeholder = strings.TrimSpace(placeholder)
	if orig == "" || placeholder == "" || strings.EqualFold(orig, placeholder) {
		return
	}
	b.forward = append(b.forward, rawMapping{
		source:      orig,
		replacement: placeholder,
		wordBound:   wordBound,
	})
	b.reverse = append(b.reverse, rawMapping{
		source:      placeholder,
		replacement: orig,
		wordBound:   false,
	})
}

// addContactRules registers candidate name and email replacement rules.
func (b *mappingBuilder) addContactRules(profile core.Profile) {
	if profile.FullName != "" {
		b.addPair(profile.FullName, PlaceholderFullName, isWordBounded(profile.FullName))
		// Also support reverse deobfuscation if LLM dropped the brackets.
		if !strings.EqualFold(profile.FullName, "John Doe") {
			b.reverse = append(b.reverse, rawMapping{
				source:      "John Doe",
				replacement: profile.FullName,
				wordBound:   true,
			})
		}
	}

	if profile.Email != "" {
		b.addPair(profile.Email, PlaceholderEmail, false)
	}
}

// addPhoneRule registers phone obfuscation while avoiding false positives on short numbers.
func (b *mappingBuilder) addPhoneRule(phone string) {
	// Minimum 7 substantive digits prevents masking country codes like "+1".
	cleanPhone := strings.TrimSpace(phone)
	if cleanPhone == "" || countDigits(cleanPhone) < 7 {
		return
	}
	if isWordBounded(cleanPhone) {
		b.addPair(cleanPhone, PlaceholderPhone, true)
		return
	}
	// Phone starts with a non-word rune (e.g. '+' or '('); capture the leading delimiter in ${1}
	// so surrounding whitespace is preserved.
	b.forward = append(b.forward, rawMapping{
		source:        cleanPhone,
		replacement:   PlaceholderPhone,
		customPattern: fmt.Sprintf(`(?i)(^|[^\w+])%s\b`, regexp.QuoteMeta(cleanPhone)),
		customReplace: fmt.Sprintf(`${1}%s`, PlaceholderPhone),
	})
	b.reverse = append(b.reverse, rawMapping{
		source:      PlaceholderPhone,
		replacement: cleanPhone,
		wordBound:   false,
	})
}

// extractRealWebsite finds the candidate's primary portfolio or website URL.
func extractRealWebsite(profile core.Profile) string {
	for _, p := range profile.Profiles {
		if strings.EqualFold(p.Network, "website") || strings.EqualFold(p.Network, "portfolio") {
			if trimmed := strings.TrimSpace(p.URL); trimmed != "" {
				return trimmed
			}
		}
	}
	if profile.Metadata != nil {
		if w, ok := profile.Metadata["website"].(string); ok {
			return strings.TrimSpace(w)
		}
	}
	return ""
}

// addWebsiteAndSocialRules registers website URL, social URL, and social handle replacements.
func (b *mappingBuilder) addWebsiteAndSocialRules(profile core.Profile) {
	realWebsite := extractRealWebsite(profile)
	if realWebsite != "" && isValidURL(realWebsite) {
		b.addPair(realWebsite, PlaceholderWebsite, false)
	}

	for i, p := range profile.Profiles {
		idx := strconv.Itoa(i + 1)
		placeholderURL := PlaceholderSocialURLPrefix + idx
		placeholderUser := PlaceholderSocialUserPrefix + idx

		cleanURL := strings.TrimSpace(p.URL)
		if cleanURL != "" && cleanURL != realWebsite && isValidURL(cleanURL) {
			b.addPair(cleanURL, placeholderURL, false)
		}

		// Usernames shorter than 3 characters (e.g. "go", "ai", "c") are excluded to prevent
		// corrupting technical terms and common words.
		cleanUser := strings.TrimSpace(p.Username)
		if len(cleanUser) >= 3 {
			b.addPair(cleanUser, placeholderUser, isWordBounded(cleanUser))
		}
	}
}

// compileMappings sorts raw mappings longest-first and compiles unique regexp rules.
func compileMappings(mappings []rawMapping) []textRule {
	// Sort by source length descending to prevent partial prefix collision.
	sort.Slice(mappings, func(i, j int) bool {
		return len(mappings[i].source) > len(mappings[j].source)
	})

	var rules []textRule
	seen := make(map[string]bool)
	for _, m := range mappings {
		key := fmt.Sprintf("%t:%s:%s:%s", m.wordBound, m.source, m.replacement, m.customPattern)
		if seen[key] {
			continue
		}
		seen[key] = true

		pattern := m.customPattern
		replace := m.customReplace
		if pattern == "" {
			pattern = regexp.QuoteMeta(m.source)
			if m.wordBound {
				pattern = fmt.Sprintf(`(?i)\b%s\b`, pattern)
			} else {
				pattern = fmt.Sprintf(`(?i)%s`, pattern)
			}
			replace = m.replacement
		}

		re, err := regexp.Compile(pattern)
		if err == nil {
			rules = append(rules, textRule{
				re:      re,
				replace: replace,
			})
		}
	}
	return rules
}

// NewSession constructs a Session from a Profile.
func NewSession(profile *core.Profile) *Session {
	var targetProfile core.Profile
	if profile != nil {
		targetProfile = *profile
	}

	var builder mappingBuilder
	builder.addContactRules(targetProfile)
	builder.addPhoneRule(targetProfile.Phone)
	builder.addWebsiteAndSocialRules(targetProfile)

	return &Session{
		obfRules:   compileMappings(builder.forward),
		deobfRules: compileMappings(builder.reverse),
	}
}

// NewSessionFromSnapshot constructs a Session from a MainProfileSnapshot.
func NewSessionFromSnapshot(snapshot *core.MainProfileSnapshot) *Session {
	if snapshot == nil {
		return NewSession(nil)
	}
	return NewSession(&snapshot.Profile)
}

// NewSessionFromRequest constructs a Session from a GenerationRequest.
func NewSessionFromRequest(req core.GenerationRequest) *Session {
	return NewSessionFromSnapshot(req.MainProfile)
}

// ObfuscateString replaces occurrences of real contact details with placeholders.
func (s *Session) ObfuscateString(input string) string {
	if s == nil || input == "" || len(s.obfRules) == 0 {
		return input
	}
	res := input
	for _, rule := range s.obfRules {
		res = rule.re.ReplaceAllString(res, rule.replace)
	}
	return res
}

// DeobfuscateString replaces occurrences of placeholders with real contact details.
func (s *Session) DeobfuscateString(input string) string {
	if s == nil || input == "" || len(s.deobfRules) == 0 {
		return input
	}
	res := input
	for _, rule := range s.deobfRules {
		res = rule.re.ReplaceAllString(res, rule.replace)
	}
	return res
}

// cloneSlice returns a nil-preserving shallow copy of items.
func cloneSlice[T any](items []T) []T {
	if items == nil {
		return nil
	}
	out := make([]T, len(items))
	copy(out, items)
	return out
}

// cloneSnapshot returns a copy-on-write clone of snap for ObfuscateSnapshot.
func cloneSnapshot(snap *core.MainProfileSnapshot) *core.MainProfileSnapshot {
	if snap == nil {
		return nil
	}
	res := *snap
	if snap.Experiences != nil {
		res.Experiences = make([]core.WorkExperience, len(snap.Experiences))
		for i, exp := range snap.Experiences {
			res.Experiences[i] = exp
			res.Experiences[i].Facts = cloneSlice(exp.Facts)
		}
	}
	if snap.Projects != nil {
		res.Projects = make([]core.Project, len(snap.Projects))
		for i, proj := range snap.Projects {
			res.Projects[i] = proj
			res.Projects[i].Facts = cloneSlice(proj.Facts)
		}
	}
	if snap.Education != nil {
		res.Education = make([]core.Education, len(snap.Education))
		for i, edu := range snap.Education {
			res.Education[i] = edu
			res.Education[i].Facts = cloneSlice(edu.Facts)
		}
	}
	res.Facts = cloneSlice(snap.Facts)
	return &res
}

// ObfuscateSnapshot returns a copy-on-write clone of snap with candidate PII scrubbed from
// free-text and URL fields passed to LLMs. Unscrubbed slices and maps (such as Skills,
// Certifications, Profile.Profiles, and Profile.Metadata) share backing storage with snap
// and must not be mutated in place by callers.
func (s *Session) ObfuscateSnapshot(snap *core.MainProfileSnapshot) *core.MainProfileSnapshot {
	if snap == nil {
		return nil
	}
	res := cloneSnapshot(snap)
	if s == nil || len(s.obfRules) == 0 {
		return res
	}

	res.Profile.Headline = s.ObfuscateString(res.Profile.Headline)
	res.Profile.Summary = s.ObfuscateString(res.Profile.Summary)

	for i := range res.Experiences {
		res.Experiences[i].RoleTitle = s.ObfuscateString(res.Experiences[i].RoleTitle)
		res.Experiences[i].DutiesSummary = s.ObfuscateString(res.Experiences[i].DutiesSummary)
		for j := range res.Experiences[i].Facts {
			res.Experiences[i].Facts[j].Statement = s.ObfuscateString(res.Experiences[i].Facts[j].Statement)
			res.Experiences[i].Facts[j].Impact = s.ObfuscateString(res.Experiences[i].Facts[j].Impact)
		}
	}

	for i := range res.Projects {
		res.Projects[i].Name = s.ObfuscateString(res.Projects[i].Name)
		res.Projects[i].Role = s.ObfuscateString(res.Projects[i].Role)
		res.Projects[i].URL = s.ObfuscateString(res.Projects[i].URL)
		res.Projects[i].Summary = s.ObfuscateString(res.Projects[i].Summary)
		for j := range res.Projects[i].Facts {
			res.Projects[i].Facts[j].Statement = s.ObfuscateString(res.Projects[i].Facts[j].Statement)
			res.Projects[i].Facts[j].Impact = s.ObfuscateString(res.Projects[i].Facts[j].Impact)
		}
	}

	for i := range res.Education {
		for j := range res.Education[i].Facts {
			res.Education[i].Facts[j].Statement = s.ObfuscateString(res.Education[i].Facts[j].Statement)
			res.Education[i].Facts[j].Impact = s.ObfuscateString(res.Education[i].Facts[j].Impact)
		}
	}

	for i := range res.Facts {
		res.Facts[i].Statement = s.ObfuscateString(res.Facts[i].Statement)
		res.Facts[i].Impact = s.ObfuscateString(res.Facts[i].Impact)
	}

	return res
}

// cloneTailoredContent returns a copy-on-write clone of tc for ObfuscatePriorVersion.
func cloneTailoredContent(tc *core.TailoredContent) *core.TailoredContent {
	if tc == nil {
		return nil
	}
	res := *tc
	if tc.Experiences != nil {
		res.Experiences = make([]core.TailoredExperience, len(tc.Experiences))
		for i, exp := range tc.Experiences {
			res.Experiences[i] = exp
			res.Experiences[i].Bullets = cloneSlice(exp.Bullets)
		}
	}
	if tc.Projects != nil {
		res.Projects = make([]core.TailoredProject, len(tc.Projects))
		for i, proj := range tc.Projects {
			res.Projects[i] = proj
			res.Projects[i].Bullets = cloneSlice(proj.Bullets)
		}
	}
	if tc.Education != nil {
		res.Education = make([]core.TailoredEducation, len(tc.Education))
		for i, edu := range tc.Education {
			res.Education[i] = edu
			res.Education[i].Bullets = cloneSlice(edu.Bullets)
		}
	}
	return &res
}

// ObfuscatePriorVersion returns a copy-on-write clone of prior tailored content with candidate
// PII scrubbed from narrative, bullet, and project fields passed to LLMs. Unscrubbed slices
// (such as SkillGroups and Certifications) share backing storage with prior.
func (s *Session) ObfuscatePriorVersion(prior *core.TailoredContent) *core.TailoredContent {
	if prior == nil {
		return nil
	}
	res := cloneTailoredContent(prior)
	if s == nil || len(s.obfRules) == 0 {
		return res
	}

	res.Summary = s.ObfuscateString(res.Summary)

	for i := range res.Experiences {
		res.Experiences[i].RoleTitle = s.ObfuscateString(res.Experiences[i].RoleTitle)
		res.Experiences[i].DutiesSummary = s.ObfuscateString(res.Experiences[i].DutiesSummary)
		for j := range res.Experiences[i].Bullets {
			res.Experiences[i].Bullets[j].Text = s.ObfuscateString(res.Experiences[i].Bullets[j].Text)
		}
	}

	for i := range res.Projects {
		res.Projects[i].Name = s.ObfuscateString(res.Projects[i].Name)
		res.Projects[i].Role = s.ObfuscateString(res.Projects[i].Role)
		res.Projects[i].URL = s.ObfuscateString(res.Projects[i].URL)
		res.Projects[i].Summary = s.ObfuscateString(res.Projects[i].Summary)
		for j := range res.Projects[i].Bullets {
			res.Projects[i].Bullets[j].Text = s.ObfuscateString(res.Projects[i].Bullets[j].Text)
		}
	}

	for i := range res.Education {
		for j := range res.Education[i].Bullets {
			res.Education[i].Bullets[j].Text = s.ObfuscateString(res.Education[i].Bullets[j].Text)
		}
	}

	return res
}

// ObfuscateRequest returns a shallow copy of req with copy-on-write clones of MainProfile and
// PriorVersion and PII scrubbed from free-text prompt fields. Unscrubbed slices and pointers on
// req (such as Preset and BranchContext.PinnedEntityIDs) share backing storage with req.
func (s *Session) ObfuscateRequest(req core.GenerationRequest) core.GenerationRequest {
	res := req
	res.MainProfile = s.ObfuscateSnapshot(req.MainProfile)
	res.PriorVersion = s.ObfuscatePriorVersion(req.PriorVersion)
	res.GlobalRules = s.ObfuscateString(req.GlobalRules)
	res.PresetInstructions = s.ObfuscateString(req.PresetInstructions)

	res.BranchContext.TargetRole = s.ObfuscateString(res.BranchContext.TargetRole)
	res.BranchContext.TargetRequirements = s.ObfuscateString(res.BranchContext.TargetRequirements)
	res.BranchContext.BranchInstructions = s.ObfuscateString(res.BranchContext.BranchInstructions)

	return res
}

// DeobfuscateTailoredContent restores original contact details into LLM-authored narrative and
// bullet fields of TailoredContent. Structured entity headers (company, role title, project
// identity, education, certifications) are canonically populated by provenance.Validate.
func (s *Session) DeobfuscateTailoredContent(content *core.TailoredContent) {
	if s == nil || content == nil || len(s.deobfRules) == 0 {
		return
	}
	content.Summary = s.DeobfuscateString(content.Summary)

	for i := range content.Experiences {
		content.Experiences[i].DutiesSummary = s.DeobfuscateString(content.Experiences[i].DutiesSummary)
		for j := range content.Experiences[i].Bullets {
			content.Experiences[i].Bullets[j].Text = s.DeobfuscateString(content.Experiences[i].Bullets[j].Text)
		}
	}

	for i := range content.Projects {
		content.Projects[i].Summary = s.DeobfuscateString(content.Projects[i].Summary)
		for j := range content.Projects[i].Bullets {
			content.Projects[i].Bullets[j].Text = s.DeobfuscateString(content.Projects[i].Bullets[j].Text)
		}
	}
}

// deobfuscateGapItems restores original contact details across every GapItem in items.
func (s *Session) deobfuscateGapItems(items []core.GapItem) {
	for i := range items {
		items[i].Requirement = s.DeobfuscateString(items[i].Requirement)
		items[i].MissingArea = s.DeobfuscateString(items[i].MissingArea)
		items[i].ClarifyingPrompt = s.DeobfuscateString(items[i].ClarifyingPrompt)
		items[i].RecommendedAction = s.DeobfuscateString(items[i].RecommendedAction)
	}
}

// DeobfuscateGapReport restores original contact details into the provided GapReport.
func (s *Session) DeobfuscateGapReport(report *core.GapReport) {
	if s == nil || report == nil || len(s.deobfRules) == 0 {
		return
	}
	report.OverallMatchSummary = s.DeobfuscateString(report.OverallMatchSummary)

	for i := range report.StrategicRecommendations {
		report.StrategicRecommendations[i] = s.DeobfuscateString(report.StrategicRecommendations[i])
	}

	s.deobfuscateGapItems(report.MissingRequirements)
	if len(report.IdentifiedGaps) > 0 &&
		(len(report.MissingRequirements) == 0 ||
			len(report.IdentifiedGaps) != len(report.MissingRequirements) ||
			&report.IdentifiedGaps[0] != &report.MissingRequirements[0]) {
		s.deobfuscateGapItems(report.IdentifiedGaps)
	}
}

// DeobfuscateGenerationResponse restores original contact details across TailoredContent and GapReport.
func (s *Session) DeobfuscateGenerationResponse(resp *core.GenerationResponse) {
	if s == nil || resp == nil {
		return
	}
	s.DeobfuscateTailoredContent(&resp.TailoredContent)
	s.DeobfuscateGapReport(&resp.GapReport)
}

// DeobfuscateATSScore restores original contact details across free-text narrative and highlight
// fields of an ATSScore and its ExtractedCandidate.
func (s *Session) DeobfuscateATSScore(score *core.ATSScore) {
	if s == nil || score == nil || len(s.deobfRules) == 0 {
		return
	}
	score.ExtractionSummary = s.DeobfuscateString(score.ExtractionSummary)
	score.MatchingSummary = s.DeobfuscateString(score.MatchingSummary)
	if score.ExtractedCandidate != nil {
		c := score.ExtractedCandidate
		c.Summary = s.DeobfuscateString(c.Summary)
		for i := range c.Experiences {
			c.Experiences[i].Title = s.DeobfuscateString(c.Experiences[i].Title)
			for j := range c.Experiences[i].Highlights {
				c.Experiences[i].Highlights[j] = s.DeobfuscateString(c.Experiences[i].Highlights[j])
			}
		}
	}
}
