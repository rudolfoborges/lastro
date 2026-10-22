package calendar_test

import (
	"errors"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
)

func saoPaulo(t *testing.T) *time.Location {
	t.Helper()
	loc, err := calendar.LoadTimeZone(calendar.DefaultTimeZone)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestNowLateAtNightStaysOnTheLocalDay(t *testing.T) {
	messageAt := time.Date(2026, 10, 22, 2, 50, 0, 0, time.UTC)

	got, err := calendar.Resolve(calendar.ReferenceNow, messageAt, saoPaulo(t))

	if err != nil {
		t.Fatal(err)
	}
	if got.On == nil || got.On.String() != "2026-10-21" {
		t.Errorf("local date = %v, want 2026-10-21 (23:50 in São Paulo)", got.On)
	}
	if got.At == nil || !got.At.Equal(messageAt) || got.At.Location() != time.UTC {
		t.Errorf("instant = %v, want %v in UTC", got.At, messageAt)
	}
	if got.Precision != calendar.PrecisionExact || got.Source != calendar.SourceInferred {
		t.Errorf("precision/source = %s/%s, want exact/inferred", got.Precision, got.Source)
	}
}

func TestTodayAndYesterdayResolveToInferredDays(t *testing.T) {
	messageAt := time.Date(2026, 11, 1, 1, 30, 0, 0, time.UTC)
	tests := map[calendar.Reference]string{
		calendar.ReferenceToday:     "2026-10-31",
		calendar.ReferenceYesterday: "2026-10-30",
	}
	for reference, want := range tests {
		t.Run(string(reference), func(t *testing.T) {
			got, err := calendar.Resolve(reference, messageAt, saoPaulo(t))
			if err != nil {
				t.Fatal(err)
			}
			if got.On == nil || got.On.String() != want || got.At != nil {
				t.Errorf("Resolve(%s) = %+v, want day %s without instant", reference, got, want)
			}
			if got.Precision != calendar.PrecisionDay || got.Source != calendar.SourceInferred {
				t.Errorf("precision/source = %s/%s, want day/inferred", got.Precision, got.Source)
			}
		})
	}
}

func TestYesterdayCrossesMonthAndYearBoundaries(t *testing.T) {
	messageAt := time.Date(2027, 1, 1, 15, 0, 0, 0, time.UTC)

	got, err := calendar.Resolve(calendar.ReferenceYesterday, messageAt, saoPaulo(t))

	if err != nil || got.On.String() != "2026-12-31" {
		t.Errorf("Resolve(yesterday) = %v, %v, want 2026-12-31", got.On, err)
	}
}

func TestUnknownReferencesAreRejected(t *testing.T) {
	_, err := calendar.Resolve("last week", time.Now(), saoPaulo(t))
	if !errors.Is(err, calendar.ErrUnknownReference) {
		t.Errorf("Resolve(last week) error = %v, want ErrUnknownReference", err)
	}
}

func TestPartialDatesKeepTheirPrecision(t *testing.T) {
	month := calendar.InMonth(2026, time.September, calendar.SourceDocument)
	if month.Precision != calendar.PrecisionMonth || month.On.String() != "2026-09-01" || month.At != nil {
		t.Errorf("InMonth() = %+v", month)
	}
	unknown := calendar.Unknown()
	if unknown.Known() || unknown.On != nil || unknown.At != nil {
		t.Errorf("Unknown() = %+v", unknown)
	}
}

func TestParseDateAcceptsOnlyRealCalendarDates(t *testing.T) {
	if got, err := calendar.ParseDate("2028-02-29"); err != nil || got.Format() != "29/02/2028" {
		t.Errorf("ParseDate(leap day) = %v, %v", got, err)
	}
	for _, value := range []string{"2026-02-30", "21/10/2026", "2026-10-21T10:00:00Z", ""} {
		if _, err := calendar.ParseDate(value); err == nil {
			t.Errorf("ParseDate(%q) error = nil, want error", value)
		}
	}
}

func TestLoadTimeZoneAcceptsOnlyIANANames(t *testing.T) {
	if _, err := calendar.LoadTimeZone("Europe/Lisbon"); err != nil {
		t.Errorf("LoadTimeZone(Europe/Lisbon) error = %v", err)
	}
	for _, name := range []string{"", "Local", "Brasilia", "GMT-3x", "../etc/passwd"} {
		if _, err := calendar.LoadTimeZone(name); err == nil {
			t.Errorf("LoadTimeZone(%q) error = nil, want error", name)
		}
	}
}
