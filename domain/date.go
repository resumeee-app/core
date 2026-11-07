package domain

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// DateFormatISO is the canonical ISO-8601 calendar date layout.
const DateFormatISO = "2006-01-02"

// Date represents a full calendar date (year, month, day) backed by a native Go time.Time.
//
// Invariant: Time components (hour, minute, second, nanosecond) are always zeroed out
// at UTC midnight. Date serializes to and deserializes from ISO-8601 "YYYY-MM-DD" in JSON
// and persists as an ISO date string in SQL storage backends.
type Date struct {
	time.Time
}

// NewDate constructs a Date for the specified year, month, and day at UTC midnight.
//
// Parameters:
//   - year: Calendar year (e.g., 2024).
//   - month: Calendar month (1-12).
//   - day: Day of month (1-31).
//
// Returns:
//   - Date: Canonical Date instance normalized to UTC midnight.
func NewDate(year int, month time.Month, day int) Date {
	return Date{
		Time: time.Date(year, month, day, 0, 0, 0, 0, time.UTC),
	}
}

// FromTime constructs a Date by extracting the calendar year, month, and day from
// an existing time.Time instance, normalizing to UTC midnight.
//
// Parameters:
//   - t: Source time.Time instance.
//
// Returns:
//   - Date: Date instance set to the calendar day of t at UTC midnight.
func FromTime(t time.Time) Date {
	if t.IsZero() {
		return Date{}
	}
	return Date{
		Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC),
	}
}

// ParseDate parses a string representation into a Date.
//
// Supported formats include ISO-8601 calendar date ("2006-01-02"), RFC3339 timestamps,
// and year-month strings ("2006-01", defaulting day to 1). Empty or whitespace-only
// strings return a zero Date with nil error.
//
// Parameters:
//   - s: Date string to parse.
//
// Returns:
//   - Date: Parsed Date instance normalized to UTC midnight.
//   - error: Non-nil if the string cannot be parsed into a recognized date layout.
func ParseDate(s string) (Date, error) {
	trimmed := strings.TrimSpace(s)
	if trimmed == "" || trimmed == "null" || strings.EqualFold(trimmed, "present") {
		return Date{}, nil
	}

	layouts := []string{
		DateFormatISO,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01",
		"2006/01/02",
		"2006/01",
	}

	for _, layout := range layouts {
		if t, err := time.Parse(layout, trimmed); err == nil {
			return Date{
				Time: time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC),
			}, nil
		}
	}

	return Date{}, fmt.Errorf("%w: invalid date string %q", ErrValidation, s)
}

// DatePtr returns a pointer to the provided Date value.
//
// Parameters:
//   - d: Date instance.
//
// Returns:
//   - *Date: Pointer to d.
func DatePtr(d Date) *Date {
	return &d
}

// String formats the Date as an ISO-8601 calendar date string ("YYYY-MM-DD"),
// or returns an empty string if the Date is zero.
//
// Returns:
//   - string: Formatted date string.
func (d Date) String() string {
	if d.IsZero() {
		return ""
	}
	return d.Format(DateFormatISO)
}

// MarshalJSON serializes the Date into a quoted ISO-8601 string ("YYYY-MM-DD"),
// or "null" if the Date is zero.
//
// Returns:
//   - []byte: JSON-encoded bytes.
//   - error: Always nil.
func (d Date) MarshalJSON() ([]byte, error) {
	if d.IsZero() {
		return []byte("null"), nil
	}
	return json.Marshal(d.String())
}

// UnmarshalJSON deserializes a JSON string or null into a Date instance.
//
// Parameters:
//   - data: JSON-encoded bytes.
//
// Returns:
//   - error: Non-nil if data contains an invalid date format.
func (d *Date) UnmarshalJSON(data []byte) error {
	s := strings.Trim(string(data), `"`)
	if s == "" || s == "null" {
		d.Time = time.Time{}
		return nil
	}
	parsed, err := ParseDate(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// Value implements the database/sql/driver.Valuer interface for database persistence.
//
// Returns:
//   - driver.Value: ISO-8601 date string or nil if zero.
//   - error: Always nil.
func (d Date) Value() (driver.Value, error) {
	if d.IsZero() {
		return nil, nil
	}
	return d.String(), nil
}

// Scan implements the database/sql.Scanner interface for database retrieval.
//
// Parameters:
//   - value: Raw database column value.
//
// Returns:
//   - error: Non-nil if value cannot be converted into a Date.
func (d *Date) Scan(value any) error {
	if value == nil {
		d.Time = time.Time{}
		return nil
	}

	switch v := value.(type) {
	case time.Time:
		*d = FromTime(v)
		return nil
	case string:
		parsed, err := ParseDate(v)
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	case []byte:
		parsed, err := ParseDate(string(v))
		if err != nil {
			return err
		}
		*d = parsed
		return nil
	default:
		return fmt.Errorf("cannot scan %T into domain.Date: %v", value, value)
	}
}

// EqualDate returns true if d and other represent the same calendar day.
//
// Parameters:
//   - other: The Date instance to compare with.
//
// Returns:
//   - bool: True if both dates share identical year, month, and day.
func (d Date) EqualDate(other Date) bool {
	if d.IsZero() && other.IsZero() {
		return true
	}
	if d.IsZero() || other.IsZero() {
		return false
	}
	return d.Year() == other.Year() && d.Month() == other.Month() && d.Day() == other.Day()
}

// BeforeDate returns true if d is strictly before other on the calendar.
//
// Parameters:
//   - other: The Date instance to compare against.
//
// Returns:
//   - bool: True if d is earlier than other.
func (d Date) BeforeDate(other Date) bool {
	return d.Before(other.Time)
}

// AfterDate returns true if d is strictly after other on the calendar.
//
// Parameters:
//   - other: The Date instance to compare against.
//
// Returns:
//   - bool: True if d is later than other.
func (d Date) AfterDate(other Date) bool {
	return d.After(other.Time)
}
