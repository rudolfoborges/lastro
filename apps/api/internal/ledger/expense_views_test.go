package ledger_test

import (
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func (f *fixture) spend(key string, minor int64, on string, method *ledger.PaymentMethodRef, category string, status ledger.AllocationStatus) ledger.Transaction {
	f.t.Helper()
	report := supermarket(key)
	report.RawText, report.Description = "gasto "+key, "Compra "+key
	report.Amount, report.DateReference, report.OccurredOn = ptr(brl(minor)), nil, day(on)
	report.PaymentMethod, report.ConfirmDistinct = method, true
	report.Category, report.CategoryStatus = nil, ""
	if category != "" {
		report.Category, report.CategoryStatus = ptr(category), status
	}
	return *f.record(report).Transaction
}

var pix = &ledger.PaymentMethodRef{Kind: ledger.PaymentPix}

func (f *fixture) overview(month string, now time.Time) ledger.MonthOverview {
	f.t.Helper()
	overview, err := f.ledger.MonthOverview(f.t.Context(), f.account, month, "", now)
	if err != nil {
		f.t.Fatalf("MonthOverview() error = %v", err)
	}
	return overview
}

func (f *fixture) beatEvery(step time.Duration, from, to time.Time) {
	f.t.Helper()
	for at := from; !at.After(to); at = at.Add(step) {
		_, err := f.ledger.RecordHeartbeat(f.t.Context(), f.account, ledger.Heartbeat{
			DeviceID: "galaxy-s24", SentAt: at, ClientVersion: "0.3.0", NotificationAccess: true, ReceivedAt: at,
		})
		if err != nil {
			f.t.Fatal(err)
		}
	}
}

func groupByKey(groups []ledger.CategoryGroup) map[string]ledger.CategoryGroup {
	out := map[string]ledger.CategoryGroup{}
	for _, group := range groups {
		out[group.Key] = group
	}
	return out
}

func TestTheOverviewKeepsWhatIsOutsideTheGrossTotalApart(t *testing.T) {
	f := newFixture(t)
	groceries := f.spend("a", 10000, "2026-09-03", pix, "groceries", ledger.AllocationConfirmed)
	f.spend("b", 5000, "2026-09-04", pix, "meals", ledger.AllocationSuggested)
	f.spend("c", 2000, "2026-09-05", pix, "", "")
	f.spend("d", 7000, "2026-09-06", credit("1234"), "groceries", ledger.AllocationConfirmed)
	refund := storeReport("e", 1000)
	refund.RawText, refund.Description, refund.Category = "A loja devolveu 10", "", nil
	refund.Nature, refund.RefundOfID, refund.OccurredOn, refund.DateReference, refund.ConfirmDistinct = ledger.NatureRefund, &groceries.ID, day("2026-09-07"), nil, true
	f.record(refund)
	f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installmentLine("LOJA DE TV 01/12", 1, 12, 12345, "2026-09-10")))

	got := f.overview("2026-09", lateNight)

	totals := got.Totals
	if totals.Gross.Amount != brl(17000) || totals.Gross.Count != 3 || totals.Refunds.Amount != brl(1000) || totals.Net() != brl(16000) {
		t.Errorf("gross %v (%d), refunds %v, net %v; want R$ 170,00 in 3, R$ 10,00 refunded, R$ 160,00 net", totals.Gross.Amount, totals.Gross.Count, totals.Refunds.Amount, totals.Net())
	}
	if totals.Provisional.Amount != brl(7000) || totals.UnknownTotal != 1 || *totals.Average() != brl(5667) {
		t.Errorf("provisional %v, unknown total %d, average %v; want R$ 70,00 apart, 1 installment purchase apart, R$ 56,67", totals.Provisional.Amount, totals.UnknownTotal, totals.Average())
	}
	groups := groupByKey(got.Categories)
	if len(got.Categories) != 3 || groups["groceries"].Amount.Amount != brl(10000) || groups["provisional_classification"].Amount.Amount != brl(5000) || groups["uncategorized"].Amount.Amount != brl(2000) {
		t.Fatalf("categories = %+v, want groceries, the provisional classification and the missing one apart", got.Categories)
	}
	if *groups["groceries"].ShareBasisPoints != 5882 || *groups["provisional_classification"].ShareBasisPoints != 2941 || *groups["uncategorized"].ShareBasisPoints != 1176 {
		t.Errorf("shares = %d, %d, %d; want 58,82%%, 29,41%% and 11,76%%", *groups["groceries"].ShareBasisPoints, *groups["provisional_classification"].ShareBasisPoints, *groups["uncategorized"].ShareBasisPoints)
	}
	if len(got.Days) != 30 || got.Days[2].Gross.Amount != brl(10000) || got.Days[6].Refunds.Amount != brl(1000) || got.Days[9].Gross.Count != 0 {
		t.Errorf("days = %d, day 3 %v, day 7 refunds %v, day 10 %d; want 30 days with the installment purchase out", len(got.Days), got.Days[2].Gross.Amount, got.Days[6].Refunds.Amount, got.Days[9].Gross.Count)
	}
	if got.Days[0].Coverage != ledger.DayUnknown || got.InProgress || got.Comparison.Available || got.Comparison.Reason != ledger.ComparisonNoHistory {
		t.Errorf("coverage %s, in progress %v, comparison %+v; want an untracked closed month with no comparison", got.Days[0].Coverage, got.InProgress, got.Comparison)
	}
}

func TestOnlyTheFiveLargestConcludedCategoriesStandAlone(t *testing.T) {
	f := newFixture(t)
	for i, code := range []string{"groceries", "meals", "fuel", "transport", "home", "health", "education"} {
		f.spend(code, int64(7000-i*1000), "2026-09-0"+string(rune('1'+i)), pix, code, ledger.AllocationConfirmed)
	}

	got := f.overview("2026-09", lateNight)

	if len(got.Categories) != 6 {
		t.Fatalf("categories = %+v, want five categories and the others", got.Categories)
	}
	others := got.Categories[5]
	if got.Categories[0].Key != "groceries" || got.Categories[4].Key != "home" || others.Kind != ledger.GroupOfOthers {
		t.Errorf("order = %s ... %s, %s; want the largest first and the others last", got.Categories[0].Key, got.Categories[4].Key, others.Kind)
	}
	if others.Amount.Amount != brl(3000) || others.Amount.Count != 2 || len(others.Codes) != 2 || others.Codes[0] != "education" {
		t.Errorf("others = %+v, want health and education together", others)
	}
}

func TestTheMonthInProgressStopsTodayAndComparesTheSameDays(t *testing.T) {
	f := newFixture(t)
	f.setTracking(true, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC), nil)
	now := time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)
	f.beatEvery(50*time.Minute, time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC), time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC))
	f.beatEvery(50*time.Minute, time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC), now)
	f.spend("september", 10000, "2026-09-02", pix, "groceries", ledger.AllocationConfirmed)
	f.spend("later-in-september", 99999, "2026-09-20", pix, "groceries", ledger.AllocationConfirmed)
	f.spend("october", 15000, "2026-10-02", pix, "groceries", ledger.AllocationConfirmed)

	got := f.overview("", now)

	if got.Month != "2026-10" || !got.InProgress || got.To != *day("2026-10-03") || len(got.Days) != 31 {
		t.Fatalf("period = %+v with %d days, want October up to the 3rd", got.Period, len(got.Days))
	}
	if got.Days[0].Coverage != ledger.DayComplete || got.Days[2].Coverage != ledger.DayComplete || got.Days[3].Coverage != ledger.DayFuture {
		t.Errorf("coverage = %s, %s, %s; want covered days then the future", got.Days[0].Coverage, got.Days[2].Coverage, got.Days[3].Coverage)
	}
	comparison := got.Comparison
	if !comparison.Available || comparison.PreviousTo != *day("2026-09-03") || comparison.Previous != brl(10000) || comparison.Difference != brl(5000) || *comparison.PercentBasisPoints != 5000 {
		t.Errorf("comparison = %+v, want R$ 50,00 more (+50%%) than September 1 to 3", comparison)
	}
}

func TestComparisonsNeedACoveredBaseAndAPositiveOne(t *testing.T) {
	f := newFixture(t)
	f.setTracking(true, time.Date(2026, 8, 31, 12, 0, 0, 0, time.UTC), nil)
	now := time.Date(2026, 10, 3, 15, 0, 0, 0, time.UTC)
	f.beatEvery(50*time.Minute, time.Date(2026, 9, 1, 2, 0, 0, 0, time.UTC), time.Date(2026, 9, 4, 4, 0, 0, 0, time.UTC))
	f.spend("october", 15000, "2026-10-02", pix, "groceries", ledger.AllocationConfirmed)

	withoutPhone := f.overview("", now)
	f.beatEvery(50*time.Minute, time.Date(2026, 10, 1, 2, 0, 0, 0, time.UTC), now)
	withoutBase := f.overview("", now)

	if withoutPhone.Comparison.Available || withoutPhone.Comparison.Reason != ledger.ComparisonCoverageGap || withoutPhone.Days[1].Coverage != ledger.DayUnknown {
		t.Errorf("without the phone = %+v (day 2 %s), want the comparison suppressed by the gap", withoutPhone.Comparison, withoutPhone.Days[1].Coverage)
	}
	if !withoutBase.Comparison.Available || withoutBase.Comparison.Reason != ledger.ComparisonZeroBase || withoutBase.Comparison.PercentBasisPoints != nil {
		t.Errorf("without base = %+v, want the difference without a percentage", withoutBase.Comparison)
	}
}

func TestAListOpenedFromAGroupAddsUpToIt(t *testing.T) {
	f := newFixture(t)
	for i, code := range []string{"groceries", "meals", "fuel", "transport", "home", "health", "education"} {
		f.spend(code, int64(7000-i*1000), "2026-09-0"+string(rune('1'+i)), pix, code, ledger.AllocationConfirmed)
	}
	f.spend("suggested", 1500, "2026-09-12", pix, "meals", ledger.AllocationSuggested)
	f.spend("missing", 900, "2026-09-12", pix, "", "")
	f.spend("provisional", 4200, "2026-09-12", credit("1234"), "groceries", ledger.AllocationConfirmed)
	overview := f.overview("2026-09", lateNight)

	for _, group := range overview.Categories {
		filter := ledger.ExpenseFilter{
			Statuses: []ledger.Status{ledger.StatusConfirmed}, Natures: []ledger.Nature{ledger.NatureExpense},
			Classifications: []ledger.AllocationStatus{group.Classification},
		}
		if group.Kind == ledger.GroupOfCategory || group.Kind == ledger.GroupOfOthers {
			filter.Categories = group.Codes
		}
		list, err := f.ledger.ListExpenses(t.Context(), f.account, overview.From, overview.To, filter, 0, 0)
		if err != nil {
			t.Fatal(err)
		}
		if list.Totals.Gross.Amount != group.Amount.Amount || list.Totals.Gross.Count != group.Amount.Count || list.Matching != group.Amount.Count {
			t.Errorf("list of %s = %v in %d, want %v in %d", group.Key, list.Totals.Gross.Amount, list.Matching, group.Amount.Amount, group.Amount.Count)
		}
	}
}

func TestTheListShowsTheNewestFirstAndPagesThroughEverything(t *testing.T) {
	f := newFixture(t)
	f.spend("first", 1000, "2026-09-01", pix, "groceries", ledger.AllocationConfirmed)
	f.spend("second", 2000, "2026-09-02", pix, "groceries", ledger.AllocationConfirmed)
	f.spend("third", 3000, "2026-09-02", credit("1234"), "groceries", ledger.AllocationConfirmed)
	from, to := *day("2026-09-01"), *day("2026-09-30")

	page, err := f.ledger.ListExpenses(t.Context(), f.account, from, to, ledger.ExpenseFilter{}, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	rest, err := f.ledger.ListExpenses(t.Context(), f.account, from, to, ledger.ExpenseFilter{}, 2, *page.NextOffset)
	if err != nil {
		t.Fatal(err)
	}
	search, err := f.ledger.ListExpenses(t.Context(), f.account, from, to, ledger.ExpenseFilter{Query: "SECOND"}, 0, 0)
	if err != nil {
		t.Fatal(err)
	}

	if page.Matching != 3 || len(page.Items) != 2 || page.Items[0].Date != *day("2026-09-02") || page.Items[1].Date != *day("2026-09-02") {
		t.Fatalf("first page = %+v, want the two purchases of September 2nd", page.Items)
	}
	if page.Totals.Gross.Amount != brl(3000) || page.Totals.Provisional.Amount != brl(3000) || page.Items[0].InTotal == page.Items[1].InTotal {
		t.Errorf("totals = %v gross, %v provisional; want the provisional one apart", page.Totals.Gross.Amount, page.Totals.Provisional.Amount)
	}
	if len(page.Days) != 1 || page.Days[0].Gross.Amount != brl(2000) || len(rest.Items) != 1 || rest.NextOffset != nil || rest.Items[0].Amount != brl(1000) {
		t.Errorf("days = %+v, rest = %+v; want the day subtotal and the last purchase", page.Days, rest.Items)
	}
	if search.Matching != 1 || search.Items[0].Description != "Compra second" {
		t.Errorf("search = %+v, want the second purchase only", search.Items)
	}
}

func TestListFiltersAreValidated(t *testing.T) {
	f := newFixture(t)
	from, to := *day("2026-09-01"), *day("2026-09-30")
	tests := map[string]struct {
		filter ledger.ExpenseFilter
		limit  int
		field  string
	}{
		"status":         {ledger.ExpenseFilter{Statuses: []ledger.Status{ledger.StatusVoided}}, 0, "status"},
		"nature":         {ledger.ExpenseFilter{Natures: []ledger.Nature{ledger.NaturePayment}}, 0, "nature"},
		"classification": {ledger.ExpenseFilter{Classifications: []ledger.AllocationStatus{"maybe"}}, 0, "classification"},
		"currency":       {ledger.ExpenseFilter{Currency: "XXX"}, 0, "currency"},
		"limit":          {ledger.ExpenseFilter{}, 500, "limit"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := f.ledger.ListExpenses(t.Context(), f.account, from, to, tt.filter, tt.limit, 0)
			assertValidation(t, err, tt.field)
		})
	}
}

func TestMonthPeriodsNeverReachTheFuture(t *testing.T) {
	today := calendar.Date{Year: 2026, Month: time.October, Day: 25}

	current, err := ledger.MonthPeriod("", today)
	if err != nil {
		t.Fatal(err)
	}
	closed, err := ledger.MonthPeriod("2026-02", today)
	if err != nil {
		t.Fatal(err)
	}
	_, future := ledger.MonthPeriod("2026-11", today)

	if current.Month != "2026-10" || current.To != today || current.MonthEnd.Day != 31 || !current.InProgress {
		t.Errorf("current = %+v, want October up to the 25th", current)
	}
	if closed.To.Day != 28 || closed.InProgress {
		t.Errorf("closed = %+v, want the whole of February", closed)
	}
	assertValidation(t, future, "month")
}

func TestTheExpenseDetailShowsItsNotificationsAndKeepsStatusesApart(t *testing.T) {
	f := newFixture(t)
	observation := f.walletObservation(walletPurchase("key-1", "R$ 32,97 com Visa •••• 1234"))
	outcome := f.interpret(purchaseReading(observation, brl(3297)))

	got, err := f.ledger.ExpenseDetail(t.Context(), f.account, outcome.Transaction.ID)
	if err != nil {
		t.Fatal(err)
	}

	if got.Status != ledger.StatusProvisional || got.InTotal || got.Category.Status != ledger.AllocationSuggested {
		t.Errorf("status %s, in total %v, category %s; want a provisional expense with a suggested category", got.Status, got.InTotal, got.Category.Status)
	}
	if len(got.Notifications) != 1 || got.Notifications[0].Version != 1 || got.OnStatement {
		t.Errorf("notifications = %+v, on statement %v; want the one notification behind it", got.Notifications, got.OnStatement)
	}
	if got.LastChange == nil || got.LastChange.Author != ledger.AuthorAgent {
		t.Errorf("last change = %+v, want the Case's", got.LastChange)
	}
}
