package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

const maxInterpretationsPerCall = 50

type ListPendingNotificationsInput struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"next_cursor from the previous page; omit for the first page"`
	Limit  int    `json:"limit,omitempty" jsonschema:"notifications per page, 20 by default and at most 50"`
}

type NotificationInterpretationOutput struct {
	EventType     string  `json:"event_type"`
	Effect        string  `json:"effect" jsonschema:"what it did: none, created, linked, updated, voided, refund_created or undone"`
	TransactionID *string `json:"transaction_id,omitempty"`
}

type NotificationVersionOutput struct {
	ObservationID  string                            `json:"observation_id"`
	Version        int                               `json:"version"`
	PostedAt       string                            `json:"posted_at" jsonschema:"when the phone showed it, in Rudolfo's time zone"`
	Title          string                            `json:"title,omitempty"`
	Text           string                            `json:"text,omitempty"`
	BigText        string                            `json:"big_text,omitempty"`
	Interpretation *NotificationInterpretationOutput `json:"interpretation,omitempty" jsonschema:"your current reading of this earlier version"`
}

type PendingNotificationOutput struct {
	ObservationID    string                      `json:"observation_id" jsonschema:"send it back in submit_notification_interpretations"`
	NotificationID   string                      `json:"notification_id" jsonschema:"same for every version of this notification on the phone"`
	Version          int                         `json:"version" jsonschema:"1 for the first post; higher when the notification changed"`
	Event            string                      `json:"event" jsonschema:"posted or updated, as the phone reported it"`
	DeviceID         string                      `json:"device_id"`
	PostedAt         string                      `json:"posted_at" jsonschema:"when the phone showed it, in Rudolfo's time zone; the purchase time unless the text says otherwise"`
	ReceivedAt       string                      `json:"received_at"`
	Title            string                      `json:"title,omitempty"`
	Text             string                      `json:"text,omitempty"`
	BigText          string                      `json:"big_text,omitempty"`
	Removed          bool                        `json:"removed,omitempty" jsonschema:"Rudolfo dismissed it on the phone; this is never a cancellation"`
	PreviousVersions []NotificationVersionOutput `json:"previous_versions,omitempty" jsonschema:"earlier versions of the same notification, oldest first"`
}

type ListPendingNotificationsOutput struct {
	Notifications []PendingNotificationOutput `json:"notifications"`
	NextCursor    string                      `json:"next_cursor,omitempty" jsonschema:"present when there are more pending notifications"`
	Pending       int                         `json:"pending" jsonschema:"notifications waiting for your interpretation in total"`
	Summary       string                      `json:"summary"`
}

type InterpretationInput struct {
	ObservationID        string `json:"observation_id" jsonschema:"from list_pending_notifications"`
	EventType            string `json:"event_type" jsonschema:"what the notification says happened"`
	Amount               string `json:"amount,omitempty" jsonschema:"amount shown, as a canonical decimal such as 32.97; required for purchase and refund"`
	Currency             string `json:"currency,omitempty" jsonschema:"ISO 4217 code, BRL when omitted"`
	Description          string `json:"description,omitempty" jsonschema:"short description in Portuguese; the merchant when omitted"`
	Merchant             string `json:"merchant,omitempty" jsonschema:"store as shown in the notification"`
	PaymentMethod        string `json:"payment_method,omitempty" jsonschema:"credit_card or debit_card, only when you know which card it is"`
	CardLast4            string `json:"card_last4,omitempty" jsonschema:"last 4 digits shown in the notification"`
	OccurredAt           string `json:"occurred_at,omitempty" jsonschema:"purchase time written in the text, RFC 3339 with offset; omit to use posted_at"`
	Category             string `json:"category,omitempty" jsonschema:"suggested category code from list_categories"`
	Purpose              string `json:"purpose,omitempty" jsonschema:"only when Rudolfo told you why"`
	RelatedTransactionID string `json:"related_transaction_id,omitempty" jsonschema:"transaction this notification refers to: required for purchase_update and cancellation, the refunded purchase for refund, or a conversation record that is the same purchase"`
	ConfirmDistinct      bool   `json:"confirm_distinct,omitempty" jsonschema:"true when a possible_match candidate is a different purchase"`
	DeferMatch           bool   `json:"defer_match,omitempty" jsonschema:"true when you cannot decide on a possible_match now: Lastro records it apart, pending clarification, and opens a possible_match question for Rudolfo"`
	Question             string `json:"question,omitempty" jsonschema:"for unknown: what to ask Rudolfo, in Portuguese"`
	Reinterpret          bool   `json:"reinterpret,omitempty" jsonschema:"true to replace an earlier, different reading of the same version"`
	Reason               string `json:"reason,omitempty" jsonschema:"why the reading changed, in Portuguese; required with reinterpret"`
}

type SubmitNotificationInterpretationsInput struct {
	Interpretations []InterpretationInput `json:"interpretations" jsonschema:"one item per notification version, at most 50"`
}

type MatchCandidateOutput struct {
	Transaction TransactionOutput `json:"transaction"`
	Sources     []string          `json:"sources" jsonschema:"where the candidate already has evidence from: conversation, wallet_notification or statement_line"`
	Reasons     []string          `json:"reasons" jsonschema:"what matches, in Portuguese"`
	Differences []string          `json:"differences,omitempty" jsonschema:"what does not match, in Portuguese; weigh them before deciding"`
}

func candidateOutputs(candidates []ledger.MatchCandidate) []MatchCandidateOutput {
	if len(candidates) == 0 {
		return nil
	}
	out := make([]MatchCandidateOutput, len(candidates))
	for i, candidate := range candidates {
		out[i] = MatchCandidateOutput{
			Transaction: transactionOutput(candidate.Transaction, &candidate.Category),
			Sources:     stringsOf(candidate.Sources),
			Reasons:     candidate.Reasons,
			Differences: candidate.Differences,
		}
	}
	return out
}

type InterpretationResultOutput struct {
	ObservationID  string                 `json:"observation_id"`
	Outcome        Outcome                `json:"outcome"`
	Result         string                 `json:"result,omitempty" jsonschema:"applied, already_applied or possible_match"`
	Effect         string                 `json:"effect,omitempty"`
	Transaction    *TransactionOutput     `json:"transaction,omitempty"`
	Changed        []string               `json:"changed,omitempty"`
	Conflicts      []string               `json:"conflicts,omitempty" jsonschema:"fields Rudolfo confirmed that the notification contradicts; a clarification was opened"`
	Candidates     []MatchCandidateOutput `json:"candidates,omitempty"`
	Clarifications []ClarificationOutput  `json:"clarifications,omitempty"`
	Error          *ToolError             `json:"error,omitempty"`
	Summary        string                 `json:"summary"`
}

type SubmitNotificationInterpretationsOutput struct {
	Outcome Outcome                      `json:"outcome"`
	Results []InterpretationResultOutput `json:"results"`
	Summary string                       `json:"summary"`
}

func (o SubmitNotificationInterpretationsOutput) reportedOutcome() Outcome {
	return o.Outcome
}

func (s *Server) registerNotifications() {
	addTool(s, toolSpec{
		name:  "list_pending_notifications",
		title: "List Google Wallet notifications waiting for interpretation",
		description: "Lists the notifications Rudolfo's phone captured from Google Wallet that you have not interpreted yet, oldest first, with their redacted text. " +
			"Lastro never reads them: interpret each one and send the result with submit_notification_interpretations. Read get_instructions topic notifications first.",
		scope:      accounts.ScopeExpensesRead,
		readOnly:   true,
		idempotent: true,
	}, func(ctx context.Context, c call, in ListPendingNotificationsInput) (ListPendingNotificationsOutput, error) {
		page, err := s.deps.Ledger.PendingNotifications(ctx, c.principal.AccountID, in.Cursor, in.Limit)
		if err != nil {
			return ListPendingNotificationsOutput{}, err
		}
		return pendingNotificationsOutput(page), nil
	})

	addTool(s, toolSpec{
		name:  "submit_notification_interpretations",
		title: "Submit your interpretation of Google Wallet notifications",
		description: "Applies your reading of each pending notification: a purchase becomes a transaction (or confirms a record from the conversation), an update corrects the same transaction, a cancellation voids it, a refund is recorded apart from the purchase; declined, notice, unrelated and unknown never become expenses. " +
			"Each item is applied on its own and is idempotent per notification version, so resending the same batch changes nothing.",
		scope:      accounts.ScopeExpensesWrite,
		idempotent: true,
		enums: map[string][]any{
			"interpretations.event_type": enumValues([]ledger.EventType{
				ledger.EventPurchase, ledger.EventPurchaseUpdate, ledger.EventCancellation, ledger.EventRefund,
				ledger.EventDeclined, ledger.EventNotice, ledger.EventUnrelated, ledger.EventUnknown,
			}),
			"interpretations.payment_method": enumValues([]ledger.PaymentKind{ledger.PaymentCreditCard, ledger.PaymentDebitCard}),
		},
	}, func(ctx context.Context, c call, in SubmitNotificationInterpretationsInput) (SubmitNotificationInterpretationsOutput, error) {
		if len(in.Interpretations) == 0 || len(in.Interpretations) > maxInterpretationsPerCall {
			return SubmitNotificationInterpretationsOutput{}, invalid("interpretations", fmt.Sprintf("send 1 to %d items", maxInterpretationsPerCall))
		}
		out := SubmitNotificationInterpretationsOutput{Results: make([]InterpretationResultOutput, 0, len(in.Interpretations))}
		for _, item := range in.Interpretations {
			out.Results = append(out.Results, s.interpretOne(ctx, c, item))
		}
		out.Outcome, out.Summary = batchOutcome(out.Results)
		return out, nil
	})
}

func (s *Server) interpretOne(ctx context.Context, c call, item InterpretationInput) InterpretationResultOutput {
	result := InterpretationResultOutput{ObservationID: item.ObservationID}
	reading, err := notificationInterpretation(item)
	if err == nil {
		var outcome ledger.InterpretationOutcome
		outcome, err = s.deps.Ledger.InterpretNotification(ctx, c.principal.AccountID, reading, c.actor(ledger.AuthorAgent, ""))
		if err == nil {
			return interpretationResultOutput(item.ObservationID, outcome)
		}
	}
	switch {
	case errors.Is(err, ledger.ErrAlreadyInterpreted):
		result.Error = &ToolError{Outcome: OutcomeFailed, Code: "already_interpreted", Message: err.Error()}
	case errors.Is(err, ledger.ErrReinterpretationUnsupported):
		result.Error = &ToolError{Outcome: OutcomeFailed, Code: "reinterpretation_not_supported", Message: err.Error()}
	default:
		result.Error = s.toToolError(ctx, "submit_notification_interpretations", err)
	}
	result.Outcome = OutcomeFailed
	result.Summary = "Nada foi aplicado para esta notificação: " + result.Error.Message
	return result
}

func notificationInterpretation(in InterpretationInput) (ledger.NotificationInterpretation, error) {
	observationID, err := parseID("observation_id", in.ObservationID)
	if err != nil {
		return ledger.NotificationInterpretation{}, err
	}
	reading := ledger.NotificationInterpretation{
		ObservationID:   observationID,
		EventType:       ledger.EventType(strings.TrimSpace(in.EventType)),
		Description:     in.Description,
		Merchant:        optionalString(in.Merchant),
		Category:        optionalString(in.Category),
		Purpose:         optionalString(in.Purpose),
		ConfirmDistinct: in.ConfirmDistinct,
		DeferMatch:      in.DeferMatch,
		Question:        in.Question,
		Reinterpret:     in.Reinterpret,
		Reason:          in.Reason,
	}
	currency := strings.TrimSpace(in.Currency)
	if currency == "" {
		currency = defaultCurrency
	}
	if strings.TrimSpace(in.Amount) != "" {
		amount, err := money.Parse(strings.TrimSpace(in.Amount), currency)
		if err != nil {
			return reading, invalid("amount", err.Error())
		}
		reading.Amount = &amount
	}
	switch kind := ledger.PaymentKind(strings.TrimSpace(in.PaymentMethod)); {
	case kind != "":
		reading.PaymentMethod = &ledger.PaymentMethodRef{Kind: kind, CardLast4: optionalString(in.CardLast4)}
	case strings.TrimSpace(in.CardLast4) != "":
		return reading, invalid("payment_method", "is required with card_last4: send credit_card or debit_card when you know which card it is, or leave both out")
	}
	if reading.OccurredAt, err = parseOptionalInstant("occurred_at", in.OccurredAt); err != nil {
		return reading, err
	}
	if strings.TrimSpace(in.RelatedTransactionID) != "" {
		related, err := parseID("related_transaction_id", in.RelatedTransactionID)
		if err != nil {
			return reading, err
		}
		reading.RelatedTransactionID = &related
	}
	return reading, nil
}

func interpretationResultOutput(observationID string, outcome ledger.InterpretationOutcome) InterpretationResultOutput {
	out := InterpretationResultOutput{
		ObservationID: observationID,
		Outcome:       OutcomeCompleted,
		Result:        string(outcome.Result),
		Effect:        string(outcome.Effect),
	}
	if outcome.Transaction != nil {
		transaction := transactionOutput(*outcome.Transaction, &outcome.Category)
		out.Transaction = &transaction
	}
	for _, field := range outcome.Changed {
		out.Changed = append(out.Changed, string(field))
	}
	for _, conflict := range outcome.Conflicts {
		out.Conflicts = append(out.Conflicts, string(conflict.Field))
	}
	out.Candidates = candidateOutputs(outcome.Candidates)
	for _, clarification := range outcome.Clarifications {
		out.Clarifications = append(out.Clarifications, clarificationOutput(clarification))
	}
	out.Summary = interpretationSummary(outcome)
	if outcome.Result == ledger.InterpretationPossibleMatch {
		out.Outcome = OutcomePending
	}
	return out
}

func interpretationSummary(outcome ledger.InterpretationOutcome) string {
	line := ""
	if tr := outcome.Transaction; tr != nil {
		line = fmt.Sprintf("%s em \"%s\"", tr.Amount.Format(), tr.Description)
	}
	switch outcome.Result {
	case ledger.InterpretationPossibleMatch:
		return fmt.Sprintf("Nada foi registrado: há %d lançamento(s) que podem ser a mesma compra. Pergunte a Rudolfo, ou decida pelos motivos, e envie de novo com related_transaction_id ou confirm_distinct.", len(outcome.Candidates))
	case ledger.InterpretationAlreadyApplied:
		return "Esta notificação já tinha sido interpretada assim; nada mudou."
	}
	switch outcome.Effect {
	case ledger.EffectCreated:
		return "Registrei " + line + " a partir da notificação."
	case ledger.EffectLinked:
		return "A notificação confirma " + line + ", que já estava registrado."
	case ledger.EffectUpdated:
		return "Atualizei " + line + " com a notificação."
	case ledger.EffectVoided:
		return "Anulei " + line + ": a notificação diz que a compra foi cancelada."
	case ledger.EffectRefundCreated:
		return "Registrei o estorno de " + line + "."
	case ledger.EffectUndone:
		return "A notificação deixou de valer para " + line + "."
	}
	if outcome.EventType == ledger.EventUnknown {
		return "Não virou despesa; abri uma dúvida para Rudolfo."
	}
	return "Não virou despesa."
}

func batchOutcome(results []InterpretationResultOutput) (Outcome, string) {
	counts := map[Outcome]int{}
	for _, result := range results {
		counts[result.Outcome]++
	}
	summary := fmt.Sprintf("%d aplicada(s), %d aguardando decisão, %d com erro.", counts[OutcomeCompleted], counts[OutcomePending], counts[OutcomeFailed])
	switch {
	case counts[OutcomeFailed] == len(results):
		return OutcomeFailed, summary
	case counts[OutcomeCompleted] == len(results):
		return OutcomeCompleted, summary
	default:
		return OutcomePending, summary
	}
}

func pendingNotificationsOutput(page ledger.PendingPage) ListPendingNotificationsOutput {
	out := ListPendingNotificationsOutput{
		Notifications: make([]PendingNotificationOutput, 0, len(page.Notifications)),
		NextCursor:    page.NextCursor,
		Pending:       page.Pending,
	}
	for _, n := range page.Notifications {
		item := PendingNotificationOutput{
			ObservationID:  n.ObservationID.String(),
			NotificationID: n.NotificationID.String(),
			Version:        n.Version,
			Event:          string(n.Event),
			DeviceID:       n.DeviceID,
			PostedAt:       n.PostedAt.In(page.Location).Format(time.RFC3339),
			ReceivedAt:     n.ReceivedAt.In(page.Location).Format(time.RFC3339),
			Title:          n.Title,
			Text:           n.Text,
			BigText:        n.BigText,
			Removed:        n.RemovedAt != nil,
		}
		for _, earlier := range n.Previous {
			version := NotificationVersionOutput{
				ObservationID: earlier.ObservationID.String(),
				Version:       earlier.Version,
				PostedAt:      earlier.PostedAt.In(page.Location).Format(time.RFC3339),
				Title:         earlier.Title,
				Text:          earlier.Text,
				BigText:       earlier.BigText,
			}
			if applied := earlier.Interpretation; applied != nil {
				version.Interpretation = &NotificationInterpretationOutput{EventType: string(applied.EventType), Effect: string(applied.Effect)}
				if applied.TransactionID != nil {
					id := applied.TransactionID.String()
					version.Interpretation.TransactionID = &id
				}
			}
			item.PreviousVersions = append(item.PreviousVersions, version)
		}
		out.Notifications = append(out.Notifications, item)
	}
	switch {
	case page.Pending == 0:
		out.Summary = "Nenhuma notificação pendente."
	case page.NextCursor != "":
		out.Summary = fmt.Sprintf("%d notificação(ões) pendente(s); esta página traz %d. Peça a próxima com next_cursor.", page.Pending, len(out.Notifications))
	default:
		out.Summary = fmt.Sprintf("%d notificação(ões) pendente(s).", page.Pending)
	}
	return out
}
