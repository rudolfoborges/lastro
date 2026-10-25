package ledger

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

const (
	DefaultAnalysisMonths = 6
	maxAnalysisMonths     = 12
)

type MonthCoverage string

const (
	MonthComplete MonthCoverage = "complete"
	MonthPartial  MonthCoverage = "partial"
	MonthNone     MonthCoverage = "none"
)

type MonthTotal struct {
	Period
	Gross    Bucket
	Coverage MonthCoverage
}

type CategoryChange struct {
	Code       string
	Label      string
	Current    money.Money
	Previous   money.Money
	Difference money.Money
}

type Analysis struct {
	Currency        string
	Months          []MonthTotal
	SameDays        Comparison
	Categories      []CategoryChange
	Provisional     int
	Uncategorized   int
	HistoryStartsOn *calendar.Date
}

// Analysis compares months on the overview's rules: closed months as they
// are, the month in progress flagged as such, and the same days of the month
// before as the only fair comparison while the month is not over.
func (s *Service) Analysis(ctx context.Context, accountID uuid.UUID, months int, currency string, now time.Time) (Analysis, error) {
	if months == 0 {
		months = DefaultAnalysisMonths
	}
	if months < 1 || months > maxAnalysisMonths {
		return Analysis{}, &ValidationError{Field: "months", Message: fmt.Sprintf("must be between 1 and %d", maxAnalysisMonths)}
	}
	currency, err := normalizeCurrency(currency)
	if err != nil {
		return Analysis{}, err
	}
	analysis := Analysis{Currency: currency}
	var (
		loc           *time.Location
		rows          []purchaseRow
		periods       []Period
		firstTracking *time.Time
	)
	err = s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		if loc, err = t.location(); err != nil {
			return err
		}
		today := calendar.DateOf(now, loc)
		current, err := MonthPeriod("", today)
		if err != nil {
			return err
		}
		periods = []Period{current}
		for len(periods) < months {
			first := previousMonth(periods[0].From)
			earlier, err := MonthPeriod(first.Time().Format("2006-01"), today)
			if err != nil {
				return err
			}
			periods = append([]Period{earlier}, periods...)
		}
		if rows, err = t.expenseRows(periods[0].From, current.To); err != nil {
			return err
		}
		firstTracking, err = t.firstTrackingStart()
		return err
	})
	if err != nil {
		return Analysis{}, err
	}
	if firstTracking != nil {
		started := calendar.DateOf(*firstTracking, loc)
		analysis.HistoryStartsOn = &started
	}
	var currentCoverage PeriodCoverage
	for _, period := range periods {
		total := MonthTotal{Period: period, Gross: Bucket{Amount: money.Money{Currency: currency}}}
		for _, row := range rows {
			if row.Currency == currency && row.inTotal() && !row.on.Before(period.From) && !period.To.Before(row.on) {
				total.Gross.add(row.ID, row.amount)
			}
		}
		coverage, err := s.periodCoverage(ctx, accountID, period.From, period.To, loc, now)
		if err != nil {
			return Analysis{}, err
		}
		total.Coverage = monthCoverage(coverage, period, loc, now)
		analysis.Months = append(analysis.Months, total)
		currentCoverage = coverage
	}
	current := analysis.Months[len(analysis.Months)-1]
	analysis.SameDays, err = s.compare(ctx, accountID, comparisonBase{
		period: current.Period, currency: currency, current: current.Gross.Amount,
		coverage: currentCoverage, firstTracking: firstTracking, loc: loc, now: now,
	})
	if err != nil {
		return Analysis{}, err
	}
	for _, row := range rows {
		if row.Currency != currency || !row.inTotal() || row.on.Before(current.From) {
			continue
		}
		switch row.classification() {
		case AllocationSuggested:
			analysis.Provisional++
		case AllocationMissing:
			analysis.Uncategorized++
		}
	}
	if analysis.SameDays.Available {
		if analysis.Categories, err = s.categoryChanges(ctx, accountID, analysis.SameDays, currency); err != nil {
			return Analysis{}, err
		}
	}
	return analysis, nil
}

func monthCoverage(coverage PeriodCoverage, period Period, loc *time.Location, now time.Time) MonthCoverage {
	start, end := startOfDay(period.From, loc), startOfDay(period.To.AddDays(1), loc)
	if end.After(now) {
		end = now
	}
	switch {
	case coverage.Uncovered >= end.Sub(start):
		return MonthNone
	case coverage.Uncovered >= relevantGap:
		return MonthPartial
	default:
		return MonthComplete
	}
}

// categoryChanges compares the largest concluded categories of the same days;
// provisional and missing classifications stay out, since concluding them
// later would read as a change in spending.
func (s *Service) categoryChanges(ctx context.Context, accountID uuid.UUID, sameDays Comparison, currency string) ([]CategoryChange, error) {
	var current, previous []purchaseRow
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		if current, err = t.expenseRows(sameDays.CurrentFrom, sameDays.CurrentTo); err != nil {
			return err
		}
		previous, err = t.expenseRows(sameDays.PreviousFrom, sameDays.PreviousTo)
		return err
	})
	if err != nil {
		return nil, err
	}
	changes := map[string]*CategoryChange{}
	tally := func(rows []purchaseRow, pick func(*CategoryChange) *money.Money) {
		for _, row := range rows {
			if row.Currency != currency || !row.inTotal() || row.classification() != AllocationConfirmed {
				continue
			}
			code := categoryCodeOf(row.CategoryCode)
			change, ok := changes[code]
			if !ok {
				zero := money.Money{Currency: currency}
				change = &CategoryChange{Code: code, Label: labelOf(row), Current: zero, Previous: zero, Difference: zero}
				changes[code] = change
			}
			pick(change).Minor += row.amount.Minor
		}
	}
	tally(current, func(c *CategoryChange) *money.Money { return &c.Current })
	tally(previous, func(c *CategoryChange) *money.Money { return &c.Previous })
	out := make([]CategoryChange, 0, len(changes))
	for _, change := range changes {
		change.Difference.Minor = change.Current.Minor - change.Previous.Minor
		out = append(out, *change)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Current.Minor != out[j].Current.Minor {
			return out[i].Current.Minor > out[j].Current.Minor
		}
		if out[i].Previous.Minor != out[j].Previous.Minor {
			return out[i].Previous.Minor > out[j].Previous.Minor
		}
		return out[i].Label < out[j].Label
	})
	return out, nil
}
