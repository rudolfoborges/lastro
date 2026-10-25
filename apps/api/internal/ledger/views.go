package ledger

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/taxonomy"
)

const (
	maxViewSpan     = 400
	staleProjection = 40
	modalityUpfront = "upfront"
)

type View string

const (
	ViewPurchases   View = "purchases"
	ViewStatement   View = "statement"
	ViewCommitments View = "commitments"
	ViewPayments    View = "payments"
)

type GroupBy string

const (
	GroupNone          GroupBy = ""
	GroupCategory      GroupBy = "category"
	GroupMerchant      GroupBy = "merchant"
	GroupDay           GroupBy = "day"
	GroupWeek          GroupBy = "week"
	GroupMonth         GroupBy = "month"
	GroupPaymentMethod GroupBy = "payment_method"
	GroupModality      GroupBy = "modality"
)

var groupings = []GroupBy{GroupNone, GroupCategory, GroupMerchant, GroupDay, GroupWeek, GroupMonth, GroupPaymentMethod, GroupModality}

type ViewFilter struct {
	Category    *string
	Merchant    *string
	Modality    *string
	CardLast4   *string
	StatementID *uuid.UUID
	Statuses    []Status
	Source      *Source
}

type Bucket struct {
	Amount         money.Money
	Count          int
	TransactionIDs []uuid.UUID
}

func (b *Bucket) add(id uuid.UUID, amount money.Money) {
	b.Amount.Minor += amount.Minor
	b.Count++
	b.TransactionIDs = append(b.TransactionIDs, id)
}

type PurchaseTotals struct {
	Currency         string
	Confirmed        Bucket
	Provisional      Bucket
	Pending          Bucket
	Refunds          Bucket
	RefundsConfirmed money.Money
}

// NetConfirmed is what confirmed purchases cost after confirmed refunds of the
// period; provisional and pending values are never part of a definitive total.
func (p PurchaseTotals) NetConfirmed() money.Money {
	return money.Money{Minor: p.Confirmed.Amount.Minor - p.RefundsConfirmed.Minor, Currency: p.Currency}
}

type PurchaseGroup struct {
	Key    string
	Label  string
	Totals []PurchaseTotals
}

type UnknownTotal struct {
	TransactionID     uuid.UUID
	Description       string
	OccurredOn        calendar.Date
	InstallmentAmount money.Money
	InstallmentCount  int
	Status            Status
}

type PurchasesView struct {
	From         calendar.Date
	To           calendar.Date
	GroupBy      GroupBy
	Totals       []PurchaseTotals
	Groups       []PurchaseGroup
	UnknownTotal []UnknownTotal
	Undated      int
}

type purchaseRow struct {
	tenantdb.SpendingRowsRow
	amount money.Money
	on     calendar.Date
}

func (s *Service) Purchases(ctx context.Context, accountID uuid.UUID, from, to calendar.Date, filter ViewFilter, groupBy GroupBy) (PurchasesView, error) {
	if err := validatePeriod(from, to); err != nil {
		return PurchasesView{}, err
	}
	if !slices.Contains(groupings, groupBy) {
		return PurchasesView{}, &ValidationError{Field: "group_by", Message: fmt.Sprintf("must be one of %v", groupings[1:])}
	}
	if err := validateFilter(filter); err != nil {
		return PurchasesView{}, err
	}
	view := PurchasesView{From: from, To: to, GroupBy: groupBy}
	err := s.inTx(ctx, accountID, func(t *tx) error {
		rows, err := t.q.SpendingRows(t.ctx, tenantdb.SpendingRowsParams{AccountID: t.accountID, FromDay: from.Time(), ToDay: to.Time(), StatementID: filter.StatementID})
		if err != nil {
			return fmt.Errorf("spending rows: %w", err)
		}
		undated, err := t.q.CountUndatedPurchases(t.ctx, t.accountID)
		if err != nil {
			return fmt.Errorf("count undated purchases: %w", err)
		}
		view.Undated = int(undated)
		totals := map[string]*PurchaseTotals{}
		groups := map[string]*PurchaseGroup{}
		groupTotals := map[string]map[string]*PurchaseTotals{}
		for _, raw := range rows {
			row := purchaseRow{SpendingRowsRow: raw, amount: money.Money{Minor: raw.AmountMinor, Currency: raw.Currency}, on: calendar.DateFromTime(*raw.OccurredOn)}
			if !filter.matches(row) {
				continue
			}
			if Nature(row.Nature) == NatureExpense && AmountBasis(row.AmountBasis) == AmountInstallment {
				view.UnknownTotal = append(view.UnknownTotal, unknownTotal(row))
				continue
			}
			addPurchase(totalsFor(totals, row.Currency), row)
			if groupBy == GroupNone {
				continue
			}
			key, label := groupKey(row, groupBy)
			if _, ok := groups[key]; !ok {
				groups[key] = &PurchaseGroup{Key: key, Label: label}
				groupTotals[key] = map[string]*PurchaseTotals{}
			}
			addPurchase(totalsFor(groupTotals[key], row.Currency), row)
		}
		view.Totals = sortedTotals(totals)
		for key, group := range groups {
			group.Totals = sortedTotals(groupTotals[key])
			view.Groups = append(view.Groups, *group)
		}
		sort.Slice(view.Groups, func(i, j int) bool { return view.Groups[i].Key < view.Groups[j].Key })
		return nil
	})
	return view, err
}

func validatePeriod(from, to calendar.Date) error {
	switch {
	case to.Before(from):
		return &ValidationError{Field: "to", Message: "must not be before from"}
	case from.AddDays(maxViewSpan).Before(to):
		return &ValidationError{Field: "to", Message: fmt.Sprintf("the period can have at most %d days", maxViewSpan)}
	}
	return nil
}

func validateFilter(filter ViewFilter) error {
	for _, status := range filter.Statuses {
		if !slices.Contains(activeStatuses, status) {
			return &ValidationError{Field: "status", Message: "must be provisional, confirmed or pending_clarification"}
		}
	}
	if filter.Modality != nil && !slices.Contains([]string{string(ModalityCardInstallment), string(ModalityPixInstallment), modalityUpfront}, *filter.Modality) {
		return &ValidationError{Field: "modality", Message: "must be card_installment, pix_installment or upfront"}
	}
	if filter.Source != nil && !slices.Contains([]Source{SourceConversation, SourceWalletNotification, SourceStatementLine, SourceManual, SourceImport}, *filter.Source) {
		return &ValidationError{Field: "source", Message: "must be conversation, wallet_notification, statement_line, manual or import"}
	}
	return nil
}

func (f ViewFilter) matches(row purchaseRow) bool {
	switch {
	case len(f.Statuses) > 0 && !slices.Contains(f.Statuses, Status(row.Status)):
		return false
	case f.Category != nil && categoryCodeOf(row.CategoryCode) != *f.Category:
		return false
	case f.Merchant != nil && !strings.Contains(normalizedName(merchantOf(row)), normalizedName(*f.Merchant)):
		return false
	case f.Modality != nil && modalityKey(row.PaymentModality) != *f.Modality:
		return false
	case f.CardLast4 != nil && (row.CardLast4 == nil || *row.CardLast4 != *f.CardLast4):
		return false
	case f.Source != nil && !slices.Contains(row.Sources, string(*f.Source)):
		return false
	}
	return true
}

func categoryCodeOf(code *string) string {
	if code == nil {
		return taxonomy.Unidentified
	}
	return *code
}

func merchantOf(row purchaseRow) string {
	if row.Merchant != nil {
		return *row.Merchant
	}
	return row.Description
}

func modalityKey(modality *string) string {
	if modality == nil {
		return modalityUpfront
	}
	return *modality
}

func totalsFor(totals map[string]*PurchaseTotals, currency string) *PurchaseTotals {
	if _, ok := totals[currency]; !ok {
		zero := money.Money{Currency: currency}
		totals[currency] = &PurchaseTotals{
			Currency: currency, RefundsConfirmed: zero,
			Confirmed: Bucket{Amount: zero}, Provisional: Bucket{Amount: zero}, Pending: Bucket{Amount: zero}, Refunds: Bucket{Amount: zero},
		}
	}
	return totals[currency]
}

func addPurchase(totals *PurchaseTotals, row purchaseRow) {
	if Nature(row.Nature) == NatureRefund {
		totals.Refunds.add(row.ID, row.amount)
		if Status(row.Status) == StatusConfirmed {
			totals.RefundsConfirmed.Minor += row.amount.Minor
		}
		return
	}
	switch Status(row.Status) {
	case StatusConfirmed:
		totals.Confirmed.add(row.ID, row.amount)
	case StatusProvisional:
		totals.Provisional.add(row.ID, row.amount)
	default:
		totals.Pending.add(row.ID, row.amount)
	}
}

func sortedTotals(totals map[string]*PurchaseTotals) []PurchaseTotals {
	out := make([]PurchaseTotals, 0, len(totals))
	for _, total := range totals {
		out = append(out, *total)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Currency < out[j].Currency })
	return out
}

func unknownTotal(row purchaseRow) UnknownTotal {
	out := UnknownTotal{TransactionID: row.ID, Description: row.Description, OccurredOn: row.on, InstallmentAmount: row.amount, Status: Status(row.Status)}
	if row.InstallmentCount != nil {
		out.InstallmentCount = int(*row.InstallmentCount)
	}
	return out
}

func groupKey(row purchaseRow, groupBy GroupBy) (string, string) {
	switch groupBy {
	case GroupCategory:
		code := categoryCodeOf(row.CategoryCode)
		label := "Não identificado"
		if row.CategoryLabel != nil {
			label = *row.CategoryLabel
		}
		return code, label
	case GroupMerchant:
		return normalizedName(merchantOf(row)), merchantOf(row)
	case GroupDay:
		return row.on.String(), row.on.Format()
	case GroupWeek:
		start := row.on.AddDays(-((int(row.on.Time().Weekday()) + 6) % 7))
		return start.String(), "semana de " + start.Format()
	case GroupMonth:
		month := row.on.Time().Format("2006-01")
		return month, row.on.Time().Format("01/2006")
	case GroupPaymentMethod:
		if row.PaymentKind == nil {
			return "unknown", "meio de pagamento desconhecido"
		}
		label := paymentLabel(PaymentKind(*row.PaymentKind))
		if row.CardLast4 != nil {
			label += " final " + *row.CardLast4
			return *row.PaymentKind + ":" + *row.CardLast4, label
		}
		return *row.PaymentKind, label
	default:
		modality := modalityKey(row.PaymentModality)
		return modality, modalityLabel(modality)
	}
}

func modalityLabel(modality string) string {
	switch modality {
	case string(ModalityCardInstallment):
		return "parcelado no cartão"
	case string(ModalityPixInstallment):
		return "Pix parcelado"
	default:
		return "à vista"
	}
}

type StatementCategory struct {
	Code           string
	Label          string
	Amount         money.Money
	Lines          int
	TransactionIDs []uuid.UUID
}

type ModalitySubtotal struct {
	Modality        PaymentModality
	Amount          money.Money
	IncludedCharges money.Money
	Lines           int
}

type StatementChargesView struct {
	Review     StatementReview
	Categories []StatementCategory
	Modalities []ModalitySubtotal
}

func (s *Service) StatementCharges(ctx context.Context, accountID uuid.UUID, ref StatementRef) (StatementChargesView, error) {
	var view StatementChargesView
	err := s.inTx(ctx, accountID, func(t *tx) error {
		statement, err := t.findStatement(ref)
		if err != nil {
			return err
		}
		version, err := t.q.LatestStatementVersion(t.ctx, tenantdb.LatestStatementVersionParams{AccountID: t.accountID, StatementID: statement.ID})
		if err != nil {
			return fmt.Errorf("latest statement version: %w", err)
		}
		if view.Review, err = t.review(statement, version); err != nil {
			return err
		}
		rows, err := t.q.StatementLineRows(t.ctx, tenantdb.StatementLineRowsParams{AccountID: t.accountID, StatementVersionID: version.ID})
		if err != nil {
			return fmt.Errorf("statement line rows: %w", err)
		}
		view.Categories, view.Modalities = chargedBreakdown(rows, version.Currency)
		return nil
	})
	return view, err
}

// chargedBreakdown splits what the cycle charges by category, and installments
// by modality as a separate reading of the same lines, never added again.
func chargedBreakdown(rows []tenantdb.StatementLineRowsRow, currency string) ([]StatementCategory, []ModalitySubtotal) {
	categories := map[string]*StatementCategory{}
	modalities := map[PaymentModality]*ModalitySubtotal{}
	for _, row := range rows {
		kind := StatementItemKind(row.Kind)
		if Direction(row.Direction) != DirectionDebit || kind == ItemUnknown || kind == ItemAdjustment {
			continue
		}
		code, label := taxonomy.Unidentified, "Não identificado"
		if row.CategoryCode != nil {
			code, label = *row.CategoryCode, *row.CategoryLabel
		}
		category, ok := categories[code]
		if !ok {
			category = &StatementCategory{Code: code, Label: label, Amount: money.Money{Currency: currency}}
			categories[code] = category
		}
		category.Amount.Minor += row.AmountMinor
		category.Lines++
		if row.TransactionID != nil && !slices.Contains(category.TransactionIDs, *row.TransactionID) {
			category.TransactionIDs = append(category.TransactionIDs, *row.TransactionID)
		}
		if !kind.installment() {
			continue
		}
		modality := ModalityCardInstallment
		if kind == ItemPixInstallment {
			modality = ModalityPixInstallment
		}
		subtotal, ok := modalities[modality]
		if !ok {
			subtotal = &ModalitySubtotal{Modality: modality, Amount: money.Money{Currency: currency}, IncludedCharges: money.Money{Currency: currency}}
			modalities[modality] = subtotal
		}
		subtotal.Amount.Minor += row.AmountMinor
		subtotal.IncludedCharges.Minor += row.IncludedChargesMinor
		subtotal.Lines++
	}
	outCategories := make([]StatementCategory, 0, len(categories))
	for _, category := range categories {
		outCategories = append(outCategories, *category)
	}
	sort.Slice(outCategories, func(i, j int) bool { return outCategories[i].Amount.Minor > outCategories[j].Amount.Minor })
	outModalities := make([]ModalitySubtotal, 0, len(modalities))
	for _, subtotal := range modalities {
		outModalities = append(outModalities, *subtotal)
	}
	sort.Slice(outModalities, func(i, j int) bool { return outModalities[i].Modality < outModalities[j].Modality })
	return outCategories, outModalities
}

type CommitmentMonth struct {
	Month          string
	Currency       string
	Known          money.Money
	Estimated      money.Money
	Installments   int
	MonthEstimated bool
}

type PlanCommitment struct {
	TransactionID      uuid.UUID
	Description        string
	Modality           PaymentModality
	Count              int
	Remaining          money.Money
	RemainingKnown     money.Money
	RemainingEstimated money.Money
	InformedOn         calendar.Date
	Stale              bool
}

type CommitmentTotals struct {
	Currency  string
	Known     money.Money
	Estimated money.Money
}

func (c CommitmentTotals) Total() money.Money {
	return money.Money{Minor: c.Known.Minor + c.Estimated.Minor, Currency: c.Currency}
}

type CommitmentsView struct {
	AsOf              calendar.Date
	Totals            []CommitmentTotals
	Months            []CommitmentMonth
	Plans             []PlanCommitment
	OldestInformation *calendar.Date
}

func (s *Service) FutureCommitments(ctx context.Context, accountID uuid.UUID, filter ViewFilter, now time.Time) (CommitmentsView, error) {
	if err := validateFilter(filter); err != nil {
		return CommitmentsView{}, err
	}
	var view CommitmentsView
	err := s.inTx(ctx, accountID, func(t *tx) error {
		loc, err := t.location()
		if err != nil {
			return err
		}
		view.AsOf = calendar.DateOf(now, loc)
		ids, err := t.q.ActivePlanTransactions(t.ctx, t.accountID)
		if err != nil {
			return fmt.Errorf("active plans: %w", err)
		}
		totals := map[string]*CommitmentTotals{}
		months := map[string]*CommitmentMonth{}
		for _, id := range ids {
			transaction, err := t.transaction(id)
			if err != nil {
				return err
			}
			plan, err := t.installmentPlan(id)
			if err != nil || plan == nil {
				return err
			}
			if !filter.matchesPlan(transaction, *plan) || plan.Remaining().IsZero() {
				continue
			}
			commitment := PlanCommitment{
				TransactionID: id, Description: transaction.Description, Modality: plan.Modality, Count: plan.Count,
				Remaining: plan.Remaining(), RemainingKnown: plan.RemainingKnown, RemainingEstimated: plan.RemainingEstimated,
				InformedOn: plan.InformedOn, Stale: plan.InformedOn.AddDays(staleProjection).Before(view.AsOf),
			}
			view.Plans = append(view.Plans, commitment)
			if view.OldestInformation == nil || plan.InformedOn.Before(*view.OldestInformation) {
				informed := plan.InformedOn
				view.OldestInformation = &informed
			}
			total, ok := totals[plan.RemainingKnown.Currency]
			if !ok {
				currency := plan.RemainingKnown.Currency
				total = &CommitmentTotals{Currency: currency, Known: money.Money{Currency: currency}, Estimated: money.Money{Currency: currency}}
				totals[currency] = total
			}
			total.Known.Minor += plan.RemainingKnown.Minor
			total.Estimated.Minor += plan.RemainingEstimated.Minor
			addMonths(months, transaction, *plan)
		}
		for _, total := range totals {
			view.Totals = append(view.Totals, *total)
		}
		sort.Slice(view.Totals, func(i, j int) bool { return view.Totals[i].Currency < view.Totals[j].Currency })
		for _, month := range months {
			view.Months = append(view.Months, *month)
		}
		sort.Slice(view.Months, func(i, j int) bool {
			if view.Months[i].Month != view.Months[j].Month {
				return view.Months[i].Month < view.Months[j].Month
			}
			return view.Months[i].Currency < view.Months[j].Currency
		})
		return nil
	})
	return view, err
}

func (f ViewFilter) matchesPlan(transaction Transaction, plan InstallmentPlan) bool {
	switch {
	case f.Modality != nil && string(plan.Modality) != *f.Modality:
		return false
	case f.CardLast4 != nil && (plan.CardLast4 == nil || *plan.CardLast4 != *f.CardLast4):
		return false
	case f.Merchant != nil && !strings.Contains(normalizedName(merchantOrDescription(transaction)), normalizedName(*f.Merchant)):
		return false
	}
	return true
}

func merchantOrDescription(transaction Transaction) string {
	if transaction.Merchant != nil {
		return *transaction.Merchant
	}
	return transaction.Description
}

// addMonths places each future installment in the month it is expected: one
// month after the last charged one, or after the purchase when none was
// charged yet, which is then only an estimate of the month.
func addMonths(months map[string]*CommitmentMonth, transaction Transaction, plan InstallmentPlan) {
	var anchor *calendar.Date
	last := 0
	for _, installment := range plan.Installments {
		if installment.State == InstallmentCharged && installment.ClosingDate != nil {
			anchor, last = installment.ClosingDate, installment.Number
		}
	}
	monthEstimated := anchor == nil
	if anchor == nil {
		base := plan.InformedOn
		if transaction.Occurred.On != nil {
			base = *transaction.Occurred.On
		}
		anchor = &base
	}
	for _, installment := range plan.Installments {
		if installment.State == InstallmentCharged {
			continue
		}
		month := anchor.Time().AddDate(0, installment.Number-last, 0).Format("2006-01")
		key := month + ":" + installment.Amount.Currency
		entry, ok := months[key]
		if !ok {
			entry = &CommitmentMonth{Month: month, Currency: installment.Amount.Currency, Known: money.Money{Currency: installment.Amount.Currency}, Estimated: money.Money{Currency: installment.Amount.Currency}}
			months[key] = entry
		}
		if installment.State == InstallmentKnownFuture {
			entry.Known.Minor += installment.Amount.Minor
		} else {
			entry.Estimated.Minor += installment.Amount.Minor
		}
		entry.Installments++
		entry.MonthEstimated = entry.MonthEstimated || monthEstimated
	}
}

type StatementPayments struct {
	Statement       Statement
	PreviousBalance *money.Money
	Payments        money.Money
	Credits         money.Money
	PaidAmount      money.Money
	PaidIn          *Statement
	Paid            bool
}

type PaymentsView struct {
	Statements []StatementPayments
}

// Payments explains each statement with its previous balance, the payments
// and credits it shows, and whether a later statement shows it paid; payments
// and balances are never new spending.
func (s *Service) Payments(ctx context.Context, accountID uuid.UUID, cardLast4 *string, fromMonth, toMonth string) (PaymentsView, error) {
	var view PaymentsView
	err := s.inTx(ctx, accountID, func(t *tx) error {
		basis, err := t.monthBasis()
		if err != nil {
			return err
		}
		rows, err := t.q.ListStatementsWithLatestVersion(t.ctx, t.accountID)
		if err != nil {
			return fmt.Errorf("list statements: %w", err)
		}
		all := make([]StatementPayments, 0, len(rows))
		for _, row := range rows {
			statement := tenantdb.Statement{ID: row.ID, AccountID: row.AccountID, Issuer: row.Issuer, IssuerKey: row.IssuerKey, CardLast4: row.CardLast4, ClosingDate: row.ClosingDate, PaymentMethodID: row.PaymentMethodID, CreatedAt: row.CreatedAt}
			version, err := t.q.GetStatementVersion(t.ctx, tenantdb.GetStatementVersionParams{AccountID: t.accountID, ID: row.VersionID})
			if err != nil {
				return fmt.Errorf("statement version: %w", err)
			}
			composition, err := t.composition(version)
			if err != nil {
				return err
			}
			all = append(all, StatementPayments{
				Statement:       t.statementOf(statement, version, basis),
				PreviousBalance: composition.PreviousBalance,
				Payments:        composition.Payments.Amount,
				Credits:         composition.Credits.Amount,
				PaidAmount:      money.Money{Currency: version.Currency},
			})
		}
		for i := range all {
			if next := nextOfCard(all, i); next != nil {
				all[i].PaidAmount = next.Payments
				all[i].PaidIn = &next.Statement
				all[i].Paid = next.Payments.Minor >= all[i].Statement.Version.Total.Minor
			}
			if (cardLast4 == nil || all[i].Statement.CardLast4 == *cardLast4) && inMonths(all[i].Statement.Month, fromMonth, toMonth) {
				view.Statements = append(view.Statements, all[i])
			}
		}
		return nil
	})
	return view, err
}

func nextOfCard(all []StatementPayments, index int) *StatementPayments {
	for j := index + 1; j < len(all); j++ {
		if all[j].Statement.CardLast4 == all[index].Statement.CardLast4 {
			return &all[j]
		}
	}
	return nil
}

func inMonths(month, from, to string) bool {
	return (from == "" || month >= from) && (to == "" || month <= to)
}
