package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

const coverageChunkDays = 92

type QuerySpendingInput struct {
	From        string `json:"from,omitempty" jsonschema:"first purchase date, YYYY-MM-DD; the first day of the current month when omitted"`
	To          string `json:"to,omitempty" jsonschema:"last purchase date, YYYY-MM-DD; today when omitted"`
	GroupBy     string `json:"group_by,omitempty" jsonschema:"split the totals by category, merchant, day, week, month, payment_method or modality"`
	Category    string `json:"category,omitempty" jsonschema:"category code; unidentified also includes purchases without category"`
	Merchant    string `json:"merchant,omitempty" jsonschema:"part of the store name, compared without case or accents"`
	Modality    string `json:"modality,omitempty" jsonschema:"card_installment, pix_installment or upfront"`
	CardLast4   string `json:"card_last4,omitempty"`
	StatementID string `json:"statement_id,omitempty" jsonschema:"only purchases charged on this statement"`
	Status      string `json:"status,omitempty" jsonschema:"only confirmed, provisional or pending_clarification"`
	Source      string `json:"source,omitempty" jsonschema:"only purchases with evidence from conversation, wallet_notification or statement_line"`
}

type BucketOutput struct {
	Amount         MoneyOutput `json:"amount"`
	Count          int         `json:"count"`
	TransactionIDs []string    `json:"transaction_ids" jsonschema:"the transactions that compose this amount"`
}

type PurchaseTotalsOutput struct {
	Currency     string       `json:"currency"`
	Confirmed    BucketOutput `json:"confirmed"`
	Provisional  BucketOutput `json:"provisional" jsonschema:"not confirmed by a statement or a debit yet"`
	Pending      BucketOutput `json:"pending" jsonschema:"records with an unresolved overlap between sources; never part of a definitive total"`
	Refunds      BucketOutput `json:"refunds" jsonschema:"refunds dated in the period"`
	NetConfirmed MoneyOutput  `json:"net_confirmed" jsonschema:"confirmed purchases minus confirmed refunds"`
}

type PurchaseGroupOutput struct {
	Key    string                 `json:"key"`
	Label  string                 `json:"label"`
	Totals []PurchaseTotalsOutput `json:"totals"`
}

type UnknownTotalOutput struct {
	TransactionID     string      `json:"transaction_id"`
	Description       string      `json:"description"`
	Date              string      `json:"date"`
	InstallmentAmount MoneyOutput `json:"installment_amount"`
	InstallmentCount  int         `json:"installment_count"`
}

type PeriodOutput struct {
	From string `json:"from"`
	To   string `json:"to"`
}

type CoverageNoteOutput struct {
	Complete    bool     `json:"complete" jsonschema:"false when the automatic tracking had gaps, pauses or delays in the period"`
	Limitations []string `json:"limitations,omitempty" jsonschema:"in Portuguese; relay every one before talking about the totals"`
}

type QuerySpendingOutput struct {
	View          string                 `json:"view" jsonschema:"purchases: by purchase date"`
	Premise       string                 `json:"premise" jsonschema:"what the numbers mean, in Portuguese; say it when the question allowed other readings"`
	Period        PeriodOutput           `json:"period"`
	Totals        []PurchaseTotalsOutput `json:"totals" jsonschema:"one entry per currency; currencies are never added"`
	Groups        []PurchaseGroupOutput  `json:"groups,omitempty"`
	UnknownTotal  []UnknownTotalOutput   `json:"unknown_total,omitempty" jsonschema:"purchases in installments whose total was never informed; left out of the totals"`
	Undated       int                    `json:"undated,omitempty" jsonschema:"records without a date, in no period"`
	OpenQuestions int                    `json:"open_questions" jsonschema:"open questions about the transactions of these totals"`
	Coverage      CoverageNoteOutput     `json:"coverage"`
	Summary       string                 `json:"summary"`
}

type StatementRefInput struct {
	StatementID string `json:"statement_id,omitempty"`
	ClosingDate string `json:"closing_date,omitempty" jsonschema:"YYYY-MM-DD"`
	Month       string `json:"month,omitempty" jsonschema:"YYYY-MM, the month the statement belongs to (by its closing date, as Rudolfo chose)"`
	CardLast4   string `json:"card_last4,omitempty" jsonschema:"holder's card, only when more than one statement matches"`
}

type StatementCategoryOutput struct {
	Code           string      `json:"code"`
	Label          string      `json:"label"`
	Amount         MoneyOutput `json:"amount"`
	Lines          int         `json:"lines"`
	TransactionIDs []string    `json:"transaction_ids"`
}

type ModalitySubtotalOutput struct {
	Modality        string      `json:"modality"`
	Label           string      `json:"label"`
	Amount          MoneyOutput `json:"amount" jsonschema:"installments of this modality charged in the cycle; already inside the categories, never added again"`
	IncludedCharges MoneyOutput `json:"included_charges" jsonschema:"interest, IOF and fees inside those installments, as the statement detailed them"`
	Lines           int         `json:"lines"`
}

type StatementPaymentOutput struct {
	PreviousBalance *MoneyOutput `json:"previous_balance,omitempty"`
	Payments        MoneyOutput  `json:"payments" jsonschema:"payments shown on this statement (of the previous one)"`
	Paid            bool         `json:"paid" jsonschema:"true only when a later statement of the same card shows a payment of at least this total"`
	PaidAmount      *MoneyOutput `json:"paid_amount,omitempty"`
	PaidIn          *string      `json:"paid_in,omitempty" jsonschema:"closing date of the statement that shows the payment"`
}

type StatementSummaryOutput struct {
	Statement  StatementOutput           `json:"statement"`
	Review     StatementReviewOutput     `json:"review"`
	Categories []StatementCategoryOutput `json:"categories" jsonschema:"what the cycle charges, by category"`
	Modalities []ModalitySubtotalOutput  `json:"modalities,omitempty"`
	Payment    StatementPaymentOutput    `json:"payment"`
	Summary    string                    `json:"summary"`
}

type FutureCommitmentsInput struct {
	Modality  string `json:"modality,omitempty" jsonschema:"card_installment or pix_installment"`
	CardLast4 string `json:"card_last4,omitempty"`
	Merchant  string `json:"merchant,omitempty"`
}

type CommitmentTotalsOutput struct {
	Currency  string      `json:"currency"`
	Known     MoneyOutput `json:"known"`
	Estimated MoneyOutput `json:"estimated"`
	Total     MoneyOutput `json:"total"`
}

type CommitmentMonthOutput struct {
	Month          string      `json:"month" jsonschema:"YYYY-MM"`
	Currency       string      `json:"currency"`
	Known          MoneyOutput `json:"known"`
	Estimated      MoneyOutput `json:"estimated"`
	Installments   int         `json:"installments"`
	MonthEstimated bool        `json:"month_estimated,omitempty" jsonschema:"true when no installment of a plan was charged yet, so the month itself is an estimate"`
}

type PlanCommitmentOutput struct {
	TransactionID      string      `json:"transaction_id"`
	Description        string      `json:"description"`
	Modality           string      `json:"modality"`
	Count              int         `json:"count"`
	Remaining          MoneyOutput `json:"remaining"`
	RemainingKnown     MoneyOutput `json:"remaining_known"`
	RemainingEstimated MoneyOutput `json:"remaining_estimated"`
	InformedOn         string      `json:"informed_on"`
	Stale              bool        `json:"stale,omitempty" jsonschema:"the information is more than 40 days old"`
}

type FutureCommitmentsOutput struct {
	View              string                   `json:"view" jsonschema:"commitments: installments still to come"`
	AsOf              string                   `json:"as_of"`
	Totals            []CommitmentTotalsOutput `json:"totals"`
	Months            []CommitmentMonthOutput  `json:"months"`
	Plans             []PlanCommitmentOutput   `json:"plans"`
	OldestInformation *string                  `json:"oldest_information,omitempty" jsonschema:"date of the oldest information behind the projection"`
	Summary           string                   `json:"summary"`
}

type ComponentTotalOutput struct {
	Kind   string      `json:"kind"`
	Amount MoneyOutput `json:"amount"`
}

type PixPlanOutput struct {
	Transaction        TransactionOutput      `json:"transaction"`
	Count              int                    `json:"count"`
	Charged            MoneyOutput            `json:"charged"`
	Breakdown          []ComponentTotalOutput `json:"breakdown,omitempty"`
	WithoutBreakdown   int                    `json:"without_breakdown,omitempty"`
	RemainingKnown     MoneyOutput            `json:"remaining_known"`
	RemainingEstimated MoneyOutput            `json:"remaining_estimated"`
}

type PixTotalsOutput struct {
	Currency           string                 `json:"currency"`
	Charged            MoneyOutput            `json:"charged" jsonschema:"what statements charged so far for Pix installments"`
	Breakdown          []ComponentTotalOutput `json:"breakdown" jsonschema:"principal, interest, IOF and fees inside the charged installments, as detailed by the statements"`
	WithoutBreakdown   int                    `json:"without_breakdown" jsonschema:"charged installments whose statement did not detail the composition"`
	RemainingKnown     MoneyOutput            `json:"remaining_known"`
	RemainingEstimated MoneyOutput            `json:"remaining_estimated"`
}

type PixInstallmentSummaryOutput struct {
	View    string            `json:"view" jsonschema:"pix_installments: by plan, as charged by statements"`
	Plans   []PixPlanOutput   `json:"plans"`
	Totals  []PixTotalsOutput `json:"totals"`
	Summary string            `json:"summary"`
}

type NoInput struct{}

func (s *Server) registerQueries() {
	addTool(s, toolSpec{
		name:  "query_spending",
		title: "Answer how much was spent in a period",
		description: "Purchases by purchase date, in Rudolfo's time zone: confirmed, provisional and pending apart, refunds of the period and the confirmed net, one total per currency, the transaction IDs behind each amount, open questions and the coverage limitations of the period. " +
			"Filters by category, merchant, modality, card, statement, status and source; group_by splits the totals. Statement charges, future installments and Pix installments have their own tools; never add their values to these.",
		scope:      accounts.ScopeExpensesRead,
		readOnly:   true,
		idempotent: true,
		enums: map[string][]any{
			"group_by": {"category", "merchant", "day", "week", "month", "payment_method", "modality"},
			"modality": {"card_installment", "pix_installment", "upfront"},
			"status":   {"confirmed", "provisional", "pending_clarification"},
			"source":   {"conversation", "wallet_notification", "statement_line"},
		},
	}, s.querySpending)

	addTool(s, toolSpec{
		name:        "get_statement_summary",
		title:       "Answer how much a statement charges",
		description: "What a credit card statement charges: the printed total, the composition, the charges by category, the installments by modality with the interest inside them, the previous balance, the payment evidence and the review status. The latest statement by default, or by id, closing date or month.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
	}, s.statementSummary)

	addTool(s, toolSpec{
		name:        "get_future_commitments",
		title:       "Answer how much is still to come in installments",
		description: "Installments still to come from every installment plan, known and estimated apart, by expected month, with the date of the information behind them. Never add them to spending: they are future charges.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
		enums:       map[string][]any{"modality": {"card_installment", "pix_installment"}},
	}, s.futureCommitments)

	addTool(s, toolSpec{
		name:        "get_pix_installment_summary",
		title:       "Answer how much goes to Pix installments and how much is interest",
		description: "Pix installment plans: what statements charged, the principal, interest, IOF and fees they detailed inside those installments, what came without detail and what is still to come.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
	}, s.pixSummary)
}

func (s *Server) querySpending(ctx context.Context, c call, in QuerySpendingInput) (QuerySpendingOutput, error) {
	loc, err := s.deps.Accounts.Location(ctx, c.principal.AccountID)
	if err != nil {
		return QuerySpendingOutput{}, err
	}
	today := calendar.DateOf(c.receivedAt, loc)
	from := calendar.Date{Year: today.Year, Month: today.Month, Day: 1}
	to := today
	if given, err := parseOptionalDate("from", in.From); err != nil {
		return QuerySpendingOutput{}, err
	} else if given != nil {
		from = *given
	}
	if given, err := parseOptionalDate("to", in.To); err != nil {
		return QuerySpendingOutput{}, err
	} else if given != nil {
		to = *given
	}
	filter, err := viewFilter(in)
	if err != nil {
		return QuerySpendingOutput{}, err
	}
	view, err := s.deps.Ledger.Purchases(ctx, c.principal.AccountID, from, to, filter, ledger.GroupBy(in.GroupBy))
	if err != nil {
		return QuerySpendingOutput{}, err
	}
	out := QuerySpendingOutput{
		View:    string(ledger.ViewPurchases),
		Premise: purchasesPremise(in),
		Period:  PeriodOutput{From: from.String(), To: to.String()},
		Totals:  []PurchaseTotalsOutput{},
		Undated: view.Undated,
	}
	var ids []uuid.UUID
	for _, total := range view.Totals {
		out.Totals = append(out.Totals, purchaseTotalsOutput(total))
		for _, bucket := range []ledger.Bucket{total.Confirmed, total.Provisional, total.Pending, total.Refunds} {
			ids = append(ids, bucket.TransactionIDs...)
		}
	}
	for _, group := range view.Groups {
		entry := PurchaseGroupOutput{Key: group.Key, Label: group.Label}
		for _, total := range group.Totals {
			entry.Totals = append(entry.Totals, purchaseTotalsOutput(total))
		}
		out.Groups = append(out.Groups, entry)
	}
	for _, unknown := range view.UnknownTotal {
		out.UnknownTotal = append(out.UnknownTotal, UnknownTotalOutput{
			TransactionID: unknown.TransactionID.String(), Description: unknown.Description, Date: unknown.OccurredOn.String(),
			InstallmentAmount: moneyOutput(unknown.InstallmentAmount), InstallmentCount: unknown.InstallmentCount,
		})
		ids = append(ids, unknown.TransactionID)
	}
	if out.OpenQuestions, err = s.deps.Ledger.OpenQuestionsOf(ctx, c.principal.AccountID, ids); err != nil {
		return QuerySpendingOutput{}, err
	}
	if out.Coverage, err = s.periodCoverage(ctx, c, from, to, today); err != nil {
		return QuerySpendingOutput{}, err
	}
	out.Summary = purchasesSummary(out, from, to)
	return out, nil
}

func viewFilter(in QuerySpendingInput) (ledger.ViewFilter, error) {
	filter := ledger.ViewFilter{
		Category:  optionalString(in.Category),
		Merchant:  optionalString(in.Merchant),
		Modality:  optionalString(in.Modality),
		CardLast4: optionalString(in.CardLast4),
	}
	var err error
	if filter.StatementID, err = optionalID("statement_id", in.StatementID); err != nil {
		return filter, err
	}
	if status := strings.TrimSpace(in.Status); status != "" {
		filter.Statuses = []ledger.Status{ledger.Status(status)}
	}
	if source := strings.TrimSpace(in.Source); source != "" {
		value := ledger.Source(source)
		filter.Source = &value
	}
	return filter, nil
}

func purchasesPremise(in QuerySpendingInput) string {
	parts := []string{"Compras pela data da compra, no fuso de Rudolfo, sem as parcelas cobradas nas faturas nem os encargos"}
	if in.Category != "" {
		parts = append(parts, "só da categoria "+in.Category)
	}
	if in.Merchant != "" {
		parts = append(parts, "só de \""+in.Merchant+"\"")
	}
	if in.Modality != "" {
		parts = append(parts, "só "+modalityName(in.Modality))
	}
	return strings.Join(parts, ", ") + ". Confirmados, provisórios e pendentes ficam separados; só o confirmado vale como total."
}

func modalityName(modality string) string {
	switch modality {
	case string(ledger.ModalityCardInstallment):
		return "parcelado no cartão"
	case string(ledger.ModalityPixInstallment):
		return "Pix parcelado"
	default:
		return "à vista"
	}
}

func bucketOutput(bucket ledger.Bucket) BucketOutput {
	out := BucketOutput{Amount: moneyOutput(bucket.Amount), Count: bucket.Count, TransactionIDs: []string{}}
	for _, id := range bucket.TransactionIDs {
		out.TransactionIDs = append(out.TransactionIDs, id.String())
	}
	return out
}

func purchaseTotalsOutput(total ledger.PurchaseTotals) PurchaseTotalsOutput {
	return PurchaseTotalsOutput{
		Currency:     total.Currency,
		Confirmed:    bucketOutput(total.Confirmed),
		Provisional:  bucketOutput(total.Provisional),
		Pending:      bucketOutput(total.Pending),
		Refunds:      bucketOutput(total.Refunds),
		NetConfirmed: moneyOutput(total.NetConfirmed()),
	}
}

// periodCoverage checks the automatic tracking of the period in chunks the
// coverage report accepts, and only up to today: the future has no coverage.
func (s *Server) periodCoverage(ctx context.Context, c call, from, to, today calendar.Date) (CoverageNoteOutput, error) {
	note := CoverageNoteOutput{Complete: true}
	last := to
	if today.Before(last) {
		last = today
	}
	for start := from; !last.Before(start); start = start.AddDays(coverageChunkDays) {
		end := start.AddDays(coverageChunkDays - 1)
		if last.Before(end) {
			end = last
		}
		coverage, err := s.deps.Ledger.CoverageForDays(ctx, c.principal.AccountID, &start, &end, c.receivedAt)
		var validation *ledger.ValidationError
		if errors.As(err, &validation) {
			continue
		}
		if err != nil {
			return CoverageNoteOutput{}, err
		}
		note.Complete = note.Complete && coverage.Complete
		for _, limitation := range coverage.Limitations {
			if !slices.Contains(note.Limitations, limitation) {
				note.Limitations = append(note.Limitations, limitation)
			}
		}
	}
	return note, nil
}

func purchasesSummary(out QuerySpendingOutput, from, to calendar.Date) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Compras de %s a %s, pela data da compra.", from.Format(), to.Format())
	empty := true
	for _, total := range out.Totals {
		empty = false
		fmt.Fprintf(&b, " %s confirmados (%d)", total.Confirmed.Amount.Formatted, total.Confirmed.Count)
		if total.Provisional.Count > 0 {
			fmt.Fprintf(&b, ", %s provisórios (%d)", total.Provisional.Amount.Formatted, total.Provisional.Count)
		}
		if total.Pending.Count > 0 {
			fmt.Fprintf(&b, ", %s pendentes de conferência (%d), fora do total confirmado", total.Pending.Amount.Formatted, total.Pending.Count)
		}
		b.WriteString(".")
		if total.Refunds.Count > 0 {
			fmt.Fprintf(&b, " Estornos: %s. Líquido confirmado: %s.", total.Refunds.Amount.Formatted, total.NetConfirmed.Formatted)
		}
	}
	if len(out.UnknownTotal) > 0 {
		fmt.Fprintf(&b, " %d compra(s) parcelada(s) sem total informado ficaram de fora: só a parcela é conhecida.", len(out.UnknownTotal))
	}
	if out.Undated > 0 {
		fmt.Fprintf(&b, " %d lançamento(s) sem data não entram em nenhum período.", out.Undated)
	}
	if out.OpenQuestions > 0 {
		fmt.Fprintf(&b, " Há %d dúvida(s) em aberto nesses lançamentos.", out.OpenQuestions)
	}
	switch {
	case empty && !out.Coverage.Complete:
		b.WriteString(" Não há registros no período, mas isso não significa que não houve gastos: " + strings.Join(out.Coverage.Limitations, " "))
	case empty:
		b.WriteString(" Não há compras registradas no período.")
	case !out.Coverage.Complete:
		b.WriteString(" Atenção: " + strings.Join(out.Coverage.Limitations, " "))
	}
	return b.String()
}

func (s *Server) statementSummary(ctx context.Context, c call, in StatementRefInput) (StatementSummaryOutput, error) {
	ref := ledger.StatementRef{CardLast4: optionalString(in.CardLast4), Month: optionalString(in.Month)}
	var err error
	if ref.StatementID, err = optionalID("statement_id", in.StatementID); err != nil {
		return StatementSummaryOutput{}, err
	}
	if ref.ClosingDate, err = parseOptionalDate("closing_date", in.ClosingDate); err != nil {
		return StatementSummaryOutput{}, err
	}
	view, err := s.deps.Ledger.StatementCharges(ctx, c.principal.AccountID, ref)
	if err != nil {
		return StatementSummaryOutput{}, err
	}
	statement := view.Review.Statement
	payments, err := s.deps.Ledger.Payments(ctx, c.principal.AccountID, &statement.CardLast4, statement.Month, statement.Month)
	if err != nil {
		return StatementSummaryOutput{}, err
	}
	out := StatementSummaryOutput{
		Statement:  *statementOutput(statement),
		Review:     reviewOutput(view.Review),
		Categories: []StatementCategoryOutput{},
	}
	for _, category := range view.Categories {
		entry := StatementCategoryOutput{Code: category.Code, Label: category.Label, Amount: moneyOutput(category.Amount), Lines: category.Lines, TransactionIDs: stringsOfIDs(category.TransactionIDs)}
		out.Categories = append(out.Categories, entry)
	}
	for _, subtotal := range view.Modalities {
		out.Modalities = append(out.Modalities, ModalitySubtotalOutput{
			Modality: string(subtotal.Modality), Label: modalityName(string(subtotal.Modality)),
			Amount: moneyOutput(subtotal.Amount), IncludedCharges: moneyOutput(subtotal.IncludedCharges), Lines: subtotal.Lines,
		})
	}
	out.Payment = StatementPaymentOutput{Payments: moneyOutput(money.Money{Currency: statement.Version.Total.Currency})}
	for _, entry := range payments.Statements {
		if entry.Statement.ID != statement.ID {
			continue
		}
		out.Payment.Payments, out.Payment.Paid = moneyOutput(entry.Payments), entry.Paid
		if entry.PreviousBalance != nil {
			previous := moneyOutput(*entry.PreviousBalance)
			out.Payment.PreviousBalance = &previous
		}
		if entry.PaidIn != nil {
			paid, closing := moneyOutput(entry.PaidAmount), entry.PaidIn.ClosingDate.String()
			out.Payment.PaidAmount, out.Payment.PaidIn = &paid, &closing
		}
	}
	out.Summary = statementChargesSummary(out, view)
	return out, nil
}

func stringsOfIDs(ids []uuid.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	return out
}

func statementChargesSummary(out StatementSummaryOutput, view ledger.StatementChargesView) string {
	statement, composition := view.Review.Statement, view.Review.Composition
	var b strings.Builder
	fmt.Fprintf(&b, "Fatura do cartão final %s de %s (fechamento %s, vencimento %s): %s cobrados.", statement.CardLast4, monthLabel(statement.Month), statement.ClosingDate.Format(), statement.Version.DueDate.Format(), statement.Version.Total.Format())
	fmt.Fprintf(&b, " Compras %s, parcelas %s, encargos %s", composition.Purchases.Amount.Format(), composition.Installments.Amount.Format(), composition.Charges.Amount.Format())
	if !composition.Credits.Amount.IsZero() {
		fmt.Fprintf(&b, ", créditos e estornos −%s", composition.Credits.Amount.Format())
	}
	if !composition.Payments.Amount.IsZero() {
		fmt.Fprintf(&b, ", pagamentos −%s", composition.Payments.Amount.Format())
	}
	if composition.PreviousBalance != nil {
		fmt.Fprintf(&b, ", saldo anterior %s", composition.PreviousBalance.Format())
	}
	b.WriteString(".")
	for _, subtotal := range view.Modalities {
		fmt.Fprintf(&b, " %s: %s nas parcelas do ciclo", modalityName(string(subtotal.Modality)), subtotal.Amount.Format())
		if !subtotal.IncludedCharges.IsZero() {
			fmt.Fprintf(&b, ", dos quais %s de juros e encargos detalhados", subtotal.IncludedCharges.Format())
		}
		b.WriteString(" (já dentro das parcelas).")
	}
	switch view.Review.Status {
	case ledger.ReviewReconciled:
		b.WriteString(" Conferida: a composição explica o total.")
	case ledger.ReviewDivergent:
		fmt.Fprintf(&b, " Divergente em %s.", composition.Difference.Format())
	default:
		b.WriteString(" Conferência incompleta.")
	}
	if out.Payment.Paid {
		fmt.Fprintf(&b, " Paga, segundo a fatura que fecha em %s.", calendarString(*out.Payment.PaidIn))
	} else {
		b.WriteString(" Ainda sem comprovação de pagamento.")
	}
	if len(view.Review.Clarifications) > 0 {
		fmt.Fprintf(&b, " %d dúvida(s) em aberto.", len(view.Review.Clarifications))
	}
	return b.String()
}

func monthLabel(month string) string {
	parsed, err := time.Parse("2006-01", month)
	if err != nil {
		return month
	}
	return parsed.Format("01/2006")
}

func calendarString(value string) string {
	day, err := calendar.ParseDate(value)
	if err != nil {
		return value
	}
	return day.Format()
}

func (s *Server) futureCommitments(ctx context.Context, c call, in FutureCommitmentsInput) (FutureCommitmentsOutput, error) {
	filter := ledger.ViewFilter{Modality: optionalString(in.Modality), CardLast4: optionalString(in.CardLast4), Merchant: optionalString(in.Merchant)}
	view, err := s.deps.Ledger.FutureCommitments(ctx, c.principal.AccountID, filter, c.receivedAt)
	if err != nil {
		return FutureCommitmentsOutput{}, err
	}
	out := FutureCommitmentsOutput{View: string(ledger.ViewCommitments), AsOf: view.AsOf.String(), Totals: []CommitmentTotalsOutput{}, Months: []CommitmentMonthOutput{}, Plans: []PlanCommitmentOutput{}}
	for _, total := range view.Totals {
		out.Totals = append(out.Totals, CommitmentTotalsOutput{Currency: total.Currency, Known: moneyOutput(total.Known), Estimated: moneyOutput(total.Estimated), Total: moneyOutput(total.Total())})
	}
	for _, month := range view.Months {
		out.Months = append(out.Months, CommitmentMonthOutput{Month: month.Month, Currency: month.Currency, Known: moneyOutput(month.Known), Estimated: moneyOutput(month.Estimated), Installments: month.Installments, MonthEstimated: month.MonthEstimated})
	}
	stale := 0
	for _, plan := range view.Plans {
		out.Plans = append(out.Plans, PlanCommitmentOutput{
			TransactionID: plan.TransactionID.String(), Description: plan.Description, Modality: string(plan.Modality), Count: plan.Count,
			Remaining: moneyOutput(plan.Remaining), RemainingKnown: moneyOutput(plan.RemainingKnown), RemainingEstimated: moneyOutput(plan.RemainingEstimated),
			InformedOn: plan.InformedOn.String(), Stale: plan.Stale,
		})
		if plan.Stale {
			stale++
		}
	}
	if view.OldestInformation != nil {
		oldest := view.OldestInformation.String()
		out.OldestInformation = &oldest
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Parcelas futuras em %s:", view.AsOf.Format())
	if len(out.Totals) == 0 {
		b.WriteString(" nenhuma parcela prevista.")
	}
	for _, total := range out.Totals {
		fmt.Fprintf(&b, " %s (%s conhecidos e %s estimados)", total.Total.Formatted, total.Known.Formatted, total.Estimated.Formatted)
	}
	if len(out.Plans) > 0 {
		fmt.Fprintf(&b, " em %d parcelamento(s). Não somam como gasto: são cobranças futuras.", len(out.Plans))
	}
	if len(out.Months) > 0 {
		fmt.Fprintf(&b, " Próximo mês: %s, %s.", monthLabel(out.Months[0].Month), moneyOutput(money.Money{Minor: view.Months[0].Known.Minor + view.Months[0].Estimated.Minor, Currency: view.Months[0].Currency}).Formatted)
	}
	if view.OldestInformation != nil {
		fmt.Fprintf(&b, " Informação mais antiga de %s.", view.OldestInformation.Format())
	}
	if stale > 0 {
		fmt.Fprintf(&b, " %d parcelamento(s) com projeção possivelmente desatualizada: a próxima fatura confirma.", stale)
	}
	out.Summary = b.String()
	return out, nil
}

func (s *Server) pixSummary(ctx context.Context, c call, _ NoInput) (PixInstallmentSummaryOutput, error) {
	view, err := s.deps.Ledger.PixInstallments(ctx, c.principal.AccountID)
	if err != nil {
		return PixInstallmentSummaryOutput{}, err
	}
	out := PixInstallmentSummaryOutput{View: "pix_installments", Plans: []PixPlanOutput{}, Totals: []PixTotalsOutput{}}
	withoutPurpose := 0
	for _, entry := range view.Plans {
		plan := PixPlanOutput{
			Transaction: transactionOutput(entry.Transaction, nil), Count: entry.Plan.Count, Charged: moneyOutput(entry.Plan.Charged),
			WithoutBreakdown: entry.Plan.ChargedWithoutDetail, RemainingKnown: moneyOutput(entry.Plan.RemainingKnown), RemainingEstimated: moneyOutput(entry.Plan.RemainingEstimated),
		}
		for _, component := range entry.Plan.Breakdown {
			plan.Breakdown = append(plan.Breakdown, ComponentTotalOutput{Kind: string(component.Kind), Amount: moneyOutput(component.Amount)})
		}
		if entry.Transaction.Purpose == nil {
			withoutPurpose++
		}
		out.Plans = append(out.Plans, plan)
	}
	var b strings.Builder
	b.WriteString("Pix parcelado:")
	if len(view.Totals) == 0 {
		b.WriteString(" nenhum parcelamento registrado.")
	}
	for _, total := range view.Totals {
		entry := PixTotalsOutput{
			Currency: total.Currency, Charged: moneyOutput(total.Charged), WithoutBreakdown: total.WithoutBreakdown,
			RemainingKnown: moneyOutput(total.RemainingKnown), RemainingEstimated: moneyOutput(total.RemainingEstimated), Breakdown: []ComponentTotalOutput{},
		}
		for _, kind := range ledger.ComponentKinds {
			if amount, ok := total.Breakdown[kind]; ok {
				entry.Breakdown = append(entry.Breakdown, ComponentTotalOutput{Kind: string(kind), Amount: moneyOutput(amount)})
			}
		}
		out.Totals = append(out.Totals, entry)
		fmt.Fprintf(&b, " %s cobrados até agora em %d parcelamento(s)", entry.Charged.Formatted, len(view.Plans))
		interest := total.Breakdown[ledger.ComponentInterest]
		iof := total.Breakdown[ledger.ComponentIOF]
		fee := total.Breakdown[ledger.ComponentFee]
		if interest.Minor+iof.Minor+fee.Minor > 0 {
			fmt.Fprintf(&b, ", dos quais %s de juros, %s de IOF e %s de tarifas, segundo o detalhamento das faturas", money.Money{Minor: interest.Minor, Currency: total.Currency}.Format(), money.Money{Minor: iof.Minor, Currency: total.Currency}.Format(), money.Money{Minor: fee.Minor, Currency: total.Currency}.Format())
		}
		b.WriteString(".")
		if total.WithoutBreakdown > 0 {
			fmt.Fprintf(&b, " %d parcela(s) vieram sem detalhamento: os juros delas não são conhecidos.", total.WithoutBreakdown)
		}
		fmt.Fprintf(&b, " Ainda faltam %s (%s conhecidos e %s estimados).", money.Money{Minor: total.RemainingKnown.Minor + total.RemainingEstimated.Minor, Currency: total.Currency}.Format(), entry.RemainingKnown.Formatted, entry.RemainingEstimated.Formatted)
	}
	if withoutPurpose > 0 {
		fmt.Fprintf(&b, " %d parcelamento(s) sem finalidade informada.", withoutPurpose)
	}
	out.Summary = b.String()
	return out, nil
}
