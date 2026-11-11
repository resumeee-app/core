package domain_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/resumeee-app/resumeee-core/domain"
)

func TestDate_NewAndFromTime(t *testing.T) {
	d := domain.NewDate(2024, time.March, 15)
	if d.Year() != 2024 || d.Month() != time.March || d.Day() != 15 {
		t.Fatalf("expected 2024-03-15, got %v", d)
	}
	if d.String() != "2024-03-15" {
		t.Errorf("expected string '2024-03-15', got %q", d.String())
	}

	utcTime := time.Date(2023, time.December, 31, 23, 59, 59, 999, time.UTC)
	fromT := domain.FromTime(utcTime)
	if fromT.Year() != 2023 || fromT.Month() != time.December || fromT.Day() != 31 {
		t.Errorf("expected 2023-12-31, got %v", fromT)
	}
	if fromT.Hour() != 0 || fromT.Minute() != 0 || fromT.Second() != 0 {
		t.Errorf("expected midnight UTC, got %v", fromT.Time)
	}
}

func TestDate_ParseDate(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantY   int
		wantM   time.Month
		wantD   int
		wantErr bool
	}{
		{"full ISO date", "2024-05-18", 2024, time.May, 18, false},
		{"rfc3339 timestamp", "2024-05-18T14:30:00Z", 2024, time.May, 18, false},
		{"year-month", "2024-05", 2024, time.May, 1, false},
		{"empty string", "", 0, 0, 0, false},
		{"whitespace only", "   ", 0, 0, 0, false},
		{"invalid format", "invalid-date", 0, 0, 0, true},
		{"invalid numbers", "2024-99-99", 0, 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseDate(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseDate(%q) error = %v, wantErr %v", tt.input, err, tt.wantErr)
			}
			if !tt.wantErr && tt.wantY != 0 {
				if got.Year() != tt.wantY || got.Month() != tt.wantM || got.Day() != tt.wantD {
					t.Errorf("ParseDate(%q) = %v, want %d-%d-%d", tt.input, got, tt.wantY, tt.wantM, tt.wantD)
				}
			}
			if !tt.wantErr && tt.wantY == 0 {
				if !got.IsZero() {
					t.Errorf("expected zero Date for empty input, got %v", got)
				}
			}
		})
	}
}

func TestDate_JSONSerialization(t *testing.T) {
	type Sample struct {
		Start domain.Date  `json:"start"`
		End   *domain.Date `json:"end,omitempty"`
	}

	endDate := domain.NewDate(2025, time.February, 28)
	sample := Sample{
		Start: domain.NewDate(2022, time.January, 1),
		End:   &endDate,
	}

	data, err := json.Marshal(sample)
	if err != nil {
		t.Fatalf("Marshal failed: %v", err)
	}
	expectedJSON := `{"start":"2022-01-01","end":"2025-02-28"}`
	if string(data) != expectedJSON {
		t.Errorf("expected %s, got %s", expectedJSON, string(data))
	}

	// Unmarshal full dates
	var decoded Sample
	if err := json.Unmarshal([]byte(expectedJSON), &decoded); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if decoded.Start.String() != "2022-01-01" {
		t.Errorf("expected Start '2022-01-01', got %q", decoded.Start.String())
	}
	if decoded.End == nil || decoded.End.String() != "2025-02-28" {
		t.Errorf("expected End '2025-02-28', got %v", decoded.End)
	}

	// Unmarshal null / omitted end date
	var decodedNilEnd Sample
	if err := json.Unmarshal([]byte(`{"start":"2020-06-15","end":null}`), &decodedNilEnd); err != nil {
		t.Fatalf("Unmarshal with null end failed: %v", err)
	}
	if decodedNilEnd.End != nil {
		t.Errorf("expected nil End, got %v", decodedNilEnd.End)
	}

	// Unmarshal RFC3339 string
	var decodedRFC Sample
	if err := json.Unmarshal([]byte(`{"start":"2020-06-15T00:00:00Z"}`), &decodedRFC); err != nil {
		t.Fatalf("Unmarshal with RFC3339 failed: %v", err)
	}
	if decodedRFC.Start.String() != "2020-06-15" {
		t.Errorf("expected Start '2020-06-15', got %q", decodedRFC.Start.String())
	}
}

func TestDate_SQLValuerAndScanner(t *testing.T) {
	d := domain.NewDate(2024, time.July, 4)

	val, err := d.Value()
	if err != nil {
		t.Fatalf("Value() error: %v", err)
	}
	if val != "2024-07-04" {
		t.Errorf("expected Valuer '2024-07-04', got %v", val)
	}

	// Zero date value
	zeroD := domain.Date{}
	zeroVal, err := zeroD.Value()
	if err != nil {
		t.Fatalf("zero Date Value() error: %v", err)
	}
	if zeroVal != nil {
		t.Errorf("expected nil driver.Value for zero date, got %v", zeroVal)
	}

	// Scan from string
	var scanned domain.Date
	if err := scanned.Scan("2024-07-04"); err != nil {
		t.Fatalf("Scan(string) error: %v", err)
	}
	if scanned.String() != "2024-07-04" {
		t.Errorf("expected scanned date '2024-07-04', got %q", scanned.String())
	}

	// Scan from []byte
	var scannedBytes domain.Date
	if err := scannedBytes.Scan([]byte("2024-07-04")); err != nil {
		t.Fatalf("Scan([]byte) error: %v", err)
	}
	if scannedBytes.String() != "2024-07-04" {
		t.Errorf("expected scanned date '2024-07-04', got %q", scannedBytes.String())
	}

	// Scan from time.Time
	var scannedTime domain.Date
	tInput := time.Date(2024, time.July, 4, 12, 0, 0, 0, time.UTC)
	if err := scannedTime.Scan(tInput); err != nil {
		t.Fatalf("Scan(time.Time) error: %v", err)
	}
	if scannedTime.String() != "2024-07-04" {
		t.Errorf("expected scanned date '2024-07-04', got %q", scannedTime.String())
	}

	// Scan from nil
	var scannedNil domain.Date
	if err := scannedNil.Scan(nil); err != nil {
		t.Fatalf("Scan(nil) error: %v", err)
	}
	if !scannedNil.IsZero() {
		t.Errorf("expected zero Date from nil scan, got %v", scannedNil)
	}
}

func TestDate_ComparisonsAndHelpers(t *testing.T) {
	d1 := domain.NewDate(2022, time.January, 1)
	d2 := domain.NewDate(2022, time.January, 1)
	d3 := domain.NewDate(2023, time.January, 1)

	if !d1.EqualDate(d2) {
		t.Errorf("expected d1 == d2")
	}
	if !d1.BeforeDate(d3) {
		t.Errorf("expected d1 < d3")
	}
	if !d3.AfterDate(d1) {
		t.Errorf("expected d3 > d1")
	}

	ptr := domain.DatePtr(d1)
	if ptr == nil || !ptr.EqualDate(d1) {
		t.Errorf("expected DatePtr to match d1")
	}
}
