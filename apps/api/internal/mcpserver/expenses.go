package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

const defaultCurrency = "BRL"

type RecordExpenseInput struct {
	RequestID            string `json:"request_id" jsonschema:"generated once for this expense and reused on every retry of it"`
	RawText              string `json:"raw_text" jsonschema:"Rudolfo's message as sent, or the transcription of a voice message"`
	InputMode            string `json:"input_mode,omitempty" jsonschema:"text (default) or voice"`
	MessageAt            string `json:"message_at,omitempty" jsonschema:"when the message was sent, RFC 3339 with offset; omit when unknown"`
	Amount               string `json:"amount,omitempty" jsonschema:"total amount as a canonical decimal such as 327.39; omit when Rudolfo did not say it"`
	Currency             string `json:"currency,omitempty" jsonschema:"ISO 4217 code, BRL when omitted"`
	Description          string `json:"description,omitempty" jsonschema:"short description of what was bought, in Portuguese"`
	Merchant             string `json:"merchant,omitempty" jsonschema:"store or payee, only when known"`
	DateReference        string `json:"date_reference,omitempty" jsonschema:"now, today or yesterday when Rudolfo spoke relative to the message"`
	OccurredAt           string `json:"occurred_at,omitempty" jsonschema:"stated date and time, RFC 3339 with offset"`
	OccurredOn           string `json:"occurred_on,omitempty" jsonschema:"stated date without time, YYYY-MM-DD"`
	PaymentMethod        string `json:"payment_method,omitempty" jsonschema:"credit_card, debit_card, pix or cash; omit when unknown"`
	CardLast4            string `json:"card_last4,omitempty" jsonschema:"last 4 digits of the card, only when Rudolfo said them"`
	Category             string `json:"category,omitempty" jsonschema:"category code from list_categories"`
	CategoryStatus       string `json:"category_status,omitempty" jsonschema:"suggested (default) when you classified; confirmed only when Rudolfo said the category"`
	Purpose              string `json:"purpose,omitempty" jsonschema:"why the money was spent, only when Rudolfo said it"`
	InstallmentCount     int    `json:"installment_count,omitempty" jsonschema:"number of installments, from 2 to 120"`
	InstallmentAmount    string `json:"installment_amount,omitempty" jsonschema:"value of each installment as a canonical decimal"`
	RelatedTransactionID string `json:"related_transaction_id,omitempty" jsonschema:"a possible_match candidate that is this same purchase: the conversation becomes its evidence instead of a new transaction"`
	ConfirmDistinct      bool   `json:"confirm_distinct,omitempty" jsonschema:"true only after a possible_duplicate or possible_match turned out to be another real purchase"`
	DeferMatch           bool   `json:"defer_match,omitempty" jsonschema:"true when you cannot decide on a possible_match now: Lastro records it apart, pending clarification, and opens a possible_match question for Rudolfo"`
}

type RecordExpenseOutput struct {
	Outcome        Outcome                `json:"outcome"`
	Result         string                 `json:"result" jsonschema:"recorded, linked (confirmed an existing transaction), duplicate_of, possible_duplicate, possible_match or needs_clarification"`
	Transaction    *TransactionOutput     `json:"transaction,omitempty" jsonschema:"the recorded or linked transaction, or the existing one for duplicate_of and possible_duplicate"`
	Candidates     []MatchCandidateOutput `json:"candidates,omitempty" jsonschema:"for possible_match: transactions from Google Wallet or a statement that may be this same purchase"`
	Changed        []string               `json:"changed,omitempty" jsonschema:"for linked: fields of the existing transaction the conversation filled"`
	Inferred       []string               `json:"inferred,omitempty" jsonschema:"fields Lastro inferred, such as the date from now"`
	Pending        []string               `json:"pending,omitempty" jsonschema:"fields still missing"`
	Clarifications []ClarificationOutput  `json:"clarifications,omitempty" jsonschema:"questions opened for this expense"`
	Question       string                 `json:"question,omitempty" jsonschema:"what to ask Rudolfo for needs_clarification"`
	Summary        string                 `json:"summary" jsonschema:"text to relay to Rudolfo, in Portuguese"`
}

func (o RecordExpenseOutput) reportedOutcome() Outcome {
	return o.Outcome
}

func (s *Server) registerExpenses() {
	addTool(s, toolSpec{
		name:  "record_expense",
		title: "Record an expense from the conversation",
		description: "Records an expense Rudolfo reported, already understood and classified by you. Lastro validates it, deduplicates it by request_id and warns when the same text and amount arrive again in this conversation. " +
			"Partial records are fine when what is missing does not change the amount or the purchase (for example the payment method); missing amount or identity returns needs_clarification and records nothing. " +
			"Read get_instructions topic conversation first.",
		scope:      accounts.ScopeExpensesWrite,
		idempotent: true,
		enums: map[string][]any{
			"input_mode":      {"text", "voice"},
			"date_reference":  {"now", "today", "yesterday"},
			"payment_method":  enumValues([]ledger.PaymentKind{ledger.PaymentCreditCard, ledger.PaymentDebitCard, ledger.PaymentPix, ledger.PaymentCash}),
			"category_status": {"suggested", "confirmed"},
		},
	}, func(ctx context.Context, c call, in RecordExpenseInput) (RecordExpenseOutput, error) {
		report, err := expenseReport(c, in)
		if err != nil {
			return RecordExpenseOutput{}, err
		}
		outcome, err := s.deps.Ledger.RecordExpense(ctx, c.principal.AccountID, report, c.actor(ledger.AuthorAgent, in.RequestID))
		if err != nil {
			return RecordExpenseOutput{}, err
		}
		return recordExpenseOutput(outcome), nil
	})
}

func expenseReport(c call, in RecordExpenseInput) (ledger.ExpenseReport, error) {
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = defaultCurrency
	}
	report := ledger.ExpenseReport{
		RequestID:       in.RequestID,
		Conversation:    c.conversation,
		ReceivedAt:      c.receivedAt,
		InputMode:       ledger.InputMode(in.InputMode),
		RawText:         in.RawText,
		Description:     in.Description,
		Merchant:        optionalString(in.Merchant),
		Purpose:         optionalString(in.Purpose),
		ConfirmDistinct: in.ConfirmDistinct,
		DeferMatch:      in.DeferMatch,
	}
	var err error
	if strings.TrimSpace(in.RelatedTransactionID) != "" {
		related, err := parseID("related_transaction_id", in.RelatedTransactionID)
		if err != nil {
			return report, err
		}
		report.RelatedTransactionID = &related
	}
	if report.MessageAt, err = parseOptionalInstant("message_at", in.MessageAt); err != nil {
		return report, err
	}
	if report.OccurredAt, err = parseOptionalInstant("occurred_at", in.OccurredAt); err != nil {
		return report, err
	}
	if report.OccurredOn, err = parseOptionalDate("occurred_on", in.OccurredOn); err != nil {
		return report, err
	}
	if in.DateReference != "" {
		reference := calendar.Reference(in.DateReference)
		report.DateReference = &reference
	}
	if strings.TrimSpace(in.Amount) != "" {
		amount, err := money.Parse(strings.TrimSpace(in.Amount), currency)
		if err != nil {
			return report, invalid("amount", err.Error())
		}
		report.Amount = &amount
	} else if err := money.ValidCurrency(currency); err != nil {
		return report, invalid("currency", err.Error())
	}
	if in.PaymentMethod != "" {
		report.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentKind(in.PaymentMethod), CardLast4: optionalString(in.CardLast4)}
	} else if strings.TrimSpace(in.CardLast4) != "" {
		return report, invalid("payment_method", "is required with card_last4: send credit_card or debit_card")
	}
	if in.Category != "" {
		report.Category = optionalString(in.Category)
		report.CategoryStatus = ledger.AllocationStatus(in.CategoryStatus)
	}
	if in.InstallmentCount != 0 || strings.TrimSpace(in.InstallmentAmount) != "" {
		installments := &ledger.Installments{Count: in.InstallmentCount}
		if strings.TrimSpace(in.InstallmentAmount) != "" {
			amount, err := money.Parse(strings.TrimSpace(in.InstallmentAmount), currency)
			if err != nil {
				return report, invalid("installment_amount", err.Error())
			}
			installments.Amount = &amount
		}
		report.Installments = installments
	}
	return report, nil
}

func parseOptionalInstant(field, value string) (*time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil, nil
	}
	instant, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, invalid(field, fmt.Sprintf("%s must be RFC 3339 with an offset, such as 2026-10-21T23:50:00-03:00", field))
	}
	return &instant, nil
}

func recordExpenseOutput(outcome ledger.ExpenseOutcome) RecordExpenseOutput {
	out := RecordExpenseOutput{
		Outcome:  OutcomeCompleted,
		Result:   string(outcome.Result),
		Inferred: outcome.Inferred,
		Pending:  outcome.Pending,
		Question: outcome.Question,
	}
	if outcome.Transaction != nil {
		transaction := transactionOutput(*outcome.Transaction, &outcome.Category)
		out.Transaction = &transaction
	}
	for _, clarification := range outcome.Clarifications {
		out.Clarifications = append(out.Clarifications, clarificationOutput(clarification))
	}
	out.Candidates = candidateOutputs(outcome.Candidates)
	out.Changed = stringsOf(outcome.Changed)
	if len(out.Changed) == 0 {
		out.Changed = nil
	}
	switch outcome.Result {
	case ledger.ExpenseRecorded:
		out.Summary = recordedSummary(outcome)
	case ledger.ExpenseLinked:
		out.Summary = fmt.Sprintf("Essa compra já estava registrada: %s. A mensagem foi vinculada a ela e nada novo foi criado.", expenseLine(outcome))
	case ledger.ExpensePossibleMatch:
		out.Outcome = OutcomePending
		out.Summary = fmt.Sprintf("Nada foi registrado: há %d lançamento(s) do Google Wallet ou da fatura que podem ser esta mesma compra. Decida pelos motivos e diferenças, ou pergunte a Rudolfo, e chame de novo com o mesmo request_id e related_transaction_id ou confirm_distinct.", len(outcome.Candidates))
	case ledger.ExpenseDuplicateOf:
		out.Summary = fmt.Sprintf("Essa despesa já estava registrada: %s. Nada novo foi criado.", expenseLine(outcome))
	case ledger.ExpensePossibleDuplicate:
		out.Outcome = OutcomePending
		when := ""
		if outcome.RepeatedAt != nil && outcome.Location != nil {
			when = " às " + outcome.RepeatedAt.In(outcome.Location).Format("15:04")
		}
		out.Summary = fmt.Sprintf("Nada foi registrado: nesta conversa já há %s, informado%s. Pergunte se é a mesma compra.", expenseLine(outcome), when)
	case ledger.ExpenseNeedsClarification:
		out.Outcome = OutcomePending
		out.Summary = "Nada foi registrado ainda. " + outcome.Question
	}
	return out
}

func recordedSummary(outcome ledger.ExpenseOutcome) string {
	tr := outcome.Transaction
	var b strings.Builder
	fmt.Fprintf(&b, "Registrei %s.", expenseLine(outcome))
	switch {
	case tr.Status == ledger.StatusConfirmed:
		b.WriteString(" Situação: confirmada.")
	case tr.PaymentMethod != nil && tr.PaymentMethod.Kind == ledger.PaymentCreditCard:
		b.WriteString(" Situação: provisória até aparecer na fatura.")
	default:
		b.WriteString(" Situação: provisória.")
	}
	if pending := ledger.PendingLabels(outcome.Pending); len(pending) > 0 {
		fmt.Fprintf(&b, " Falta: %s.", strings.Join(pending, ", "))
	}
	return b.String()
}

func expenseLine(outcome ledger.ExpenseOutcome) string {
	tr := outcome.Transaction
	line := fmt.Sprintf("%s em \"%s\"", tr.Amount.Format(), tr.Description)
	if category := outcome.Category; category.Status != ledger.AllocationMissing && category.Code != "" {
		line += ", categoria " + category.Label
		if category.Status == ledger.AllocationSuggested {
			line += " (sugerida)"
		}
	}
	if tr.Occurred.On == nil {
		return line + ", sem data"
	}
	line += ", " + tr.Occurred.On.Format()
	if tr.Occurred.Source == calendar.SourceInferred {
		line += " (data inferida)"
	}
	return line
}
