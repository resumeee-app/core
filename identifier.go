package core

import (
	"crypto/rand"
	"math/big"
	"strings"

	"github.com/google/uuid"
)

// shortIDCharset defines the lowercase alphanumeric alphabet used for compact deterministic IDs.
const shortIDCharset = "0123456789abcdefghijklmnopqrstuvwxyz"

// shortIDLength specifies the character length of generated short IDs.
const shortIDLength = 6

// GenerateID generates a canonical persistent UUID identifier, optionally prefixed by an entity category tag.
// If prefix is provided (e.g. "exp", "skill", "proj"), the output is formatted as "<prefix>-<uuid>".
// If prefix is empty, a standard 36-character hyphenated UUIDv4 is returned.
//
// Parameters:
//   - prefix: Domain entity discriminator prefix (e.g., "exp", "fact", "skill"). Leading/trailing hyphens are trimmed.
//
// Returns:
//   - A canonical unique identifier string.
func GenerateID(prefix string) string {
	u := uuid.New().String()
	cleanPrefix := strings.Trim(prefix, "-")
	if cleanPrefix == "" {
		return u
	}
	return cleanPrefix + "-" + u
}

// GenerateShortID generates a compact, cryptographically random, URL-safe 6-character base36 identifier.
// The output is prefixed by the specified entity category tag if non-empty (e.g., "exp-9b1d4e", "fact-k8s9m2").
// If crypto/rand encounters an entropy error, a deterministic fallback character is written to ensure non-failing generation.
//
// Parameters:
//   - prefix: Domain entity discriminator prefix (e.g., "exp", "fact", "skill"). Leading/trailing hyphens are trimmed.
//
// Returns:
//   - A compact short identifier string suitable for LLM prompts and URL route paths.
func GenerateShortID(prefix string) string {
	var sb strings.Builder
	charsetLen := big.NewInt(int64(len(shortIDCharset)))
	for i := 0; i < shortIDLength; i++ {
		idx, err := rand.Int(rand.Reader, charsetLen)
		if err != nil {
			sb.WriteByte(shortIDCharset[i%len(shortIDCharset)])
			continue
		}
		sb.WriteByte(shortIDCharset[idx.Int64()])
	}
	randStr := sb.String()

	cleanPrefix := strings.Trim(prefix, "-")
	if cleanPrefix == "" {
		return randStr
	}
	return cleanPrefix + "-" + randStr
}

// ShortenID deterministically derives a compact short identifier from an existing long UUID or arbitrary identifier.
// It is used as a fallback accessor when an entity does not have an explicit ShortID persisted.
//
// Behavior:
//   - If id is empty or whitespace, it returns empty string "".
//   - If id is already compact (<= 12 characters), it returns id unmodified.
//   - If id is a bare UUIDv4 ("xxxxxxxx-xxxx-..."), it returns the first 8 characters.
//   - If id is a prefixed UUID ("<prefix>-<uuid>"), it preserves the prefix and appends the first 8 characters of the cleaned suffix (e.g. "exp-9b1deb4d").
//   - For non-hyphenated strings longer than 8 characters, it truncates to the first 8 characters.
//
// Parameters:
//   - id: The canonical long identifier or existing string to shorten.
//
// Returns:
//   - A deterministic, shortened string representation of the input identifier.
func ShortenID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	// If already short (<= 12 chars), return as-is
	if len(trimmed) <= 12 {
		return trimmed
	}

	// If it contains a hyphen, check if it's prefixed or a bare UUID
	if idx := strings.Index(trimmed, "-"); idx != -1 {
		parts := strings.Split(trimmed, "-")
		if len(parts) == 5 && len(parts[0]) == 8 && len(parts[1]) == 4 {
			// Bare UUID: return first 8 chars
			return parts[0]
		}

		prefix := trimmed[:idx]
		suffix := trimmed[idx+1:]
		suffixClean := strings.ReplaceAll(suffix, "-", "")
		if len(suffixClean) > 8 {
			suffixClean = suffixClean[:8]
		}
		return prefix + "-" + suffixClean
	}

	// No hyphens and length > 12: truncate to 8 chars
	if len(trimmed) > 8 {
		return trimmed[:8]
	}
	return trimmed
}

// unaccent maps common accented latin runes to their ASCII base character.
func unaccent(r rune) rune {
	switch r {
	case 'à', 'á', 'â', 'ã', 'ä', 'å', 'À', 'Á', 'Â', 'Ã', 'Ä', 'Å':
		return 'a'
	case 'è', 'é', 'ê', 'ë', 'È', 'É', 'Ê', 'Ë':
		return 'e'
	case 'ì', 'í', 'î', 'ï', 'Ì', 'Í', 'Î', 'Ï':
		return 'i'
	case 'ò', 'ó', 'ô', 'õ', 'ö', 'Ò', 'Ó', 'Ô', 'Õ', 'Ö', 'ø', 'Ø':
		return 'o'
	case 'ù', 'ú', 'û', 'ü', 'Ù', 'Ú', 'Û', 'Ü':
		return 'u'
	case 'ñ', 'Ñ':
		return 'n'
	case 'ç', 'Ç':
		return 'c'
	case 'ý', 'ÿ', 'Ý':
		return 'y'
	default:
		return r
	}
}

// Slugify converts an arbitrary string into a URL-safe, lowercase alphanumeric hyphenated slug.
func Slugify(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	var b strings.Builder
	lastHyphen := false
	for _, r := range s {
		r = unaccent(r)
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
			lastHyphen = false
		case r == '-' || r == '_' || r == ' ' || r == '.' || r == '/':
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
