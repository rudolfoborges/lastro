package ledger_test

import (
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func TestTheAnalysisSeparatesClosedMonthsFromTheOneInProgress(t *testing.T) {
	f := newFixture(t)
	f.setTracking(true, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC), nil)
	now := time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)
	f.beatEvery(50*time.Minute, time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC), time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC))
	f.beatEvery(50*time.Minute, time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC), now)
	f.spend("july", 5000, "2026-07-10", pix, "groceries", ledger.AllocationConfirmed)
	f.spend("september", 10000, "2026-09-02", pix, "groceries", ledger.AllocationConfirmed)
	f.spend("later-in-september", 20000, "2026-09-20", pix, "fuel", ledger.AllocationConfirmed)
	f.spend("october", 15000, "2026-10-02", pix, "groceries", ledger.AllocationConfirmed)
	f.spend("october-suggested", 3000, "2026-10-02", pix, "meals", ledger.AllocationSuggested)

	got, err := f.ledger.Analysis(t.Context(), f.account, 4, "", now)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Months) != 4 || got.Months[0].Month != "2026-07" || !got.Months[3].InProgress || got.Months[2].InProgress {
		t.Fatalf("months = %+v, want July to October with October in progress", got.Months)
	}
	if got.Months[0].Coverage != ledger.MonthNone || got.Months[2].Coverage != ledger.MonthPartial || got.Months[3].Coverage != ledger.MonthComplete {
		t.Errorf("coverage = %s, %s, %s; want July untracked, September with gaps, October covered", got.Months[0].Coverage, got.Months[2].Coverage, got.Months[3].Coverage)
	}
	if got.Months[2].Gross.Amount != brl(30000) || got.Months[3].Gross.Amount != brl(18000) {
		t.Errorf("gross = %v and %v, want R$ 300,00 in September and R$ 180,00 so far in October", got.Months[2].Gross.Amount, got.Months[3].Gross.Amount)
	}
	if !got.SameDays.Available || got.SameDays.Previous != brl(10000) || got.HistoryStartsOn == nil || *got.HistoryStartsOn != *day("2026-08-31") {
		t.Errorf("same days = %+v, history from %v; want September 1 to 3 against October 1 to 3", got.SameDays, got.HistoryStartsOn)
	}
	if len(got.Categories) != 1 || got.Categories[0].Code != "groceries" || got.Categories[0].Difference != brl(5000) || got.Provisional != 1 {
		t.Errorf("categories = %+v, provisional = %d; want groceries up R$ 50,00 and the suggested one left out", got.Categories, got.Provisional)
	}
}

func TestTheAnalysisWindowIsBounded(t *testing.T) {
	f := newFixture(t)

	_, err := f.ledger.Analysis(t.Context(), f.account, 13, "", lateNight)

	assertValidation(t, err, "months")
}
