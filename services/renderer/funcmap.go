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
)

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

var fourDigitsRegex = regexp.MustCompile(`\b(19\d\d|20\d\d)\b`)

// parseDate attempts to parse a date string using common ISO and human-readable formats.
func parseDate(s string) (time.Time, error) {
	for _, layout := range dateParseLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, errors.New("cannot parse date")
}

// formatAutoDate formats a date string as YYYY or Jan 2006 depending on input precision.
func formatAutoDate(s string) string {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" {
		return ""
	}
	t, err := parseDate(trimmed)
	if err != nil {
		return trimmed
	}
	if len(trimmed) == 4 {
		return t.Format("2006")
	}
	return t.Format("Jan 2006")
}

// formatDate formats a given date string using a standard Go time layout.
func formatDate(layout, dateStr string) string {
	if strings.TrimSpace(dateStr) == "" {
		return ""
	}
	t, err := parseDate(strings.TrimSpace(dateStr))
	if err != nil {
		return dateStr
	}
	return t.Format(layout)
}

// formatDateRange formats a start and end date interval with an optional present label.
func formatDateRange(start string, end any, presentLabel string) string {
	if strings.TrimSpace(presentLabel) == "" {
		presentLabel = "Present"
	}
	var endStr string
	isEndEmpty := false
	switch v := end.(type) {
	case nil:
		isEndEmpty = true
	case *string:
		if v == nil || strings.TrimSpace(*v) == "" {
			isEndEmpty = true
		} else {
			endStr = *v
		}
	case string:
		if strings.TrimSpace(v) == "" {
			isEndEmpty = true
		} else {
			endStr = v
		}
	default:
		s := fmt.Sprint(end)
		if strings.TrimSpace(s) == "" || s == "<nil>" {
			isEndEmpty = true
		} else {
			endStr = s
		}
	}

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

// year extracts a 4-digit calendar year from a date string.
func year(dateStr string) string {
	trimmed := strings.TrimSpace(dateStr)
	if trimmed == "" {
		return ""
	}
	if t, err := parseDate(trimmed); err == nil {
		return fmt.Sprintf("%04d", t.Year())
	}
	if match := fourDigitsRegex.FindString(trimmed); match != "" {
		return match
	}
	return dateStr
}

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
