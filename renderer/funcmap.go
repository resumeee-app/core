package renderer

import (
	"errors"
	"fmt"
	htmltemplate "html/template"
	"reflect"
	"regexp"
	"strings"
	"time"

	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/resumeee-app/resumeee-core"
)

// dateParseLayouts lists accepted date formats in decreasing structural precision.
var dateParseLayouts = []string{
	time.RFC3339,
	"2006-01-02",
	"2006-01",
	"2006",
	"2006/01/02",
	"2006/01",
	"Jan 2006",
	"January 2006",
	"01/2006",
	"01/02/2006",
}

// fourDigitsRegex matches 4-digit calendar years within free-form date strings.
var fourDigitsRegex = regexp.MustCompile(`\b(19\d\d|20\d\d)\b`)

// extractDateString converts supported date types into a normalized string and signals presence.
func extractDateString(val any) (string, bool) {
	if val == nil {
		return "", false
	}
	switch v := val.(type) {
	case core.Date:
		if v.IsZero() {
			return "", false
		}
		return v.String(), true
	case *core.Date:
		if v == nil || v.IsZero() {
			return "", false
		}
		return v.String(), true
	case time.Time:
		if v.IsZero() {
			return "", false
		}
		return v.Format("2006-01-02"), true
	case *time.Time:
		if v == nil || v.IsZero() {
			return "", false
		}
		return v.Format("2006-01-02"), true
	case fmt.Stringer:
		str := strings.TrimSpace(v.String())
		if str == "" {
			return "", false
		}
		return str, true
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return "", false
		}
		return trimmed, true
	case *string:
		if v == nil || strings.TrimSpace(*v) == "" {
			return "", false
		}
		return strings.TrimSpace(*v), true
	default:
		s := strings.TrimSpace(fmt.Sprint(val))
		if s == "" || s == "<nil>" {
			return "", false
		}
		return s, true
	}
}

// parseDate attempts to parse a date string using common ISO and human-readable formats.
func parseDate(s string) (time.Time, error) {
	for _, layout := range dateParseLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("cannot parse date")
}

// formatAutoDate formats a date input as YYYY or Jan 2006 depending on input precision.
//
// Invariant: Full dates (YYYY-MM-DD) are formatted as Month and Year (e.g., "Jan 2006"),
// preserving the requirement that resume rendering outputs month and year without days.
func formatAutoDate(val any) string {
	s, ok := extractDateString(val)
	if !ok || s == "" {
		return ""
	}
	t, err := parseDate(s)
	if err != nil {
		return s
	}
	if len(s) == 4 {
		return t.Format("2006")
	}
	return t.Format("Jan 2006")
}

// formatDate formats a given date input using a standard Go time layout.
func formatDate(layout string, dateVal any) string {
	s, ok := extractDateString(dateVal)
	if !ok || s == "" {
		return ""
	}
	t, err := parseDate(s)
	if err != nil {
		return s
	}
	return t.Format(layout)
}

// formatDateRange formats a start and end date interval with an optional present label.
// It accepts any supported date types (core.Date, *core.Date, time.Time, *time.Time, string).
func formatDateRange(start any, end any, presentLabel string) string {
	if strings.TrimSpace(presentLabel) == "" {
		presentLabel = "Present"
	}
	endStr, hasEnd := extractDateString(end)
	isEndEmpty := !hasEnd || endStr == ""

	startFormatted := formatAutoDate(start)

	var endFormatted string
	if isEndEmpty || strings.EqualFold(strings.TrimSpace(endStr), "present") {
		endFormatted = presentLabel
	} else {
		endFormatted = formatAutoDate(endStr)
	}

	if startFormatted == "" && isEndEmpty {
		return ""
	}
	if startFormatted != "" {
		return fmt.Sprintf("%s – %s", startFormatted, endFormatted)
	}
	return endFormatted
}

// year extracts a 4-digit calendar year from a date string or Date instance.
func year(dateVal any) string {
	s, ok := extractDateString(dateVal)
	if !ok || s == "" {
		return ""
	}
	if t, err := parseDate(s); err == nil {
		return fmt.Sprintf("%04d", t.Year())
	}
	if match := fourDigitsRegex.FindString(s); match != "" {
		return match
	}
	return s
}

// titleCase converts s to Unicode title case using language-independent rules.
func titleCase(s string) string {
	return cases.Title(language.Und).String(s)
}

// joinHelper joins elements of a slice or array into a string using the provided delimiter.
func joinHelper(elems any, sep string) string {
	if elems == nil {
		return ""
	}
	if strSlice, ok := elems.([]string); ok {
		return strings.Join(strSlice, sep)
	}
	val := reflect.ValueOf(elems)
	if val.Kind() == reflect.Slice || val.Kind() == reflect.Array {
		n := val.Len()
		parts := make([]string, n)
		for i := 0; i < n; i++ {
			parts[i] = fmt.Sprint(val.Index(i).Interface())
		}
		return strings.Join(parts, sep)
	}
	return fmt.Sprint(elems)
}

// firstHelper returns the first string representation from a slice or array.
func firstHelper(items any) string {
	if items == nil {
		return ""
	}
	if s, ok := items.([]string); ok {
		if len(s) > 0 {
			return s[0]
		}
		return ""
	}
	val := reflect.ValueOf(items)
	if val.Kind() == reflect.Slice || val.Kind() == reflect.Array {
		if val.Len() > 0 {
			return fmt.Sprint(val.Index(0).Interface())
		}
		return ""
	}
	return fmt.Sprint(items)
}

// lastHelper returns the final string element from a slice or array.
func lastHelper(items any) string {
	if items == nil {
		return ""
	}
	if s, ok := items.([]string); ok {
		if len(s) > 0 {
			return s[len(s)-1]
		}
		return ""
	}
	val := reflect.ValueOf(items)
	if val.Kind() == reflect.Slice || val.Kind() == reflect.Array {
		if val.Len() > 0 {
			return fmt.Sprint(val.Index(val.Len() - 1).Interface())
		}
		return ""
	}
	return fmt.Sprint(items)
}

// defaultHelper returns fallback if val is nil, empty, or a zero value.
func defaultHelper(fallback, val any) any {
	if val == nil {
		return fallback
	}
	v := reflect.ValueOf(val)
	switch v.Kind() {
	case reflect.String:
		if strings.TrimSpace(v.String()) == "" {
			return fallback
		}
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map:
		if v.IsNil() || v.IsZero() {
			return fallback
		}
	default:
		if v.IsZero() {
			return fallback
		}
	}
	return val
}

// coalesceHelper returns the first non-empty string among the provided arguments.
func coalesceHelper(vals ...any) string {
	for _, v := range vals {
		if v == nil {
			continue
		}
		switch val := v.(type) {
		case string:
			if strings.TrimSpace(val) != "" {
				return val
			}
		case *string:
			if val != nil && strings.TrimSpace(*val) != "" {
				return *val
			}
		default:
			s := fmt.Sprint(val)
			if strings.TrimSpace(s) != "" && s != "<nil>" {
				return s
			}
		}
	}
	return ""
}

// safeCSSHelper wraps css as htmltemplate.CSS for explicit style attribute injection inside templates.
func safeCSSHelper(css any) htmltemplate.CSS {
	return htmltemplate.CSS(fmt.Sprint(css))
}

// SafeFuncMap provides sandboxed, whitelisted template helper functions with zero OS/filesystem access.
var SafeFuncMap = map[string]any{
	"formatDate":      formatDate,
	"formatDateRange": formatDateRange,
	"year":            year,
	"lower":           strings.ToLower,
	"upper":           strings.ToUpper,
	"title":           titleCase,
	"trim":            strings.TrimSpace,
	"trimPrefix":      strings.TrimPrefix,
	"trimSuffix":      strings.TrimSuffix,
	"replace":         strings.ReplaceAll,
	"contains":        strings.Contains,
	"hasPrefix":       strings.HasPrefix,
	"hasSuffix":       strings.HasSuffix,
	"join":            joinHelper,
	"split":           strings.Split,
	"first":           firstHelper,
	"last":            lastHelper,
	"default":         defaultHelper,
	"coalesce":        coalesceHelper,
	"safeCSS":         safeCSSHelper,
}
