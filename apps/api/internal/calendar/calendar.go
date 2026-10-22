package calendar

import (
	"errors"
	"fmt"
	"time"
)

const DefaultTimeZone = "America/Sao_Paulo"

type Precision string

const (
	PrecisionExact   Precision = "exact"
	PrecisionDay     Precision = "day"
	PrecisionMonth   Precision = "month"
	PrecisionUnknown Precision = "unknown"
)

type Source string

const (
	SourceStated   Source = "stated"
	SourceInferred Source = "inferred"
	SourceDocument Source = "document"
)

type Reference string

const (
	ReferenceNow       Reference = "now"
	ReferenceToday     Reference = "today"
	ReferenceYesterday Reference = "yesterday"
)

var ErrUnknownReference = errors.New(`date reference must be "now", "today" or "yesterday"`)

type Date struct {
	Year  int
	Month time.Month
	Day   int
}

func ParseDate(value string) (Date, error) {
	t, err := time.Parse(time.DateOnly, value)
	if err != nil {
		return Date{}, fmt.Errorf("date %q must be a calendar date such as 2026-10-21", value)
	}
	return DateFromTime(t), nil
}

func DateOf(instant time.Time, loc *time.Location) Date {
	return DateFromTime(instant.In(loc))
}

func DateFromTime(t time.Time) Date {
	year, month, day := t.Date()
	return Date{Year: year, Month: month, Day: day}
}

func (d Date) AddDays(days int) Date {
	return DateFromTime(d.Time().AddDate(0, 0, days))
}

func (d Date) Time() time.Time {
	return time.Date(d.Year, d.Month, d.Day, 0, 0, 0, 0, time.UTC)
}

func (d Date) String() string {
	return d.Time().Format(time.DateOnly)
}

func (d Date) Format() string {
	return d.Time().Format("02/01/2006")
}

func (d Date) Before(other Date) bool {
	return d.Time().Before(other.Time())
}

type Occurrence struct {
	At        *time.Time
	On        *Date
	Precision Precision
	Source    Source
}

func Exact(instant time.Time, loc *time.Location, source Source) Occurrence {
	at := instant.UTC()
	on := DateOf(instant, loc)
	return Occurrence{At: &at, On: &on, Precision: PrecisionExact, Source: source}
}

func OnDay(day Date, source Source) Occurrence {
	return Occurrence{On: &day, Precision: PrecisionDay, Source: source}
}

func InMonth(year int, month time.Month, source Source) Occurrence {
	first := Date{Year: year, Month: month, Day: 1}
	return Occurrence{On: &first, Precision: PrecisionMonth, Source: source}
}

func Unknown() Occurrence {
	return Occurrence{Precision: PrecisionUnknown, Source: SourceStated}
}

func Resolve(reference Reference, messageAt time.Time, loc *time.Location) (Occurrence, error) {
	switch reference {
	case ReferenceNow:
		return Exact(messageAt, loc, SourceInferred), nil
	case ReferenceToday:
		return OnDay(DateOf(messageAt, loc), SourceInferred), nil
	case ReferenceYesterday:
		return OnDay(DateOf(messageAt, loc).AddDays(-1), SourceInferred), nil
	default:
		return Occurrence{}, fmt.Errorf("%w: got %q", ErrUnknownReference, reference)
	}
}

func (o Occurrence) Known() bool {
	return o.Precision != PrecisionUnknown
}

func LoadTimeZone(name string) (*time.Location, error) {
	if name == "" || name == "Local" {
		return nil, fmt.Errorf("time zone %q must be an IANA name such as %s", name, DefaultTimeZone)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, fmt.Errorf("time zone %q must be an IANA name such as %s", name, DefaultTimeZone)
	}
	return loc, nil
}
