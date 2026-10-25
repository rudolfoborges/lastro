package ledger

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	DefaultCurrency    = "BRL"
	topCategories      = 5
	basisPoints        = 10_000
	relevantGap        = 24 * time.Hour
	defaultExpensePage = 50
	maxExpensePage     = 200
	maxQuery           = 100
)

type DayCoverage string

const (
	DayComplete DayCoverage = "complete"
	DayPartial  DayCoverage = "partial"
	DayUnknown  DayCoverage = "unknown"
	DayFuture   DayCoverage = "future"
)

type GroupKind string

const (
	GroupOfCategory      GroupKind = "category"
	GroupOfOthers        GroupKind = "other"
	GroupOfProvisional   GroupKind = "provisional_classification"
	GroupOfUncategorized GroupKind = "uncategorized"
)

type ComparisonReason string

const (
	ComparisonNoHistory   ComparisonReason = "no_history"
	ComparisonCoverageGap ComparisonReason = "coverage_gap"
	ComparisonZeroBase    ComparisonReason = "zero_base"
)

// Period is a calendar month as far as it was observed: the current month
// ends today, so its days ahead never read as days without spending.
type Period struct {
	Month      string
	From       calendar.Date
	To         calendar.Date
	MonthEnd   calendar.Date
	InProgress bool
}

func MonthPeriod(month string, today calendar.Date) (Period, error) {
	first := calendar.Date{Year: today.Year, Month: today.Month, Day: 1}
	if month = strings.TrimSpace(month); month != "" {
		parsed, err := time.Parse("2006-01", month)
		if err != nil {
			return Period{}, &ValidationError{Field: "month", Message: "must be YYYY-MM"}
		}
		first = calendar.Date{Year: parsed.Year(), Month: parsed.Month(), Day: 1}
	}
	if today.Before(first) {
		return Period{}, &ValidationError{Field: "month", Message: "must not be in the future"}
	}
	end := first.AddDays(daysIn(first) - 1)
	period := Period{Month: first.Time().Format("2006-01"), From: first, To: end, MonthEnd: end}
	if !end.Before(today) {
		period.To, period.InProgress = today, true
	}
	return period, nil
}

func daysIn(first calendar.Date) int {
	return time.Date(first.Year, first.Month+1, 0, 0, 0, 0, 0, time.UTC).Day()
}

func previousMonth(first calendar.Date) calendar.Date {
	return calendar.DateFromTime(first.Time().AddDate(0, -1, 0))
}

// ExpenseTotals follows the dashboard rules: only confirmed expenses whose
// full amount is known make the gross total; refunds, provisional and pending
// values and purchases known only by installment stay apart.
type ExpenseTotals struct {
	Currency     string
	Gross        Bucket
	Refunds      Bucket
	Provisional  Bucket
	Pending      Bucket
	UnknownTotal int
}

func newExpenseTotals(currency string) ExpenseTotals {
	zero := money.Money{Currency: currency}
	return ExpenseTotals{Currency: currency, Gross: Bucket{Amount: zero}, Refunds: Bucket{Amount: zero}, Provisional: Bucket{Amount: zero}, Pending: Bucket{Amount: zero}}
}

func (e ExpenseTotals) Net() money.Money {
	return money.Money{Minor: e.Gross.Amount.Minor - e.Refunds.Amount.Minor, Currency: e.Currency}
}

func (e ExpenseTotals) Average() *money.Money {
	if e.Gross.Count == 0 {
		return nil
	}
	return &money.Money{Minor: roundedDiv(e.Gross.Amount.Minor, int64(e.Gross.Count)), Currency: e.Currency}
}

func (e *ExpenseTotals) add(row purchaseRow) {
	switch {
	case Nature(row.Nature) == NatureRefund:
		if Status(row.Status) == StatusConfirmed {
			e.Refunds.add(row.ID, row.amount)
		}
	case AmountBasis(row.AmountBasis) == AmountInstallment:
		e.UnknownTotal++
	case Status(row.Status) == StatusConfirmed:
		e.Gross.add(row.ID, row.amount)
	case Status(row.Status) == StatusProvisional:
		e.Provisional.add(row.ID, row.amount)
	default:
		e.Pending.add(row.ID, row.amount)
	}
}

func (r purchaseRow) inTotal() bool {
	return Nature(r.Nature) == NatureExpense && Status(r.Status) == StatusConfirmed && AmountBasis(r.AmountBasis) != AmountInstallment
}

func (r purchaseRow) classification() AllocationStatus {
	if r.CategoryStatus == nil {
		return AllocationMissing
	}
	return AllocationStatus(*r.CategoryStatus)
}

func roundedDiv(numerator, denominator int64) int64 {
	if numerator < 0 {
		return -roundedDiv(-numerator, denominator)
	}
	return (2*numerator + denominator) / (2 * denominator)
}

func share(part, whole money.Money) *int {
	if whole.Minor <= 0 {
		return nil
	}
	value := int(roundedDiv(part.Minor*basisPoints, whole.Minor))
	return &value
}

type CategoryGroup struct {
	Kind             GroupKind
	Key              string
	Label            string
	Codes            []string
	Classification   AllocationStatus
	Amount           Bucket
	ShareBasisPoints *int
}

// categoryGroups splits the gross total into exclusive groups: the five
// largest concluded categories, the other concluded ones together, and the
// provisional and missing classifications apart, never hidden among others.
func categoryGroups(rows []purchaseRow, currency string, gross money.Money) []CategoryGroup {
	zero := money.Money{Currency: currency}
	concluded := map[string]*CategoryGroup{}
	provisional := CategoryGroup{Kind: GroupOfProvisional, Key: string(GroupOfProvisional), Label: "Classificação provisória", Classification: AllocationSuggested, Amount: Bucket{Amount: zero}}
	missing := CategoryGroup{Kind: GroupOfUncategorized, Key: string(GroupOfUncategorized), Label: "Sem categoria", Classification: AllocationMissing, Amount: Bucket{Amount: zero}}
	for _, row := range rows {
		if row.Currency != currency || !row.inTotal() {
			continue
		}
		switch row.classification() {
		case AllocationConfirmed:
			code := categoryCodeOf(row.CategoryCode)
			group, ok := concluded[code]
			if !ok {
				group = &CategoryGroup{Kind: GroupOfCategory, Key: code, Label: labelOf(row), Codes: []string{code}, Classification: AllocationConfirmed, Amount: Bucket{Amount: zero}}
				concluded[code] = group
			}
			group.Amount.add(row.ID, row.amount)
		case AllocationSuggested:
			provisional.Amount.add(row.ID, row.amount)
		default:
			missing.Amount.add(row.ID, row.amount)
		}
	}
	ranked := make([]CategoryGroup, 0, len(concluded))
	for _, group := range concluded {
		ranked = append(ranked, *group)
	}
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].Amount.Amount.Minor != ranked[j].Amount.Amount.Minor {
			return ranked[i].Amount.Amount.Minor > ranked[j].Amount.Amount.Minor
		}
		return ranked[i].Label < ranked[j].Label
	})
	groups := ranked
	if len(ranked) > topCategories+1 {
		others := CategoryGroup{Kind: GroupOfOthers, Key: string(GroupOfOthers), Label: "Outras", Classification: AllocationConfirmed, Amount: Bucket{Amount: zero}}
		for _, group := range ranked[topCategories:] {
			others.Codes = append(others.Codes, group.Codes...)
			others.Amount.Amount.Minor += group.Amount.Amount.Minor
			others.Amount.Count += group.Amount.Count
			others.Amount.TransactionIDs = append(others.Amount.TransactionIDs, group.Amount.TransactionIDs...)
		}
		sort.Strings(others.Codes)
		groups = append(ranked[:topCategories:topCategories], others)
	}
	for _, group := range []CategoryGroup{provisional, missing} {
		if group.Amount.Count > 0 {
			groups = append(groups, group)
		}
	}
	for i := range groups {
		groups[i].ShareBasisPoints = share(groups[i].Amount.Amount, gross)
	}
	return groups
}

func labelOf(row purchaseRow) string {
	if row.CategoryLabel != nil {
		return *row.CategoryLabel
	}
	return "Não identificado"
}

type DayTotal struct {
	Date     calendar.Date
	Gross    Bucket
	Refunds  Bucket
	Coverage DayCoverage
}

func dayTotals(rows []purchaseRow, currency string, period Period, coverage PeriodCoverage, loc *time.Location, now time.Time) []DayTotal {
	zero := money.Money{Currency: currency}
	days := map[calendar.Date]*DayTotal{}
	var out []DayTotal
	for day := period.From; !period.MonthEnd.Before(day); day = day.AddDays(1) {
		out = append(out, DayTotal{Date: day, Gross: Bucket{Amount: zero}, Refunds: Bucket{Amount: zero}, Coverage: coverage.of(day, period, loc, now)})
	}
	for i := range out {
		days[out[i].Date] = &out[i]
	}
	for _, row := range rows {
		day, ok := days[row.on]
		if !ok || row.Currency != currency {
			continue
		}
		switch {
		case row.inTotal():
			day.Gross.add(row.ID, row.amount)
		case Nature(row.Nature) == NatureRefund && Status(row.Status) == StatusConfirmed:
			day.Refunds.add(row.ID, row.amount)
		}
	}
	return out
}

// PeriodCoverage tells how much of a period the automatic tracking watched:
// pauses and stretches without a sign of life from the phone are uncovered.
type PeriodCoverage struct {
	Complete    bool
	Uncovered   time.Duration
	Limitations []string
	Tracking    Tracking
	Inbox       InboxStatus
	Devices     []DeviceStatus
	windows     []Window
}

func (s *Service) periodCoverage(ctx context.Context, accountID uuid.UUID, from, to calendar.Date, loc *time.Location, now time.Time) (PeriodCoverage, error) {
	start, end := startOfDay(from, loc), startOfDay(to.AddDays(1), loc)
	if end.After(now) {
		end = now
	}
	if !start.Before(end) {
		return PeriodCoverage{Complete: true}, nil
	}
	coverage, err := s.Coverage(ctx, accountID, start, end, now)
	if err != nil {
		return PeriodCoverage{}, err
	}
	windows := slices.Concat(coverage.Inactive, coverage.Gaps)
	return PeriodCoverage{
		Complete:    coverage.Complete,
		Uncovered:   overlap(windows, start, end),
		Limitations: coverage.Limitations,
		Tracking:    coverage.Tracking,
		Inbox:       coverage.Inbox,
		Devices:     coverage.Devices,
		windows:     windows,
	}, nil
}

func (c PeriodCoverage) of(day calendar.Date, period Period, loc *time.Location, now time.Time) DayCoverage {
	if period.To.Before(day) {
		return DayFuture
	}
	start, end := startOfDay(day, loc), startOfDay(day.AddDays(1), loc)
	if end.After(now) {
		end = now
	}
	missing := overlap(c.windows, start, end)
	switch {
	case missing <= 0:
		return DayComplete
	case missing >= end.Sub(start):
		return DayUnknown
	default:
		return DayPartial
	}
}

func overlap(windows []Window, from, to time.Time) time.Duration {
	var total time.Duration
	for _, window := range windows {
		start, end := latest(window.From, from), window.To
		if to.Before(end) {
			end = to
		}
		if end.After(start) {
			total += end.Sub(start)
		}
	}
	return total
}

type Comparison struct {
	Available          bool
	Reason             ComparisonReason
	CurrentFrom        calendar.Date
	CurrentTo          calendar.Date
	PreviousFrom       calendar.Date
	PreviousTo         calendar.Date
	Current            money.Money
	Previous           money.Money
	Difference         money.Money
	PercentBasisPoints *int
}

// previousPeriod is the same stretch of the month before: the days observed so
// far for a month in progress, so a partial month never faces a whole one.
func previousPeriod(period Period) (calendar.Date, calendar.Date) {
	first := previousMonth(period.From)
	end := first.AddDays(daysIn(first) - 1)
	if !period.InProgress {
		return first, end
	}
	last := first.AddDays(period.To.Day - 1)
	if end.Before(last) {
		last = end
	}
	return first, last
}

type comparisonBase struct {
	period        Period
	currency      string
	current       money.Money
	coverage      PeriodCoverage
	firstTracking *time.Time
	loc           *time.Location
	now           time.Time
}

// compare suppresses the conclusion when the Lastro was not tracking the
// earlier period or a day or more went uncovered in either of them.
func (s *Service) compare(ctx context.Context, accountID uuid.UUID, base comparisonBase) (Comparison, error) {
	prevFrom, prevTo := previousPeriod(base.period)
	out := Comparison{CurrentFrom: base.period.From, CurrentTo: base.period.To, PreviousFrom: prevFrom, PreviousTo: prevTo, Current: base.current}
	var rows []purchaseRow
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		rows, err = t.expenseRows(prevFrom, prevTo)
		return err
	})
	if err != nil {
		return Comparison{}, err
	}
	out.Previous = money.Money{Currency: base.currency}
	for _, row := range rows {
		if row.Currency == base.currency && row.inTotal() {
			out.Previous.Minor += row.amount.Minor
		}
	}
	out.Difference = money.Money{Minor: out.Current.Minor - out.Previous.Minor, Currency: base.currency}
	if base.firstTracking == nil || base.firstTracking.After(startOfDay(prevFrom, base.loc)) {
		out.Reason = ComparisonNoHistory
		return out, nil
	}
	previous, err := s.periodCoverage(ctx, accountID, prevFrom, prevTo, base.loc, base.now)
	if err != nil {
		return Comparison{}, err
	}
	if base.coverage.Uncovered >= relevantGap || previous.Uncovered >= relevantGap {
		out.Reason = ComparisonCoverageGap
		return out, nil
	}
	out.Available = true
	if out.Previous.Minor == 0 {
		out.Reason = ComparisonZeroBase
		return out, nil
	}
	percent := int(roundedDiv(out.Difference.Minor*basisPoints, out.Previous.Minor))
	out.PercentBasisPoints = &percent
	return out, nil
}

type MonthOverview struct {
	Period
	Currency   string
	Currencies []string
	Totals     ExpenseTotals
	Categories []CategoryGroup
	Days       []DayTotal
	Comparison Comparison
	Coverage   PeriodCoverage
}

func (s *Service) MonthOverview(ctx context.Context, accountID uuid.UUID, month, currency string, now time.Time) (MonthOverview, error) {
	currency, err := normalizeCurrency(currency)
	if err != nil {
		return MonthOverview{}, err
	}
	overview := MonthOverview{Currency: currency}
	var (
		loc           *time.Location
		rows          []purchaseRow
		firstTracking *time.Time
	)
	err = s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		if loc, err = t.location(); err != nil {
			return err
		}
		if overview.Period, err = MonthPeriod(month, calendar.DateOf(now, loc)); err != nil {
			return err
		}
		if rows, err = t.expenseRows(overview.From, overview.To); err != nil {
			return err
		}
		firstTracking, err = t.firstTrackingStart()
		return err
	})
	if err != nil {
		return MonthOverview{}, err
	}
	overview.Currencies = currenciesOf(rows)
	overview.Totals = totalsOf(rows, currency)
	overview.Categories = categoryGroups(rows, currency, overview.Totals.Gross.Amount)
	if overview.Coverage, err = s.periodCoverage(ctx, accountID, overview.From, overview.To, loc, now); err != nil {
		return MonthOverview{}, err
	}
	overview.Days = dayTotals(rows, currency, overview.Period, overview.Coverage, loc, now)
	overview.Comparison, err = s.compare(ctx, accountID, comparisonBase{
		period: overview.Period, currency: currency, current: overview.Totals.Gross.Amount,
		coverage: overview.Coverage, firstTracking: firstTracking, loc: loc, now: now,
	})
	if err != nil {
		return MonthOverview{}, err
	}
	return overview, nil
}

func normalizeCurrency(currency string) (string, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return DefaultCurrency, nil
	}
	if err := money.ValidCurrency(currency); err != nil {
		return "", &ValidationError{Field: "currency", Message: err.Error()}
	}
	return currency, nil
}

func (t *tx) expenseRows(from, to calendar.Date) ([]purchaseRow, error) {
	raw, err := t.q.SpendingRows(t.ctx, tenantdb.SpendingRowsParams{AccountID: t.accountID, FromDay: from.Time(), ToDay: to.Time()})
	if err != nil {
		return nil, fmt.Errorf("spending rows: %w", err)
	}
	rows := make([]purchaseRow, len(raw))
	for i, row := range raw {
		rows[i] = purchaseRow{SpendingRowsRow: row, amount: money.Money{Minor: row.AmountMinor, Currency: row.Currency}, on: calendar.DateFromTime(*row.OccurredOn)}
	}
	return rows, nil
}

func (t *tx) firstTrackingStart() (*time.Time, error) {
	started, err := t.q.FirstTrackingStart(t.ctx, t.accountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("first tracking start: %w", err)
	}
	return &started, nil
}

func currenciesOf(rows []purchaseRow) []string {
	var currencies []string
	for _, row := range rows {
		if !slices.Contains(currencies, row.Currency) {
			currencies = append(currencies, row.Currency)
		}
	}
	sort.Strings(currencies)
	return currencies
}

func totalsOf(rows []purchaseRow, currency string) ExpenseTotals {
	totals := newExpenseTotals(currency)
	for _, row := range rows {
		if row.Currency == currency {
			totals.add(row)
		}
	}
	return totals
}

type ExpenseFilter struct {
	Currency        string
	Statuses        []Status
	Natures         []Nature
	Categories      []string
	Classifications []AllocationStatus
	Query           string
}

func (f ExpenseFilter) normalized() (ExpenseFilter, error) {
	var err error
	if f.Currency, err = normalizeCurrency(f.Currency); err != nil {
		return f, err
	}
	for _, status := range f.Statuses {
		if !slices.Contains(activeStatuses, status) {
			return f, &ValidationError{Field: "status", Message: "must be confirmed, provisional or pending_clarification"}
		}
	}
	for _, nature := range f.Natures {
		if nature != NatureExpense && nature != NatureRefund {
			return f, &ValidationError{Field: "nature", Message: "must be expense or refund"}
		}
	}
	for _, classification := range f.Classifications {
		if !slices.Contains([]AllocationStatus{AllocationConfirmed, AllocationSuggested, AllocationMissing}, classification) {
			return f, &ValidationError{Field: "classification", Message: "must be confirmed, suggested or missing"}
		}
	}
	f.Query = strings.TrimSpace(f.Query)
	if len([]rune(f.Query)) > maxQuery {
		return f, &ValidationError{Field: "q", Message: fmt.Sprintf("must have at most %d characters", maxQuery)}
	}
	return f, nil
}

func (f ExpenseFilter) matches(row purchaseRow) bool {
	switch {
	case row.Currency != f.Currency:
		return false
	case len(f.Statuses) > 0 && !slices.Contains(f.Statuses, Status(row.Status)):
		return false
	case len(f.Natures) > 0 && !slices.Contains(f.Natures, Nature(row.Nature)):
		return false
	case len(f.Classifications) > 0 && !slices.Contains(f.Classifications, row.classification()):
		return false
	case len(f.Categories) > 0 && (row.CategoryCode == nil || !slices.Contains(f.Categories, *row.CategoryCode)):
		return false
	case f.Query != "" && !strings.Contains(normalizedName(row.Description+" "+merchantOf(row)), normalizedName(f.Query)):
		return false
	}
	return true
}

type ExpenseItem struct {
	ID               uuid.UUID
	Description      string
	Merchant         *string
	Date             calendar.Date
	At               *time.Time
	Amount           money.Money
	AmountBasis      AmountBasis
	Nature           Nature
	Status           Status
	InTotal          bool
	Category         *CategoryState
	Classification   AllocationStatus
	PaymentKind      *PaymentKind
	CardLast4        *string
	InstallmentCount *int
	Modality         string
	createdAt        time.Time
}

func expenseItem(row purchaseRow) ExpenseItem {
	item := ExpenseItem{
		ID: row.ID, Description: row.Description, Merchant: row.Merchant, Date: row.on, At: row.OccurredAt,
		Amount: row.amount, AmountBasis: AmountBasis(row.AmountBasis), Nature: Nature(row.Nature), Status: Status(row.Status),
		InTotal: row.inTotal(), Classification: row.classification(), CardLast4: row.CardLast4, Modality: modalityKey(row.PaymentModality),
		createdAt: row.CreatedAt,
	}
	if row.CategoryCode != nil {
		item.Category = &CategoryState{Code: *row.CategoryCode, Label: labelOf(row), Status: item.Classification}
	}
	if row.PaymentKind != nil {
		kind := PaymentKind(*row.PaymentKind)
		item.PaymentKind = &kind
	}
	if row.InstallmentCount != nil {
		count := int(*row.InstallmentCount)
		item.InstallmentCount = &count
	}
	return item
}

func PaymentLabel(kind PaymentKind) string {
	return paymentLabel(kind)
}

type ExpenseList struct {
	From       calendar.Date
	To         calendar.Date
	Filter     ExpenseFilter
	Totals     ExpenseTotals
	Matching   int
	Items      []ExpenseItem
	Days       []DayTotal
	Offset     int
	NextOffset *int
}

// ListExpenses lists what composes a total under the same rules as the
// overview, so a list opened from a chart adds up to the bar it came from.
func (s *Service) ListExpenses(ctx context.Context, accountID uuid.UUID, from, to calendar.Date, filter ExpenseFilter, limit, offset int) (ExpenseList, error) {
	if err := validatePeriod(from, to); err != nil {
		return ExpenseList{}, err
	}
	filter, err := filter.normalized()
	if err != nil {
		return ExpenseList{}, err
	}
	switch {
	case limit < 0 || limit > maxExpensePage:
		return ExpenseList{}, &ValidationError{Field: "limit", Message: fmt.Sprintf("must be between 1 and %d", maxExpensePage)}
	case offset < 0:
		return ExpenseList{}, &ValidationError{Field: "offset", Message: "must not be negative"}
	case limit == 0:
		limit = defaultExpensePage
	}
	var rows []purchaseRow
	err = s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		rows, err = t.expenseRows(from, to)
		return err
	})
	if err != nil {
		return ExpenseList{}, err
	}
	list := ExpenseList{From: from, To: to, Filter: filter, Totals: newExpenseTotals(filter.Currency), Offset: offset}
	var matching []ExpenseItem
	dayGross := map[calendar.Date]*Bucket{}
	for _, row := range rows {
		if !filter.matches(row) {
			continue
		}
		list.Totals.add(row)
		matching = append(matching, expenseItem(row))
		if row.inTotal() {
			day, ok := dayGross[row.on]
			if !ok {
				day = &Bucket{Amount: money.Money{Currency: filter.Currency}}
				dayGross[row.on] = day
			}
			day.add(row.ID, row.amount)
		}
	}
	sort.SliceStable(matching, func(i, j int) bool { return newerFirst(matching[i], matching[j]) })
	list.Matching = len(matching)
	end := min(offset+limit, len(matching))
	if offset < end {
		list.Items = matching[offset:end]
	}
	if end < len(matching) {
		list.NextOffset = &end
	}
	for _, item := range list.Items {
		if len(list.Days) > 0 && list.Days[len(list.Days)-1].Date == item.Date {
			continue
		}
		total := DayTotal{Date: item.Date, Gross: Bucket{Amount: money.Money{Currency: filter.Currency}}}
		if day, ok := dayGross[item.Date]; ok {
			total.Gross = *day
		}
		list.Days = append(list.Days, total)
	}
	return list, nil
}

func newerFirst(a, b ExpenseItem) bool {
	if a.Date != b.Date {
		return b.Date.Before(a.Date)
	}
	switch {
	case a.At != nil && b.At != nil && !a.At.Equal(*b.At):
		return a.At.After(*b.At)
	case a.At != nil && b.At == nil:
		return true
	case a.At == nil && b.At != nil:
		return false
	}
	return a.createdAt.After(b.createdAt)
}

type LinkedNotification struct {
	EvidenceStatus EvidenceStatus
	NotificationID uuid.UUID
	Version        int
	Event          NotificationEvent
	PostedAt       time.Time
	ReceivedAt     time.Time
}

type ExpenseDetail struct {
	TransactionDetail
	InTotal       bool
	Notifications []LinkedNotification
	OnStatement   bool
	LastChange    *Revision
}

// ExpenseDetail explains a record as it is: its status apart from its
// category's, and the notifications and statement line behind it.
func (s *Service) ExpenseDetail(ctx context.Context, accountID, id uuid.UUID) (ExpenseDetail, error) {
	var detail ExpenseDetail
	err := s.inTx(ctx, accountID, func(t *tx) error {
		base, err := t.transactionDetail(id)
		if err != nil {
			return err
		}
		detail = ExpenseDetail{
			TransactionDetail: base,
			InTotal:           base.Nature == NatureExpense && base.Status == StatusConfirmed && base.AmountBasis != AmountInstallment,
		}
		statuses := map[uuid.UUID]EvidenceStatus{}
		var observationIDs []uuid.UUID
		for _, evidence := range base.Evidence {
			if evidence.Status != EvidenceConfirmed {
				continue
			}
			switch evidence.Source {
			case SourceWalletNotification:
				statuses[evidence.ObservationID] = evidence.Status
				observationIDs = append(observationIDs, evidence.ObservationID)
			case SourceStatementLine:
				detail.OnStatement = true
			}
		}
		if len(observationIDs) > 0 {
			rows, err := t.q.NotificationVersionsOfObservations(t.ctx, tenantdb.NotificationVersionsOfObservationsParams{AccountID: t.accountID, ObservationIds: observationIDs})
			if err != nil {
				return fmt.Errorf("notifications of evidence: %w", err)
			}
			for _, row := range rows {
				detail.Notifications = append(detail.Notifications, LinkedNotification{
					EvidenceStatus: statuses[row.ObservationID], NotificationID: row.NotificationID, Version: int(row.Version),
					Event: NotificationEvent(row.Event), PostedAt: row.PostedAt, ReceivedAt: row.ReceivedAt,
				})
			}
			sort.Slice(detail.Notifications, func(i, j int) bool { return detail.Notifications[i].PostedAt.Before(detail.Notifications[j].PostedAt) })
		}
		if len(base.History) > 0 {
			last := base.History[len(base.History)-1]
			detail.LastChange = &last
		}
		return nil
	})
	return detail, err
}
