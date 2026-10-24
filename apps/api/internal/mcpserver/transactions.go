package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

var clearableFields = []any{"merchant", "payee", "payment_method", "purpose", "installments", "date"}

type UpdateTransactionInput struct {
	TransactionID     string   `json:"transaction_id"`
	RequestID         string   `json:"request_id" jsonschema:"generated once for this correction and reused on every retry"`
	Reason            string   `json:"reason" jsonschema:"what Rudolfo asked, in Portuguese; kept in the history"`
	Description       string   `json:"description,omitempty"`
	Merchant          string   `json:"merchant,omitempty"`
	Payee             string   `json:"payee,omitempty"`
	Amount            string   `json:"amount,omitempty" jsonschema:"new total as a canonical decimal; the original stays in the evidence"`
	Currency          string   `json:"currency,omitempty" jsonschema:"ISO 4217 code of the new amount; defaults to the current currency"`
	MessageAt         string   `json:"message_at,omitempty" jsonschema:"when Rudolfo sent the correction, RFC 3339, for date_reference"`
	DateReference     string   `json:"date_reference,omitempty"`
	OccurredAt        string   `json:"occurred_at,omitempty" jsonschema:"RFC 3339 with offset"`
	OccurredOn        string   `json:"occurred_on,omitempty" jsonschema:"YYYY-MM-DD"`
	PaymentMethod     string   `json:"payment_method,omitempty"`
	CardLast4         string   `json:"card_last4,omitempty"`
	Nature            string   `json:"nature,omitempty"`
	Purpose           string   `json:"purpose,omitempty"`
	InstallmentCount  int      `json:"installment_count,omitempty"`
	InstallmentAmount string   `json:"installment_amount,omitempty"`
	Clear             []string `json:"clear,omitempty" jsonschema:"fields Rudolfo said are not known after all"`
}

type CorrectionOutput struct {
	Outcome     Outcome           `json:"outcome"`
	Transaction TransactionOutput `json:"transaction"`
	Changed     []string          `json:"changed"`
	Summary     string            `json:"summary"`
}

func (o CorrectionOutput) reportedOutcome() Outcome {
	return o.Outcome
}

type SetCategoryInput struct {
	TransactionID  string `json:"transaction_id"`
	RequestID      string `json:"request_id" jsonschema:"generated once for this classification and reused on every retry"`
	Category       string `json:"category" jsonschema:"category code from list_categories"`
	CategoryStatus string `json:"category_status,omitempty" jsonschema:"confirmed (default) when Rudolfo said it; suggested when you classified"`
	Reason         string `json:"reason,omitempty" jsonschema:"kept in the history"`
}

type VoidTransactionInput struct {
	TransactionID string `json:"transaction_id"`
	RequestID     string `json:"request_id" jsonschema:"generated once and reused on every retry"`
	Reason        string `json:"reason" jsonschema:"why the record was a mistake, in Portuguese"`
}

type GetTransactionInput struct {
	TransactionID string `json:"transaction_id"`
}

type EvidenceOutput struct {
	ObservationID string   `json:"observation_id"`
	Source        string   `json:"source"`
	Status        string   `json:"status"`
	Origin        string   `json:"origin"`
	Score         *int     `json:"score,omitempty"`
	Reasons       []string `json:"reasons,omitempty"`
	ReceivedAt    string   `json:"received_at"`
}

type RevisionOutput struct {
	Field     string `json:"field"`
	OldValue  any    `json:"old_value"`
	NewValue  any    `json:"new_value"`
	Reason    string `json:"reason"`
	Author    string `json:"author" jsonschema:"user, agent, rule or system"`
	CreatedAt string `json:"created_at"`
}

type InstallmentOutput struct {
	Number      int         `json:"number"`
	Amount      MoneyOutput `json:"amount"`
	State       string      `json:"state" jsonschema:"charged (on a statement), known_future (stated by Rudolfo or printed on a statement) or estimated (projected from a known installment or the total)"`
	ClosingDate *string     `json:"closing_date,omitempty" jsonschema:"statement that charged or informed it"`
	InformedOn  string      `json:"informed_on" jsonschema:"date of the information; an old date means the projection may be stale"`
}

type InstallmentPlanOutput struct {
	Modality           string              `json:"modality" jsonschema:"card_installment or pix_installment"`
	Count              int                 `json:"count"`
	InstallmentAmount  *MoneyOutput        `json:"installment_amount,omitempty"`
	AmountKnown        bool                `json:"amount_known" jsonschema:"true when Rudolfo or a statement stated the installment amount; false when it was only seen charged"`
	Total              *MoneyOutput        `json:"total,omitempty" jsonschema:"contracted total, only when informed; never computed from one installment"`
	InformedOn         string              `json:"informed_on"`
	Installments       []InstallmentOutput `json:"installments" jsonschema:"charged ones and the ones still to come; numbers charged before Lastro saw the plan are left out"`
	Charged            MoneyOutput         `json:"charged"`
	RemainingKnown     MoneyOutput         `json:"remaining_known" jsonschema:"future installments that are known"`
	RemainingEstimated MoneyOutput         `json:"remaining_estimated" jsonschema:"future installments that are estimated"`
	Remaining          MoneyOutput         `json:"remaining" jsonschema:"future commitment of this plan: known plus estimated"`
}

type TransactionDetailOutput struct {
	Transaction     TransactionOutput      `json:"transaction"`
	InstallmentPlan *InstallmentPlanOutput `json:"installment_plan,omitempty" jsonschema:"for purchases in installments: what was charged and what is still to come"`
	Evidence        []EvidenceOutput       `json:"evidence"`
	History         []RevisionOutput       `json:"history" jsonschema:"every change, oldest first"`
	Clarifications  []ClarificationOutput  `json:"clarifications,omitempty" jsonschema:"open questions about this transaction"`
}

type ListTransactionsInput struct {
	From          string `json:"from,omitempty" jsonschema:"first purchase date, YYYY-MM-DD"`
	To            string `json:"to,omitempty" jsonschema:"last purchase date, YYYY-MM-DD"`
	Status        string `json:"status,omitempty" jsonschema:"only this status; voided and merged records appear only when asked"`
	Category      string `json:"category,omitempty" jsonschema:"category code; unidentified also lists transactions without category"`
	PaymentMethod string `json:"payment_method,omitempty"`
	Limit         int    `json:"limit,omitempty" jsonschema:"up to 200 (default 50)"`
	Offset        int    `json:"offset,omitempty" jsonschema:"skip this many, to page through results"`
}

type ListTransactionsOutput struct {
	Transactions []TransactionOutput `json:"transactions"`
	Count        int                 `json:"count" jsonschema:"number of transactions in this page"`
	Summary      string              `json:"summary"`
}

func (s *Server) registerTransactions() {
	paymentMethods := enumValues([]ledger.PaymentKind{ledger.PaymentCreditCard, ledger.PaymentDebitCard, ledger.PaymentPix, ledger.PaymentCash})
	natures := enumValues([]ledger.Nature{ledger.NatureExpense, ledger.NatureCharge, ledger.NatureCredit, ledger.NatureRefund, ledger.NaturePayment, ledger.NatureTransfer, ledger.NatureAdjustment})

	addTool(s, toolSpec{
		name:        "update_transaction",
		title:       "Correct a transaction",
		description: "Changes description, merchant, payee, amount, date, payment method, nature, purpose or installments of a transaction because Rudolfo said so. Every change keeps the reason in the history and the original value in the evidence; fields he changes are protected from automatic changes later.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
		enums: map[string][]any{
			"date_reference": {"now", "today", "yesterday"},
			"payment_method": paymentMethods,
			"nature":         natures,
		},
	}, func(ctx context.Context, c call, in UpdateTransactionInput) (CorrectionOutput, error) {
		id, err := parseID("transaction_id", in.TransactionID)
		if err != nil {
			return CorrectionOutput{}, err
		}
		for _, field := range in.Clear {
			if !slices.Contains(clearableFields, any(field)) {
				return CorrectionOutput{}, invalid("clear", fmt.Sprintf("can list %v", clearableFields))
			}
		}
		current, err := s.deps.Ledger.GetTransaction(ctx, c.principal.AccountID, id)
		if err != nil {
			return CorrectionOutput{}, err
		}
		patch, err := s.patchFrom(ctx, c, in, current.Transaction)
		if err != nil {
			return CorrectionOutput{}, err
		}
		correction, err := s.deps.Ledger.Correct(ctx, c.principal.AccountID, id, patch, in.Reason, c.actor(ledger.AuthorUser, in.RequestID))
		if err != nil {
			return CorrectionOutput{}, err
		}
		changed := make([]string, len(correction.Changed))
		for i, field := range correction.Changed {
			changed[i] = string(field)
		}
		return CorrectionOutput{
			Outcome:     OutcomeCompleted,
			Transaction: transactionOutput(correction.Transaction, &correction.Category),
			Changed:     changed,
			Summary:     correctionSummary(correction),
		}, nil
	})

	addTool(s, toolSpec{
		name:        "set_category",
		title:       "Classify a transaction",
		description: "Sets the category of a transaction. Each transaction has a single category. Rudolfo's classification is confirmed and is never replaced by later suggestions.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
		enums:       map[string][]any{"category_status": {"suggested", "confirmed"}},
	}, func(ctx context.Context, c call, in SetCategoryInput) (CorrectionOutput, error) {
		id, err := parseID("transaction_id", in.TransactionID)
		if err != nil {
			return CorrectionOutput{}, err
		}
		status, author, reason := ledger.AllocationConfirmed, ledger.AuthorUser, "Categoria definida por Rudolfo."
		if in.CategoryStatus == string(ledger.AllocationSuggested) {
			status, author, reason = ledger.AllocationSuggested, ledger.AuthorAgent, "Classificado pela Case."
		}
		if strings.TrimSpace(in.Reason) != "" {
			reason = in.Reason
		}
		result, err := s.deps.Ledger.Categorize(ctx, c.principal.AccountID, id, in.Category, status, reason, c.actor(author, in.RequestID))
		if err != nil {
			return CorrectionOutput{}, err
		}
		detail, err := s.deps.Ledger.GetTransaction(ctx, c.principal.AccountID, id)
		if err != nil {
			return CorrectionOutput{}, err
		}
		out := CorrectionOutput{Outcome: OutcomeCompleted, Transaction: transactionOutput(detail.Transaction, &detail.Category), Changed: []string{}}
		switch {
		case result.Conflict != nil:
			out.Summary = "A categoria confirmada por Rudolfo foi mantida; a sugestão virou uma dúvida."
		case result.Changed:
			out.Changed = []string{"category"}
			out.Summary = fmt.Sprintf("Classifiquei \"%s\" como %s.", detail.Description, detail.Category.Label)
		default:
			out.Summary = fmt.Sprintf("\"%s\" já estava classificado como %s.", detail.Description, detail.Category.Label)
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:        "void_transaction",
		title:       "Void a transaction recorded by mistake",
		description: "Marks a transaction recorded by mistake as voided. Nothing is deleted: it stays in the history and stops counting in views and questions.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
		destructive: true,
	}, func(ctx context.Context, c call, in VoidTransactionInput) (CorrectionOutput, error) {
		id, err := parseID("transaction_id", in.TransactionID)
		if err != nil {
			return CorrectionOutput{}, err
		}
		voided, err := s.deps.Ledger.Void(ctx, c.principal.AccountID, id, in.Reason, c.actor(ledger.AuthorUser, in.RequestID))
		if err != nil {
			return CorrectionOutput{}, err
		}
		return CorrectionOutput{
			Outcome:     OutcomeCompleted,
			Transaction: transactionOutput(voided, nil),
			Changed:     []string{"status"},
			Summary:     fmt.Sprintf("Anulei %s em \"%s\". O registro continua no histórico.", voided.Amount.Format(), voided.Description),
		}, nil
	})

	addTool(s, toolSpec{
		name:        "get_transaction",
		title:       "Show a transaction",
		description: "Returns a transaction with its categories, the evidence behind it, every change in order, the open questions about it and, for a purchase in installments, its plan: what was charged and the future commitment, known or estimated.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
	}, func(ctx context.Context, c call, in GetTransactionInput) (TransactionDetailOutput, error) {
		id, err := parseID("transaction_id", in.TransactionID)
		if err != nil {
			return TransactionDetailOutput{}, err
		}
		detail, err := s.deps.Ledger.GetTransaction(ctx, c.principal.AccountID, id)
		if err != nil {
			return TransactionDetailOutput{}, err
		}
		return detailOutput(detail), nil
	})

	addTool(s, toolSpec{
		name:        "list_transactions",
		title:       "List transactions",
		description: "Lists transactions by purchase period, status, category and payment method, newest first. It does not add amounts up: totals come from the query tools.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
		enums: map[string][]any{
			"status":         enumValues([]ledger.Status{ledger.StatusProvisional, ledger.StatusConfirmed, ledger.StatusPendingClarification, ledger.StatusVoided, ledger.StatusMerged}),
			"payment_method": paymentMethods,
		},
	}, func(ctx context.Context, c call, in ListTransactionsInput) (ListTransactionsOutput, error) {
		filter := ledger.TransactionFilter{Category: optionalString(in.Category), Limit: in.Limit, Offset: in.Offset}
		var err error
		if filter.From, err = parseOptionalDate("from", in.From); err != nil {
			return ListTransactionsOutput{}, err
		}
		if filter.To, err = parseOptionalDate("to", in.To); err != nil {
			return ListTransactionsOutput{}, err
		}
		if in.Status != "" {
			filter.Statuses = []ledger.Status{ledger.Status(in.Status)}
		}
		if in.PaymentMethod != "" {
			kind := ledger.PaymentKind(in.PaymentMethod)
			filter.PaymentMethod = &kind
		}
		listed, err := s.deps.Ledger.ListTransactions(ctx, c.principal.AccountID, filter)
		if err != nil {
			return ListTransactionsOutput{}, err
		}
		out := ListTransactionsOutput{Transactions: []TransactionOutput{}, Count: len(listed)}
		for _, transaction := range listed {
			out.Transactions = append(out.Transactions, transactionOutput(transaction.Transaction, &transaction.Category))
		}
		switch len(listed) {
		case 0:
			out.Summary = "Nenhum lançamento encontrado com esses filtros."
		case 1:
			out.Summary = "1 lançamento encontrado."
		default:
			out.Summary = fmt.Sprintf("%d lançamentos encontrados.", len(listed))
		}
		return out, nil
	})
}

func (s *Server) patchFrom(ctx context.Context, c call, in UpdateTransactionInput, current ledger.Transaction) (ledger.Patch, error) {
	var patch ledger.Patch
	cleared := func(field string) bool { return slices.Contains(in.Clear, field) }
	patch.Description = optionalString(in.Description)
	patch.Merchant = optionalField(in.Merchant, cleared("merchant"))
	patch.Payee = optionalField(in.Payee, cleared("payee"))
	patch.Purpose = optionalField(in.Purpose, cleared("purpose"))
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = current.Amount.Currency
	}
	if strings.TrimSpace(in.Amount) != "" {
		amount, err := money.Parse(strings.TrimSpace(in.Amount), currency)
		if err != nil {
			return patch, invalid("amount", err.Error())
		}
		patch.Amount = &amount
	}
	occurred, err := s.occurrenceFrom(ctx, c, in)
	if err != nil {
		return patch, err
	}
	switch {
	case occurred != nil:
		patch.Occurred = occurred
	case cleared("date"):
		unknown := calendar.Unknown()
		patch.Occurred = &unknown
	}
	switch {
	case in.PaymentMethod != "":
		patch.PaymentMethod = ledger.Value(ledger.PaymentMethodRef{Kind: ledger.PaymentKind(in.PaymentMethod), CardLast4: optionalString(in.CardLast4)})
	case cleared("payment_method"):
		patch.PaymentMethod = ledger.Cleared[ledger.PaymentMethodRef]()
	}
	if in.Nature != "" {
		nature := ledger.Nature(in.Nature)
		patch.Nature = &nature
	}
	switch {
	case in.InstallmentCount != 0 || strings.TrimSpace(in.InstallmentAmount) != "":
		installments := ledger.Installments{Count: in.InstallmentCount}
		if strings.TrimSpace(in.InstallmentAmount) != "" {
			amount, err := money.Parse(strings.TrimSpace(in.InstallmentAmount), currency)
			if err != nil {
				return patch, invalid("installment_amount", err.Error())
			}
			installments.Amount = &amount
		}
		patch.Installments = ledger.Value(installments)
	case cleared("installments"):
		patch.Installments = ledger.Cleared[ledger.Installments]()
	}
	if emptyPatch(patch) {
		return patch, invalid("transaction_id", "the call changes nothing: send at least one field to correct")
	}
	return patch, nil
}

func (s *Server) occurrenceFrom(ctx context.Context, c call, in UpdateTransactionInput) (*calendar.Occurrence, error) {
	given := 0
	for _, value := range []string{in.DateReference, in.OccurredAt, in.OccurredOn} {
		if strings.TrimSpace(value) != "" {
			given++
		}
	}
	if given == 0 {
		return nil, nil
	}
	if given > 1 {
		return nil, invalid("date_reference", "send only one of date_reference, occurred_at and occurred_on")
	}
	loc, err := s.deps.Accounts.Location(ctx, c.principal.AccountID)
	if err != nil {
		return nil, err
	}
	var occurred calendar.Occurrence
	switch {
	case in.DateReference != "":
		messageAt, err := parseOptionalInstant("message_at", in.MessageAt)
		if err != nil {
			return nil, err
		}
		reference := c.receivedAt
		if messageAt != nil {
			reference = *messageAt
		}
		occurred, err = calendar.Resolve(calendar.Reference(in.DateReference), reference, loc)
		if err != nil {
			return nil, invalid("date_reference", err.Error())
		}
	case in.OccurredAt != "":
		at, err := parseOptionalInstant("occurred_at", in.OccurredAt)
		if err != nil {
			return nil, err
		}
		occurred = calendar.Exact(*at, loc, calendar.SourceStated)
	default:
		on, err := parseOptionalDate("occurred_on", in.OccurredOn)
		if err != nil {
			return nil, err
		}
		occurred = calendar.OnDay(*on, calendar.SourceStated)
	}
	return &occurred, nil
}

func optionalField(value string, cleared bool) ledger.Optional[string] {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return ledger.Value(trimmed)
	}
	if cleared {
		return ledger.Cleared[string]()
	}
	return ledger.Optional[string]{}
}

func emptyPatch(p ledger.Patch) bool {
	return p.Description == nil && !p.Merchant.Set && !p.Payee.Set && p.Amount == nil && p.Occurred == nil &&
		!p.PaymentMethod.Set && p.Nature == nil && !p.Purpose.Set && !p.Installments.Set
}

func correctionSummary(correction ledger.Correction) string {
	if len(correction.Changed) == 0 {
		return fmt.Sprintf("Nada mudou em \"%s\": os valores já eram esses.", correction.Transaction.Description)
	}
	labels := make([]string, len(correction.Changed))
	for i, field := range correction.Changed {
		labels[i] = ledger.FieldLabel(field)
	}
	return fmt.Sprintf("Atualizei %s de \"%s\".", joinPortuguese(labels), correction.Transaction.Description)
}

func joinPortuguese(items []string) string {
	switch len(items) {
	case 0:
		return ""
	case 1:
		return items[0]
	default:
		return strings.Join(items[:len(items)-1], ", ") + " e " + items[len(items)-1]
	}
}

func detailOutput(detail ledger.TransactionDetail) TransactionDetailOutput {
	out := TransactionDetailOutput{
		Transaction: transactionOutput(detail.Transaction, &detail.Category),
		Evidence:    []EvidenceOutput{},
		History:     []RevisionOutput{},
	}
	for _, e := range detail.Evidence {
		out.Evidence = append(out.Evidence, EvidenceOutput{
			ObservationID: e.ObservationID.String(),
			Source:        string(e.Source),
			Status:        string(e.Status),
			Origin:        string(e.Origin),
			Score:         e.Score,
			Reasons:       e.Reasons,
			ReceivedAt:    e.ReceivedAt.UTC().Format(time.RFC3339),
		})
	}
	for _, r := range detail.History {
		out.History = append(out.History, RevisionOutput{
			Field:     r.Field,
			OldValue:  decoded(r.OldValue),
			NewValue:  decoded(r.NewValue),
			Reason:    r.Reason,
			Author:    string(r.Author),
			CreatedAt: r.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	for _, c := range detail.Clarifications {
		out.Clarifications = append(out.Clarifications, clarificationOutput(c))
	}
	if detail.Plan != nil {
		out.InstallmentPlan = planOutput(*detail.Plan)
	}
	return out
}

func planOutput(plan ledger.InstallmentPlan) *InstallmentPlanOutput {
	out := &InstallmentPlanOutput{
		Modality:           string(plan.Modality),
		Count:              plan.Count,
		AmountKnown:        plan.InstallmentAmountKnown,
		InformedOn:         plan.InformedOn.String(),
		Installments:       []InstallmentOutput{},
		Charged:            moneyOutput(plan.Charged),
		RemainingKnown:     moneyOutput(plan.RemainingKnown),
		RemainingEstimated: moneyOutput(plan.RemainingEstimated),
		Remaining:          moneyOutput(plan.Remaining()),
	}
	if plan.InstallmentAmount != nil {
		amount := moneyOutput(*plan.InstallmentAmount)
		out.InstallmentAmount = &amount
	}
	if plan.Total != nil {
		total := moneyOutput(*plan.Total)
		out.Total = &total
	}
	for _, installment := range plan.Installments {
		entry := InstallmentOutput{Number: installment.Number, Amount: moneyOutput(installment.Amount), State: string(installment.State), InformedOn: installment.InformedOn.String()}
		if installment.ClosingDate != nil {
			closing := installment.ClosingDate.String()
			entry.ClosingDate = &closing
		}
		out.Installments = append(out.Installments, entry)
	}
	return out
}

func decoded(raw json.RawMessage) any {
	var value any
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return nil
	}
	return value
}
