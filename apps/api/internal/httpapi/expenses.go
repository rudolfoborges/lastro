package httpapi

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

const purchaseDateBasis = "purchase_date"

type moneyJSON struct {
	Minor     int64  `json:"minor"`
	Currency  string `json:"currency"`
	Formatted string `json:"formatted"`
}

type tallyJSON struct {
	Amount moneyJSON `json:"amount"`
	Count  int       `json:"count"`
}

type periodJSON struct {
	Month      string `json:"month,omitempty"`
	From       string `json:"from"`
	To         string `json:"to"`
	MonthEnd   string `json:"month_end,omitempty"`
	InProgress bool   `json:"in_progress"`
	Basis      string `json:"basis"`
	Timezone   string `json:"timezone"`
}

type totalsJSON struct {
	Gross        tallyJSON  `json:"gross"`
	Average      *moneyJSON `json:"average"`
	Refunds      tallyJSON  `json:"refunds"`
	Net          moneyJSON  `json:"net"`
	Provisional  tallyJSON  `json:"provisional"`
	Pending      tallyJSON  `json:"pending"`
	UnknownTotal int        `json:"unknown_total"`
}

type groupJSON struct {
	Kind             ledger.GroupKind    `json:"kind"`
	Key              string              `json:"key"`
	Label            string              `json:"label"`
	Categories       int                 `json:"categories"`
	Gross            tallyJSON           `json:"gross"`
	ShareBasisPoints *int                `json:"share_basis_points"`
	Filter           map[string][]string `json:"filter"`
}

type dayJSON struct {
	Date     string             `json:"date"`
	Gross    tallyJSON          `json:"gross"`
	Refunds  *tallyJSON         `json:"refunds,omitempty"`
	Coverage ledger.DayCoverage `json:"coverage,omitempty"`
}

type rangeJSON struct {
	From  string    `json:"from"`
	To    string    `json:"to"`
	Gross moneyJSON `json:"gross"`
}

type comparisonJSON struct {
	Available          bool                    `json:"available"`
	Reason             ledger.ComparisonReason `json:"reason,omitempty"`
	Current            rangeJSON               `json:"current"`
	Previous           rangeJSON               `json:"previous"`
	Difference         *moneyJSON              `json:"difference,omitempty"`
	PercentBasisPoints *int                    `json:"percent_basis_points,omitempty"`
}

type trackingJSON struct {
	State ledger.TrackingState `json:"state"`
	Since *time.Time           `json:"since,omitempty"`
}

type inboxJSON struct {
	Pending         int        `json:"pending"`
	OldestPendingAt *time.Time `json:"oldest_pending_at,omitempty"`
	LastProcessedAt *time.Time `json:"last_processed_at,omitempty"`
}

type deviceJSON struct {
	DeviceID            string     `json:"device_id"`
	LastContactAt       time.Time  `json:"last_contact_at"`
	LastHeartbeatAt     *time.Time `json:"last_heartbeat_at,omitempty"`
	NotificationAccess  *bool      `json:"notification_access,omitempty"`
	BatteryUnrestricted *bool      `json:"battery_unrestricted,omitempty"`
	QueueSize           *int       `json:"queue_size,omitempty"`
}

type coverageJSON struct {
	Complete    bool         `json:"complete"`
	Limitations []string     `json:"limitations"`
	Tracking    trackingJSON `json:"tracking"`
	Inbox       inboxJSON    `json:"inbox"`
	Devices     []deviceJSON `json:"devices"`
}

type overviewJSON struct {
	GeneratedAt time.Time      `json:"generated_at"`
	Period      periodJSON     `json:"period"`
	Currency    string         `json:"currency"`
	Currencies  []string       `json:"currencies"`
	Totals      totalsJSON     `json:"totals"`
	Categories  []groupJSON    `json:"categories"`
	Days        []dayJSON      `json:"days"`
	Comparison  comparisonJSON `json:"comparison"`
	Coverage    coverageJSON   `json:"coverage"`
}

func (a *api) handleOverview(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	query := r.URL.Query()
	now := time.Now()
	overview, err := a.ledger.MonthOverview(r.Context(), principal.AccountID, query.Get("month"), query.Get("currency"), now)
	if err != nil {
		a.writeLedgerError(w, r, err)
		return
	}
	loc, err := a.accounts.Location(r.Context(), principal.AccountID)
	if err != nil {
		a.writeInternalError(w, r, err)
		return
	}
	body := overviewJSON{
		GeneratedAt: now.UTC(),
		Period:      monthPeriodJSON(overview.Period, loc),
		Currency:    overview.Currency,
		Currencies:  nonNil(overview.Currencies),
		Totals:      toTotalsJSON(overview.Totals),
		Categories:  make([]groupJSON, len(overview.Categories)),
		Days:        make([]dayJSON, len(overview.Days)),
		Comparison:  toComparisonJSON(overview.Comparison),
		Coverage:    toCoverageJSON(overview.Coverage),
	}
	for i, group := range overview.Categories {
		body.Categories[i] = toGroupJSON(group, overview.Period, overview.Currency)
	}
	for i, day := range overview.Days {
		refunds := toTallyJSON(day.Refunds)
		body.Days[i] = dayJSON{Date: day.Date.String(), Gross: toTallyJSON(day.Gross), Refunds: &refunds, Coverage: day.Coverage}
	}
	writeJSON(w, http.StatusOK, body)
}

func monthPeriodJSON(period ledger.Period, loc *time.Location) periodJSON {
	return periodJSON{
		Month: period.Month, From: period.From.String(), To: period.To.String(), MonthEnd: period.MonthEnd.String(),
		InProgress: period.InProgress, Basis: purchaseDateBasis, Timezone: loc.String(),
	}
}

func toMoneyJSON(m money.Money) moneyJSON {
	return moneyJSON{Minor: m.Minor, Currency: m.Currency, Formatted: m.Format()}
}

func toTallyJSON(bucket ledger.Bucket) tallyJSON {
	return tallyJSON{Amount: toMoneyJSON(bucket.Amount), Count: bucket.Count}
}

func toTotalsJSON(totals ledger.ExpenseTotals) totalsJSON {
	out := totalsJSON{
		Gross:        toTallyJSON(totals.Gross),
		Refunds:      toTallyJSON(totals.Refunds),
		Net:          toMoneyJSON(totals.Net()),
		Provisional:  toTallyJSON(totals.Provisional),
		Pending:      toTallyJSON(totals.Pending),
		UnknownTotal: totals.UnknownTotal,
	}
	if average := totals.Average(); average != nil {
		value := toMoneyJSON(*average)
		out.Average = &value
	}
	return out
}

// toGroupJSON carries the list filter that adds up to the group, so opening
// a bar keeps its period, currency and rules.
func toGroupJSON(group ledger.CategoryGroup, period ledger.Period, currency string) groupJSON {
	filter := map[string][]string{
		"from":           {period.From.String()},
		"to":             {period.To.String()},
		"currency":       {currency},
		"status":         {string(ledger.StatusConfirmed)},
		"nature":         {string(ledger.NatureExpense)},
		"classification": {string(group.Classification)},
	}
	if group.Kind == ledger.GroupOfCategory || group.Kind == ledger.GroupOfOthers {
		filter["category"] = group.Codes
	}
	return groupJSON{
		Kind: group.Kind, Key: group.Key, Label: group.Label, Categories: len(group.Codes),
		Gross: toTallyJSON(group.Amount), ShareBasisPoints: group.ShareBasisPoints, Filter: filter,
	}
}

func toComparisonJSON(c ledger.Comparison) comparisonJSON {
	out := comparisonJSON{
		Available: c.Available,
		Reason:    c.Reason,
		Current:   rangeJSON{From: c.CurrentFrom.String(), To: c.CurrentTo.String(), Gross: toMoneyJSON(c.Current)},
		Previous:  rangeJSON{From: c.PreviousFrom.String(), To: c.PreviousTo.String(), Gross: toMoneyJSON(c.Previous)},
	}
	if c.Available {
		difference := toMoneyJSON(c.Difference)
		out.Difference, out.PercentBasisPoints = &difference, c.PercentBasisPoints
	}
	return out
}

func toCoverageJSON(c ledger.PeriodCoverage) coverageJSON {
	out := coverageJSON{
		Complete:    c.Complete,
		Limitations: nonNil(c.Limitations),
		Tracking:    trackingJSON{State: c.Tracking.State, Since: utc(c.Tracking.Since)},
		Inbox:       inboxJSON{Pending: c.Inbox.Pending, OldestPendingAt: utc(c.Inbox.OldestPendingAt), LastProcessedAt: utc(c.Inbox.LastProcessedAt)},
		Devices:     make([]deviceJSON, len(c.Devices)),
	}
	if out.Tracking.State == "" {
		out.Tracking.State = ledger.TrackingNeverEnabled
	}
	for i, device := range c.Devices {
		out.Devices[i] = deviceJSON{
			DeviceID: device.DeviceID, LastContactAt: device.LastContactAt.UTC(), LastHeartbeatAt: utc(device.LastHeartbeatAt),
			NotificationAccess: device.NotificationAccess, BatteryUnrestricted: device.BatteryUnrestricted, QueueSize: device.QueueSize,
		}
	}
	return out
}

func nonNil(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}

type categoryJSON struct {
	Code   string                  `json:"code"`
	Label  string                  `json:"label"`
	Status ledger.AllocationStatus `json:"status"`
	Origin ledger.Author           `json:"origin,omitempty"`
}

type paymentJSON struct {
	Kind      ledger.PaymentKind `json:"kind"`
	Label     string             `json:"label"`
	CardLast4 *string            `json:"card_last4,omitempty"`
}

type expenseItemJSON struct {
	ID             uuid.UUID               `json:"id"`
	Description    string                  `json:"description"`
	Merchant       *string                 `json:"merchant,omitempty"`
	Date           string                  `json:"date"`
	Time           *string                 `json:"time,omitempty"`
	Amount         moneyJSON               `json:"amount"`
	AmountBasis    ledger.AmountBasis      `json:"amount_basis"`
	Nature         ledger.Nature           `json:"nature"`
	Status         ledger.Status           `json:"status"`
	InTotal        bool                    `json:"in_total"`
	Classification ledger.AllocationStatus `json:"classification"`
	Category       *categoryJSON           `json:"category,omitempty"`
	Payment        *paymentJSON            `json:"payment,omitempty"`
	Installments   *int                    `json:"installments,omitempty"`
}

type expenseListJSON struct {
	Period     periodJSON        `json:"period"`
	Filter     map[string]any    `json:"filter"`
	Totals     totalsJSON        `json:"totals"`
	Matching   int               `json:"matching"`
	Items      []expenseItemJSON `json:"items"`
	Days       []dayJSON         `json:"days"`
	NextOffset *int              `json:"next_offset"`
}

func (a *api) handleListExpenses(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	query := r.URL.Query()
	loc, err := a.accounts.Location(r.Context(), principal.AccountID)
	if err != nil {
		a.writeInternalError(w, r, err)
		return
	}
	from, to, period, err := listPeriod(query, calendar.DateOf(time.Now(), loc))
	if err != nil {
		a.writeLedgerError(w, r, err)
		return
	}
	limit, offset, err := pageOf(query)
	if err != nil {
		a.writeLedgerError(w, r, err)
		return
	}
	filter := ledger.ExpenseFilter{
		Currency:   query.Get("currency"),
		Categories: values(query, "category"),
		Query:      query.Get("q"),
	}
	for _, status := range values(query, "status") {
		filter.Statuses = append(filter.Statuses, ledger.Status(status))
	}
	for _, nature := range values(query, "nature") {
		filter.Natures = append(filter.Natures, ledger.Nature(nature))
	}
	for _, classification := range values(query, "classification") {
		filter.Classifications = append(filter.Classifications, ledger.AllocationStatus(classification))
	}
	list, err := a.ledger.ListExpenses(r.Context(), principal.AccountID, from, to, filter, limit, offset)
	if err != nil {
		a.writeLedgerError(w, r, err)
		return
	}
	period.Basis, period.Timezone = purchaseDateBasis, loc.String()
	body := expenseListJSON{
		Period:     period,
		Filter:     filterJSON(list.Filter),
		Totals:     toTotalsJSON(list.Totals),
		Matching:   list.Matching,
		Items:      make([]expenseItemJSON, len(list.Items)),
		Days:       make([]dayJSON, len(list.Days)),
		NextOffset: list.NextOffset,
	}
	for i, item := range list.Items {
		body.Items[i] = toExpenseItemJSON(item, loc)
	}
	for i, day := range list.Days {
		body.Days[i] = dayJSON{Date: day.Date.String(), Gross: toTallyJSON(day.Gross)}
	}
	writeJSON(w, http.StatusOK, body)
}

// listPeriod reads a month, or from and to; the current month so far by
// default.
func listPeriod(query url.Values, today calendar.Date) (calendar.Date, calendar.Date, periodJSON, error) {
	from, to := strings.TrimSpace(query.Get("from")), strings.TrimSpace(query.Get("to"))
	if from == "" && to == "" {
		period, err := ledger.MonthPeriod(query.Get("month"), today)
		if err != nil {
			return calendar.Date{}, calendar.Date{}, periodJSON{}, err
		}
		body := periodJSON{Month: period.Month, From: period.From.String(), To: period.To.String(), MonthEnd: period.MonthEnd.String(), InProgress: period.InProgress}
		return period.From, period.To, body, nil
	}
	if from == "" || to == "" {
		return calendar.Date{}, calendar.Date{}, periodJSON{}, &ledger.ValidationError{Field: "from", Message: "from and to go together"}
	}
	first, err := calendar.ParseDate(from)
	if err != nil {
		return calendar.Date{}, calendar.Date{}, periodJSON{}, &ledger.ValidationError{Field: "from", Message: "must be YYYY-MM-DD"}
	}
	last, err := calendar.ParseDate(to)
	if err != nil {
		return calendar.Date{}, calendar.Date{}, periodJSON{}, &ledger.ValidationError{Field: "to", Message: "must be YYYY-MM-DD"}
	}
	return first, last, periodJSON{From: first.String(), To: last.String(), InProgress: !last.Before(today)}, nil
}

func pageOf(query url.Values) (int, int, error) {
	limit, offset := 0, 0
	var err error
	if raw := query.Get("limit"); raw != "" {
		if limit, err = strconv.Atoi(raw); err != nil || limit < 1 {
			return 0, 0, &ledger.ValidationError{Field: "limit", Message: "must be a positive integer"}
		}
	}
	if raw := query.Get("offset"); raw != "" {
		if offset, err = strconv.Atoi(raw); err != nil || offset < 0 {
			return 0, 0, &ledger.ValidationError{Field: "offset", Message: "must be zero or a positive integer"}
		}
	}
	return limit, offset, nil
}

func values(query url.Values, key string) []string {
	var out []string
	for _, raw := range query[key] {
		for _, value := range strings.Split(raw, ",") {
			if value = strings.TrimSpace(value); value != "" {
				out = append(out, value)
			}
		}
	}
	return out
}

func filterJSON(filter ledger.ExpenseFilter) map[string]any {
	out := map[string]any{"currency": filter.Currency}
	if len(filter.Statuses) > 0 {
		out["status"] = filter.Statuses
	}
	if len(filter.Natures) > 0 {
		out["nature"] = filter.Natures
	}
	if len(filter.Categories) > 0 {
		out["category"] = filter.Categories
	}
	if len(filter.Classifications) > 0 {
		out["classification"] = filter.Classifications
	}
	if filter.Query != "" {
		out["q"] = filter.Query
	}
	return out
}

func toExpenseItemJSON(item ledger.ExpenseItem, loc *time.Location) expenseItemJSON {
	out := expenseItemJSON{
		ID: item.ID, Description: item.Description, Merchant: item.Merchant, Date: item.Date.String(), Time: localTime(item.At, loc),
		Amount: toMoneyJSON(item.Amount), AmountBasis: item.AmountBasis, Nature: item.Nature, Status: item.Status,
		InTotal: item.InTotal, Classification: item.Classification, Installments: item.InstallmentCount,
	}
	if item.Category != nil {
		out.Category = &categoryJSON{Code: item.Category.Code, Label: item.Category.Label, Status: item.Category.Status}
	}
	if item.PaymentKind != nil {
		out.Payment = &paymentJSON{Kind: *item.PaymentKind, Label: ledger.PaymentLabel(*item.PaymentKind), CardLast4: item.CardLast4}
	}
	return out
}

func localTime(at *time.Time, loc *time.Location) *string {
	if at == nil {
		return nil
	}
	value := at.In(loc).Format("15:04")
	return &value
}

type installmentPlanJSON struct {
	Modality           ledger.PaymentModality `json:"modality"`
	Count              int                    `json:"count"`
	InstallmentAmount  *moneyJSON             `json:"installment_amount,omitempty"`
	Total              *moneyJSON             `json:"total,omitempty"`
	Charged            int                    `json:"charged"`
	KnownFuture        int                    `json:"known_future"`
	Estimated          int                    `json:"estimated"`
	RemainingKnown     moneyJSON              `json:"remaining_known"`
	RemainingEstimated moneyJSON              `json:"remaining_estimated"`
}

type linkedNotificationJSON struct {
	NotificationID uuid.UUID                `json:"notification_id"`
	Version        int                      `json:"version"`
	Event          ledger.NotificationEvent `json:"event"`
	PostedAt       time.Time                `json:"posted_at"`
	ReceivedAt     time.Time                `json:"received_at"`
}

type refundJSON struct {
	ID     uuid.UUID     `json:"id"`
	Amount moneyJSON     `json:"amount"`
	Date   *string       `json:"date,omitempty"`
	Status ledger.Status `json:"status"`
}

type changeJSON struct {
	At     time.Time     `json:"at"`
	Author ledger.Author `json:"author"`
}

type expenseDetailJSON struct {
	ID             uuid.UUID                `json:"id"`
	Description    string                   `json:"description"`
	Merchant       *string                  `json:"merchant,omitempty"`
	Payee          *string                  `json:"payee,omitempty"`
	Date           *string                  `json:"date,omitempty"`
	Time           *string                  `json:"time,omitempty"`
	DatePrecision  string                   `json:"date_precision"`
	Amount         moneyJSON                `json:"amount"`
	AmountBasis    ledger.AmountBasis       `json:"amount_basis"`
	OriginalAmount *moneyJSON               `json:"original_amount,omitempty"`
	Nature         ledger.Nature            `json:"nature"`
	Status         ledger.Status            `json:"status"`
	InTotal        bool                     `json:"in_total"`
	MergedIntoID   *uuid.UUID               `json:"merged_into_id,omitempty"`
	Classification ledger.AllocationStatus  `json:"classification"`
	Category       *categoryJSON            `json:"category,omitempty"`
	Purpose        *string                  `json:"purpose,omitempty"`
	Payment        *paymentJSON             `json:"payment,omitempty"`
	Installments   *int                     `json:"installments,omitempty"`
	Plan           *installmentPlanJSON     `json:"installment_plan,omitempty"`
	Notifications  []linkedNotificationJSON `json:"notifications"`
	OnStatement    bool                     `json:"on_statement"`
	Refunds        []refundJSON             `json:"refunds"`
	Refunded       moneyJSON                `json:"refunded"`
	OpenQuestions  int                      `json:"open_questions"`
	LastChange     *changeJSON              `json:"last_change,omitempty"`
}

func (a *api) handleExpenseDetail(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	detail, err := a.ledger.ExpenseDetail(r.Context(), principal.AccountID, id)
	if err != nil {
		a.writeLedgerError(w, r, err)
		return
	}
	loc, err := a.accounts.Location(r.Context(), principal.AccountID)
	if err != nil {
		a.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, toExpenseDetailJSON(detail, loc))
}

func toExpenseDetailJSON(detail ledger.ExpenseDetail, loc *time.Location) expenseDetailJSON {
	out := expenseDetailJSON{
		ID: detail.ID, Description: detail.Description, Merchant: detail.Merchant, Payee: detail.Payee,
		Time: localTime(detail.Occurred.At, loc), DatePrecision: string(detail.Occurred.Precision),
		Amount: toMoneyJSON(detail.Amount), AmountBasis: detail.AmountBasis, Nature: detail.Nature, Status: detail.Status,
		InTotal: detail.InTotal, MergedIntoID: detail.MergedIntoID, Classification: detail.Category.Status, Purpose: detail.Purpose,
		Notifications: make([]linkedNotificationJSON, len(detail.Notifications)), OnStatement: detail.OnStatement,
		Refunds: make([]refundJSON, len(detail.Refunds)), Refunded: toMoneyJSON(detail.Refunded), OpenQuestions: len(detail.Clarifications),
	}
	if detail.Occurred.On != nil {
		on := detail.Occurred.On.String()
		out.Date = &on
	}
	if detail.OriginalAmount != nil {
		original := toMoneyJSON(*detail.OriginalAmount)
		out.OriginalAmount = &original
	}
	if detail.Category.Status != ledger.AllocationMissing {
		out.Category = &categoryJSON{Code: detail.Category.Code, Label: detail.Category.Label, Status: detail.Category.Status, Origin: detail.Category.Origin}
	}
	if method := detail.PaymentMethod; method != nil {
		out.Payment = &paymentJSON{Kind: method.Kind, Label: ledger.PaymentLabel(method.Kind), CardLast4: method.CardLast4}
	}
	if detail.Installments != nil {
		out.Installments = &detail.Installments.Count
	}
	if plan := detail.Plan; plan != nil {
		out.Plan = toPlanJSON(*plan)
	}
	for i, notification := range detail.Notifications {
		out.Notifications[i] = linkedNotificationJSON{
			NotificationID: notification.NotificationID, Version: notification.Version, Event: notification.Event,
			PostedAt: notification.PostedAt.UTC(), ReceivedAt: notification.ReceivedAt.UTC(),
		}
	}
	for i, refund := range detail.Refunds {
		out.Refunds[i] = refundJSON{ID: refund.ID, Amount: toMoneyJSON(refund.Amount), Status: refund.Status}
		if refund.Occurred.On != nil {
			on := refund.Occurred.On.String()
			out.Refunds[i].Date = &on
		}
	}
	if change := detail.LastChange; change != nil {
		out.LastChange = &changeJSON{At: change.CreatedAt.UTC(), Author: change.Author}
	}
	return out
}

func toPlanJSON(plan ledger.InstallmentPlan) *installmentPlanJSON {
	out := &installmentPlanJSON{
		Modality: plan.Modality, Count: plan.Count,
		RemainingKnown: toMoneyJSON(plan.RemainingKnown), RemainingEstimated: toMoneyJSON(plan.RemainingEstimated),
	}
	if plan.InstallmentAmount != nil {
		amount := toMoneyJSON(*plan.InstallmentAmount)
		out.InstallmentAmount = &amount
	}
	if plan.Total != nil {
		total := toMoneyJSON(*plan.Total)
		out.Total = &total
	}
	for _, installment := range plan.Installments {
		switch installment.State {
		case ledger.InstallmentCharged:
			out.Charged++
		case ledger.InstallmentKnownFuture:
			out.KnownFuture++
		default:
			out.Estimated++
		}
	}
	return out
}

func (a *api) writeLedgerError(w http.ResponseWriter, r *http.Request, err error) {
	var validation *ledger.ValidationError
	switch {
	case errors.As(err, &validation):
		writeErrorMessage(w, http.StatusBadRequest, "invalid_request", validation.Error())
	case errors.Is(err, ledger.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	default:
		a.writeInternalError(w, r, err)
	}
}
