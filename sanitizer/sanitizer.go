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
	PlaceholderFullName         = "[John Doe]"
	PlaceholderEmail            = "candidate@email.com"
	PlaceholderPhone            = "+1 111 111 1111"
	PlaceholderWebsite          = "https://candidate.example.com"
	PlaceholderSocialURLPrefix  = "https://social.example.com/profile-"
	PlaceholderSocialUserPrefix = "candidate-social-"
)

type textRule struct {
	re      *regexp.Regexp
	replace string
}

// Session manages deterministic PII masking and unmasking for a generation lifecycle.
type Session struct {
	obfRules   []textRule
	deobfRules []textRule
}

type rawMapping struct {
	source        string
	replacement   string
	wordBound     bool
	customPattern string
	customReplace string
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func isWordBounded(s string) bool {
	if s == "" {
		return false
	}
	runes := []rune(s)
	return isWordRune(runes[0]) && isWordRune(runes[len(runes)-1])
}

func countDigits(s string) int {
	cnt := 0
	for _, r := range s {
		if unicode.IsDigit(r) {
			cnt++
		}
	}
	return cnt
}

func isValidURL(raw string) bool {
	s := strings.TrimSpace(raw)
	if len(s) < 8 {
		return false
	}
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		rest := lower[strings.Index(lower, "://")+3:]
		parts := strings.Split(rest, "/")
		domainPart := parts[0]
		return strings.Contains(domainPart, ".") && !strings.HasPrefix(domainPart, ".") && !strings.HasSuffix(domainPart, ".") && len(domainPart) >= 4
	}
	parts := strings.Split(lower, "/")
	domainPart := parts[0]
	return strings.Contains(domainPart, ".") && !strings.HasPrefix(domainPart, ".") && !strings.HasSuffix(domainPart, ".") && len(domainPart) >= 4
}

// NewSession constructs a Session from a Profile.
func NewSession(profile *core.Profile) *Session {
	var targetProfile core.Profile
	if profile != nil {
		targetProfile = *profile
	}

	var forwardMappings []rawMapping
	var reverseMappings []rawMapping

	addPair := func(orig, placeholder string, wordBound bool) {
		orig = strings.TrimSpace(orig)
		placeholder = strings.TrimSpace(placeholder)
		if orig == "" || placeholder == "" || strings.EqualFold(orig, placeholder) {
			return
		}
		forwardMappings = append(forwardMappings, rawMapping{
			source:      orig,
			replacement: placeholder,
			wordBound:   wordBound,
		})
		reverseMappings = append(reverseMappings, rawMapping{
			source:      placeholder,
			replacement: orig,
			wordBound:   false,
		})
	}

	// 1. Full Name
	if targetProfile.FullName != "" {
		addPair(targetProfile.FullName, PlaceholderFullName, isWordBounded(targetProfile.FullName))
		// Also support reverse deobfuscation if LLM dropped the brackets
		if !strings.EqualFold(targetProfile.FullName, "John Doe") {
			reverseMappings = append(reverseMappings, rawMapping{
				source:      "John Doe",
				replacement: targetProfile.FullName,
				wordBound:   true,
			})
		}
	}

	// 2. Email
	if targetProfile.Email != "" {
		addPair(targetProfile.Email, PlaceholderEmail, false)
	}

	// 3. Phone
	cleanPhone := strings.TrimSpace(targetProfile.Phone)
	if cleanPhone != "" {
		// Minimum 7 substantive digits required to avoid false positives on country codes like "+1"
		if countDigits(cleanPhone) >= 7 {
			if isWordBounded(cleanPhone) {
				addPair(cleanPhone, PlaceholderPhone, true)
			} else {
				// Phone starts with non-word rune (e.g. '+' or '(').
				// Require leading boundary (^ or non-word, non-'+' character) and trailing word boundary \b.
				// Delimiter character is captured in ${1} to prevent stripping preceding whitespace.
				forwardMappings = append(forwardMappings, rawMapping{
					source:        cleanPhone,
					replacement:   PlaceholderPhone,
					customPattern: fmt.Sprintf(`(?i)(^|[^\w+])%s\b`, regexp.QuoteMeta(cleanPhone)),
					customReplace: fmt.Sprintf(`${1}%s`, PlaceholderPhone),
				})
				reverseMappings = append(reverseMappings, rawMapping{
					source:      PlaceholderPhone,
					replacement: cleanPhone,
					wordBound:   false,
				})
			}
		} else {
			// Substantive digits < 7: only register reverse deobfuscation rule for synthetic placeholder
			reverseMappings = append(reverseMappings, rawMapping{
				source:      PlaceholderPhone,
				replacement: cleanPhone,
				wordBound:   false,
			})
		}
	}

	// 4. Website
	var realWebsite string
	for _, p := range targetProfile.Profiles {
		if strings.EqualFold(p.Network, "website") || strings.EqualFold(p.Network, "portfolio") {
			realWebsite = strings.TrimSpace(p.URL)
			break
		}
	}
	if realWebsite == "" && targetProfile.Metadata != nil {
		if w, ok := targetProfile.Metadata["website"].(string); ok {
			realWebsite = strings.TrimSpace(w)
		}
	}
	if realWebsite != "" {
		if isValidURL(realWebsite) {
			addPair(realWebsite, PlaceholderWebsite, false)
		} else {
			reverseMappings = append(reverseMappings, rawMapping{
				source:      PlaceholderWebsite,
				replacement: realWebsite,
				wordBound:   false,
			})
		}
	}

	// 5. Social Links (Profiles)
	for i, p := range targetProfile.Profiles {
		idx := strconv.Itoa(i + 1)
		placeholderURL := PlaceholderSocialURLPrefix + idx
		placeholderUser := PlaceholderSocialUserPrefix + idx

		cleanURL := strings.TrimSpace(p.URL)
		if cleanURL != "" && cleanURL != realWebsite {
			if isValidURL(cleanURL) {
				addPair(cleanURL, placeholderURL, false)
			} else {
				reverseMappings = append(reverseMappings, rawMapping{
					source:      placeholderURL,
					replacement: cleanURL,
					wordBound:   false,
				})
			}
		}

		cleanUser := strings.TrimSpace(p.Username)
		if cleanUser != "" {
			if len(cleanUser) >= 3 {
				addPair(cleanUser, placeholderUser, isWordBounded(cleanUser))
			} else {
				// Usernames shorter than 3 characters (e.g. "go", "ai", "x") are excluded from free-text
				// replacement to prevent corrupting technical terms and common words.
				reverseMappings = append(reverseMappings, rawMapping{
					source:      placeholderUser,
					replacement: cleanUser,
					wordBound:   false,
				})
			}
		}
	}

	// Sort mappings by source length descending to prevent partial prefix collision
	sort.Slice(forwardMappings, func(i, j int) bool {
		return len(forwardMappings[i].source) > len(forwardMappings[j].source)
	})
	sort.Slice(reverseMappings, func(i, j int) bool {
		return len(reverseMappings[i].source) > len(reverseMappings[j].source)
	})

	compileRules := func(mappings []rawMapping) []textRule {
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

	return &Session{
		obfRules:   compileRules(forwardMappings),
		deobfRules: compileRules(reverseMappings),
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

func cloneFacts(facts []core.Fact) []core.Fact {
	if facts == nil {
		return nil
	}
	out := make([]core.Fact, len(facts))
	copy(out, facts)
	return out
}

func cloneBullets(bullets []core.TailoredBullet) []core.TailoredBullet {
	if bullets == nil {
		return nil
	}
	out := make([]core.TailoredBullet, len(bullets))
	copy(out, bullets)
	return out
}

func cloneSnapshot(snap *core.MainProfileSnapshot) *core.MainProfileSnapshot {
	if snap == nil {
		return nil
	}
	res := *snap
	res.Profile = snap.Profile

	if snap.Experiences != nil {
		res.Experiences = make([]core.WorkExperience, len(snap.Experiences))
		for i, exp := range snap.Experiences {
			res.Experiences[i] = exp
			res.Experiences[i].Facts = cloneFacts(exp.Facts)
		}
	}
	if snap.Projects != nil {
		res.Projects = make([]core.Project, len(snap.Projects))
		for i, proj := range snap.Projects {
			res.Projects[i] = proj
			res.Projects[i].Facts = cloneFacts(proj.Facts)
		}
	}
	if snap.Education != nil {
		res.Education = make([]core.Education, len(snap.Education))
		for i, edu := range snap.Education {
			res.Education[i] = edu
			res.Education[i].Facts = cloneFacts(edu.Facts)
		}
	}
	if snap.Certifications != nil {
		res.Certifications = make([]core.Certification, len(snap.Certifications))
		copy(res.Certifications, snap.Certifications)
	}
	res.Facts = cloneFacts(snap.Facts)
	return &res
}

func (s *Session) obfuscateFacts(facts []core.Fact) {
	for i := range facts {
		facts[i].Statement = s.ObfuscateString(facts[i].Statement)
		facts[i].Impact = s.ObfuscateString(facts[i].Impact)
	}
}

func (s *Session) obfuscateBullets(bullets []core.TailoredBullet) {
	for i := range bullets {
		bullets[i].Text = s.ObfuscateString(bullets[i].Text)
	}
}

func (s *Session) deobfuscateBullets(bullets []core.TailoredBullet) {
	for i := range bullets {
		bullets[i].Text = s.DeobfuscateString(bullets[i].Text)
	}
}

func (s *Session) deobfuscateGapItems(items []core.GapItem) {
	for i := range items {
		items[i].Requirement = s.DeobfuscateString(items[i].Requirement)
		items[i].MissingArea = s.DeobfuscateString(items[i].MissingArea)
		items[i].ClarifyingPrompt = s.DeobfuscateString(items[i].ClarifyingPrompt)
		items[i].RecommendedAction = s.DeobfuscateString(items[i].RecommendedAction)
	}
}

// ObfuscateSnapshot returns an isolated clone of the snapshot with PII scrubbed from fields passed to LLM prompts.
func (s *Session) ObfuscateSnapshot(snap *core.MainProfileSnapshot) *core.MainProfileSnapshot {
	if snap == nil {
		return nil
	}
	res := cloneSnapshot(snap)

	res.Profile.Headline = s.ObfuscateString(res.Profile.Headline)
	res.Profile.Summary = s.ObfuscateString(res.Profile.Summary)

	for i := range res.Experiences {
		res.Experiences[i].Company = s.ObfuscateString(res.Experiences[i].Company)
		res.Experiences[i].RoleTitle = s.ObfuscateString(res.Experiences[i].RoleTitle)
		res.Experiences[i].DutiesSummary = s.ObfuscateString(res.Experiences[i].DutiesSummary)
		s.obfuscateFacts(res.Experiences[i].Facts)
	}

	for i := range res.Projects {
		res.Projects[i].Name = s.ObfuscateString(res.Projects[i].Name)
		res.Projects[i].Role = s.ObfuscateString(res.Projects[i].Role)
		res.Projects[i].URL = s.ObfuscateString(res.Projects[i].URL)
		res.Projects[i].Summary = s.ObfuscateString(res.Projects[i].Summary)
		s.obfuscateFacts(res.Projects[i].Facts)
	}

	for i := range res.Education {
		res.Education[i].Institution = s.ObfuscateString(res.Education[i].Institution)
		res.Education[i].Degree = s.ObfuscateString(res.Education[i].Degree)
		res.Education[i].Field = s.ObfuscateString(res.Education[i].Field)
		s.obfuscateFacts(res.Education[i].Facts)
	}

	for i := range res.Certifications {
		res.Certifications[i].Name = s.ObfuscateString(res.Certifications[i].Name)
		res.Certifications[i].Issuer = s.ObfuscateString(res.Certifications[i].Issuer)
		res.Certifications[i].Summary = s.ObfuscateString(res.Certifications[i].Summary)
		res.Certifications[i].URL = s.ObfuscateString(res.Certifications[i].URL)
	}

	s.obfuscateFacts(res.Facts)

	return res
}

func cloneTailoredContent(tc *core.TailoredContent) *core.TailoredContent {
	if tc == nil {
		return nil
	}
	res := *tc
	if tc.Experiences != nil {
		res.Experiences = make([]core.TailoredExperience, len(tc.Experiences))
		for i, exp := range tc.Experiences {
			res.Experiences[i] = exp
			res.Experiences[i].Bullets = cloneBullets(exp.Bullets)
		}
	}
	if tc.Projects != nil {
		res.Projects = make([]core.TailoredProject, len(tc.Projects))
		for i, proj := range tc.Projects {
			res.Projects[i] = proj
			res.Projects[i].Bullets = cloneBullets(proj.Bullets)
		}
	}
	if tc.Education != nil {
		res.Education = make([]core.TailoredEducation, len(tc.Education))
		for i, edu := range tc.Education {
			res.Education[i] = edu
			res.Education[i].Bullets = cloneBullets(edu.Bullets)
		}
	}
	return &res
}

// ObfuscatePriorVersion returns an isolated clone of prior tailored content with PII scrubbed from fields passed to LLM prompts.
func (s *Session) ObfuscatePriorVersion(prior *core.TailoredContent) *core.TailoredContent {
	if prior == nil {
		return nil
	}
	res := cloneTailoredContent(prior)
	res.Summary = s.ObfuscateString(res.Summary)

	for i := range res.Experiences {
		res.Experiences[i].Company = s.ObfuscateString(res.Experiences[i].Company)
		res.Experiences[i].RoleTitle = s.ObfuscateString(res.Experiences[i].RoleTitle)
		res.Experiences[i].DutiesSummary = s.ObfuscateString(res.Experiences[i].DutiesSummary)
		s.obfuscateBullets(res.Experiences[i].Bullets)
	}

	for i := range res.Projects {
		res.Projects[i].Name = s.ObfuscateString(res.Projects[i].Name)
		res.Projects[i].Role = s.ObfuscateString(res.Projects[i].Role)
		res.Projects[i].URL = s.ObfuscateString(res.Projects[i].URL)
		res.Projects[i].Summary = s.ObfuscateString(res.Projects[i].Summary)
		s.obfuscateBullets(res.Projects[i].Bullets)
	}

	for i := range res.Education {
		res.Education[i].Institution = s.ObfuscateString(res.Education[i].Institution)
		res.Education[i].Degree = s.ObfuscateString(res.Education[i].Degree)
		res.Education[i].Field = s.ObfuscateString(res.Education[i].Field)
		s.obfuscateBullets(res.Education[i].Bullets)
	}

	return res
}

// ObfuscateRequest returns a copy of req with PII scrubbed from fields forwarded to LLM prompts.
func (s *Session) ObfuscateRequest(req core.GenerationRequest) core.GenerationRequest {
	res := req
	res.MainProfile = s.ObfuscateSnapshot(req.MainProfile)
	res.PriorVersion = s.ObfuscatePriorVersion(req.PriorVersion)
	res.GlobalRules = s.ObfuscateString(req.GlobalRules)
	res.PresetInstructions = s.ObfuscateString(req.PresetInstructions)

	res.BranchContext = req.BranchContext
	res.BranchContext.TargetRole = s.ObfuscateString(res.BranchContext.TargetRole)
	res.BranchContext.TargetCompany = s.ObfuscateString(res.BranchContext.TargetCompany)
	res.BranchContext.TargetRequirements = s.ObfuscateString(res.BranchContext.TargetRequirements)
	res.BranchContext.BranchInstructions = s.ObfuscateString(res.BranchContext.BranchInstructions)

	return res
}

// DeobfuscateTailoredContent restores original contact details into LLM-generated TailoredContent fields.
func (s *Session) DeobfuscateTailoredContent(content *core.TailoredContent) {
	if s == nil || content == nil {
		return
	}
	content.Summary = s.DeobfuscateString(content.Summary)

	for i := range content.Experiences {
		content.Experiences[i].Company = s.DeobfuscateString(content.Experiences[i].Company)
		content.Experiences[i].RoleTitle = s.DeobfuscateString(content.Experiences[i].RoleTitle)
		content.Experiences[i].DutiesSummary = s.DeobfuscateString(content.Experiences[i].DutiesSummary)
		s.deobfuscateBullets(content.Experiences[i].Bullets)
	}

	for i := range content.Projects {
		content.Projects[i].Name = s.DeobfuscateString(content.Projects[i].Name)
		content.Projects[i].Role = s.DeobfuscateString(content.Projects[i].Role)
		content.Projects[i].URL = s.DeobfuscateString(content.Projects[i].URL)
		content.Projects[i].Summary = s.DeobfuscateString(content.Projects[i].Summary)
		s.deobfuscateBullets(content.Projects[i].Bullets)
	}

	for i := range content.Education {
		content.Education[i].Institution = s.DeobfuscateString(content.Education[i].Institution)
		content.Education[i].Degree = s.DeobfuscateString(content.Education[i].Degree)
		content.Education[i].Field = s.DeobfuscateString(content.Education[i].Field)
	}

	for i := range content.Certifications {
		content.Certifications[i].Name = s.DeobfuscateString(content.Certifications[i].Name)
		content.Certifications[i].Issuer = s.DeobfuscateString(content.Certifications[i].Issuer)
		content.Certifications[i].URL = s.DeobfuscateString(content.Certifications[i].URL)
	}
}

// DeobfuscateGapReport restores original contact details into the provided GapReport.
func (s *Session) DeobfuscateGapReport(report *core.GapReport) {
	if s == nil || report == nil {
		return
	}
	report.OverallMatchSummary = s.DeobfuscateString(report.OverallMatchSummary)

	for i := range report.StrategicRecommendations {
		report.StrategicRecommendations[i] = s.DeobfuscateString(report.StrategicRecommendations[i])
	}

	s.deobfuscateGapItems(report.MissingRequirements)
	s.deobfuscateGapItems(report.IdentifiedGaps)
}

// DeobfuscateGenerationResponse restores original contact details across TailoredContent and GapReport.
func (s *Session) DeobfuscateGenerationResponse(resp *core.GenerationResponse) {
	if s == nil || resp == nil {
		return
	}
	s.DeobfuscateTailoredContent(&resp.TailoredContent)
	s.DeobfuscateGapReport(&resp.GapReport)
}

// DeobfuscateATSScore restores original contact details across an ATSScore and its ExtractedCandidate.
func (s *Session) DeobfuscateATSScore(score *core.ATSScore) {
	if s == nil || score == nil {
		return
	}
	score.ExtractionSummary = s.DeobfuscateString(score.ExtractionSummary)
	score.MatchingSummary = s.DeobfuscateString(score.MatchingSummary)
	if score.ExtractedCandidate != nil {
		c := score.ExtractedCandidate
		c.Summary = s.DeobfuscateString(c.Summary)
		for i := range c.Skills {
			c.Skills[i] = s.DeobfuscateString(c.Skills[i])
		}
		for i := range c.Experiences {
			c.Experiences[i].Company = s.DeobfuscateString(c.Experiences[i].Company)
			c.Experiences[i].Title = s.DeobfuscateString(c.Experiences[i].Title)
			c.Experiences[i].DateRange = s.DeobfuscateString(c.Experiences[i].DateRange)
			for j := range c.Experiences[i].Highlights {
				c.Experiences[i].Highlights[j] = s.DeobfuscateString(c.Experiences[i].Highlights[j])
			}
		}
		for i := range c.Educations {
			c.Educations[i].Institution = s.DeobfuscateString(c.Educations[i].Institution)
			c.Educations[i].Degree = s.DeobfuscateString(c.Educations[i].Degree)
			c.Educations[i].Field = s.DeobfuscateString(c.Educations[i].Field)
			c.Educations[i].DateRange = s.DeobfuscateString(c.Educations[i].DateRange)
		}
		for i := range c.Certifications {
			c.Certifications[i] = s.DeobfuscateString(c.Certifications[i])
		}
	}
}
