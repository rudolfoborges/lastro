package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/idempotency"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/redaction"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	operationRecordExpense = "record_expense"
	reasonConversationLink = "Conferido com o que Rudolfo contou na conversa."
	repetitionWindow       = 24 * time.Hour
	maxFutureMessageAt     = time.Hour
	maxRawText             = maxOriginalDescription
)

type InputMode string

const (
	InputText  InputMode = "text"
	InputVoice InputMode = "voice"
)

type ExpenseResult string

const (
	ExpenseRecorded           ExpenseResult = "recorded"
	ExpenseLinked             ExpenseResult = "linked"
	ExpenseDuplicateOf        ExpenseResult = "duplicate_of"
	ExpensePossibleDuplicate  ExpenseResult = "possible_duplicate"
	ExpensePossibleMatch      ExpenseResult = "possible_match"
	ExpenseNeedsClarification ExpenseResult = "needs_clarification"
)

type ExpenseReport struct {
	RequestID            string
	Conversation         string
	ReceivedAt           time.Time
	MessageAt            *time.Time
	InputMode            InputMode
	RawText              string
	Amount               *money.Money
	Description          string
	Merchant             *string
	DateReference        *calendar.Reference
	OccurredAt           *time.Time
	OccurredOn           *calendar.Date
	PaymentMethod        *PaymentMethodRef
	Category             *string
	CategoryStatus       AllocationStatus
	Purpose              *string
	Installments         *Installments
	CategoryRuleID       *uuid.UUID
	Nature               Nature
	RefundOfID           *uuid.UUID
	RelatedTransactionID *uuid.UUID
	ConfirmDistinct      bool
	DeferMatch           bool
}

type ExpenseOutcome struct {
	Result         ExpenseResult
	Transaction    *Transaction
	Category       Allocation
	Inferred       []string
	Pending        []string
	Clarifications []Clarification
	Candidates     []MatchCandidate
	Changed        []Field
	Question       string
	RepeatedAt     *time.Time
	Location       *time.Location
}

type recordedExpense struct {
	TransactionID uuid.UUID `json:"transaction_id"`
	Linked        bool      `json:"linked,omitempty"`
	Changed       []Field   `json:"changed,omitempty"`
}

type pendingExpense struct {
	outcome ExpenseOutcome
}

func (p *pendingExpense) Error() string {
	return string(p.outcome.Result)
}

func (s *Service) RecordExpense(ctx context.Context, accountID uuid.UUID, report ExpenseReport, actor Actor) (ExpenseOutcome, error) {
	report, err := normalizeReport(report)
	if err != nil {
		return ExpenseOutcome{}, err
	}
	if question := missingIdentity(report); question != "" {
		return ExpenseOutcome{Result: ExpenseNeedsClarification, Question: question}, nil
	}

	var outcome ExpenseOutcome
	err = s.inTx(ctx, accountID, func(t *tx) error {
		loc, err := t.location()
		if err != nil {
			return err
		}
		stored, replayed, err := idempotency.Do(t.ctx, t.q, t.accountID, operationRecordExpense, report.RequestID, hashableReport(report), func() (recordedExpense, error) {
			return t.recordExpense(report, loc, actor)
		})
		if err != nil {
			return err
		}
		outcome, err = t.expenseOutcome(stored.TransactionID, report, loc)
		switch {
		case replayed:
			outcome.Result = ExpenseDuplicateOf
		case stored.Linked:
			outcome.Result, outcome.Changed = ExpenseLinked, stored.Changed
		}
		return err
	})
	var pending *pendingExpense
	if errors.As(err, &pending) {
		return pending.outcome, nil
	}
	return outcome, err
}

func (t *tx) recordExpense(report ExpenseReport, loc *time.Location, actor Actor) (recordedExpense, error) {
	rule, err := t.applyRule(report.CategoryRuleID, &report.Category, "category_rule_id")
	if err != nil {
		return recordedExpense{}, err
	}
	if rule != nil && report.CategoryStatus == "" {
		report.CategoryStatus = AllocationSuggested
	}
	occurred, err := occurrenceOf(report, loc)
	if err != nil {
		return recordedExpense{}, err
	}
	fingerprint := repetitionFingerprint(report)
	var candidates []MatchCandidate
	if report.RelatedTransactionID == nil {
		if !report.ConfirmDistinct && !report.DeferMatch {
			if err := t.checkRepetition(report, fingerprint, loc); err != nil {
				return recordedExpense{}, err
			}
		}
		if candidates, err = t.findCandidates(conversationProbe(report, occurred, loc), loc); err != nil {
			return recordedExpense{}, err
		}
		if len(candidates) > 0 && !report.ConfirmDistinct && !report.DeferMatch {
			return recordedExpense{}, &pendingExpense{outcome: ExpenseOutcome{Result: ExpensePossibleMatch, Candidates: candidates, Location: loc}}
		}
	}

	interpreted := map[string]any{"description": report.Description}
	if report.Merchant != nil {
		interpreted["merchant"] = *report.Merchant
	}
	if report.Purpose != nil {
		interpreted["purpose"] = *report.Purpose
	}
	payload := map[string]any{
		"request_id":  report.RequestID,
		"input_mode":  report.InputMode,
		"raw_text":    report.RawText,
		"message_at":  report.messageAt().UTC().Format(time.RFC3339),
		"interpreted": interpreted,
	}
	if report.Conversation != "" {
		payload["conversation_hash"] = conversationHash(report.Conversation)
	}
	observation, err := t.recordObservation(NewObservation{
		Source:      SourceConversation,
		SourceKey:   report.RequestID,
		Payload:     payload,
		Fingerprint: fingerprint,
		ReceivedAt:  report.ReceivedAt,
	})
	if err != nil {
		return recordedExpense{}, err
	}
	report = withRedactedText(report, observation.Payload)
	details := map[string]any{"description": report.Description}
	if report.Category != nil {
		details["category"], details["category_status"] = *report.Category, report.CategoryStatus
	}
	if report.Purpose != nil {
		details["purpose"] = *report.Purpose
	}
	event := EventPurchase
	if report.Nature == NatureRefund {
		event = EventRefund
	}
	_, err = t.recordInterpretation(observation.ID, Interpretation{
		EventType:     event,
		Amount:        report.Amount,
		Merchant:      report.Merchant,
		Occurred:      occurred,
		PaymentMethod: report.PaymentMethod,
		Details:       details,
		Author:        AuthorAgent,
	})
	if err != nil {
		return recordedExpense{}, err
	}

	if report.RelatedTransactionID != nil {
		return t.linkReport(*report.RelatedTransactionID, observation.ID, report, occurred, loc, actor, rule)
	}
	var purchase *Transaction
	if report.RefundOfID != nil {
		refunded, err := t.refundable(*report.RefundOfID, *report.Amount, "refund_of_id")
		if err != nil {
			return recordedExpense{}, err
		}
		purchase = &refunded
		if report.Merchant == nil {
			report.Merchant = refunded.Merchant
		}
	}
	description := report.Description
	switch {
	case description == "" && report.Nature == NatureRefund:
		description = refundDescription(purchase, report.Merchant)
	case description == "":
		description = *report.Merchant
	}
	created, err := t.createTransaction(observation.ID, NewTransaction{
		OriginalDescription: report.RawText,
		Description:         description,
		Merchant:            report.Merchant,
		Amount:              *report.Amount,
		Occurred:            occurred,
		PaymentMethod:       report.PaymentMethod,
		Nature:              report.Nature,
		Status:              InitialStatus(report.PaymentMethod),
		Purpose:             report.Purpose,
		Installments:        report.Installments,
		RefundOfID:          report.RefundOfID,
	}, actor)
	if err != nil {
		return recordedExpense{}, err
	}
	if report.Category == nil && purchase != nil {
		current, err := t.currentCategory(purchase.ID)
		if err != nil {
			return recordedExpense{}, err
		}
		if current != nil {
			report.Category, report.CategoryStatus = &current.Code, AllocationSuggested
		}
	}
	if err := t.categorizeReport(created, report, actor, rule, false); err != nil {
		return recordedExpense{}, err
	}
	if err := t.syncPlan(created, calendar.DateOf(report.messageAt(), loc), report.Installments != nil && report.Installments.Amount != nil); err != nil {
		return recordedExpense{}, err
	}
	if err := t.settleCandidates(created, candidates, report.DeferMatch, actor); err != nil {
		return recordedExpense{}, err
	}
	if err := t.refreshClarifications(created.ID); err != nil {
		return recordedExpense{}, err
	}
	return recordedExpense{TransactionID: created.ID}, nil
}

func (t *tx) checkRepetition(report ExpenseReport, fingerprint []byte, loc *time.Location) error {
	if report.Conversation == "" {
		return nil
	}
	repeated, err := t.q.FindRecentRepetition(t.ctx, tenantdb.FindRecentRepetitionParams{
		AccountID:   t.accountID,
		Source:      string(SourceConversation),
		Fingerprint: fingerprint,
		Since:       report.ReceivedAt.Add(-repetitionWindow),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("look for repetitions: %w", err)
	}
	existing, err := t.transaction(repeated.TransactionID)
	if err != nil {
		return err
	}
	current, err := t.currentCategory(existing.ID)
	if err != nil {
		return err
	}
	repeatedAt := repeated.ReceivedAt
	return &pendingExpense{outcome: ExpenseOutcome{
		Result:      ExpensePossibleDuplicate,
		Transaction: &existing,
		Category:    EffectiveCategory(existing, current),
		RepeatedAt:  &repeatedAt,
		Location:    loc,
	}}
}

func conversationProbe(report ExpenseReport, occurred calendar.Occurrence, loc *time.Location) matchProbe {
	probe := matchProbe{
		nature:          report.Nature,
		amount:          *report.Amount,
		merchant:        report.Merchant,
		occurred:        occurred,
		method:          report.PaymentMethod,
		excludedSources: []Source{SourceConversation},
		paymentKinds:    compatibleKinds(report.PaymentMethod),
		label:           "na conversa",
	}
	probe.from, probe.to = occurrenceWindow(occurred, calendar.DateOf(report.messageAt(), loc), matchWindowDays)
	return probe
}

func (t *tx) linkReport(transactionID, observationID uuid.UUID, report ExpenseReport, occurred calendar.Occurrence, loc *time.Location, actor Actor, rule *ClassificationRule) (recordedExpense, error) {
	current, err := t.lockOpenTransaction(transactionID)
	switch {
	case errors.Is(err, ErrNotFound):
		return recordedExpense{}, &ValidationError{Field: "related_transaction_id", Message: "is not a transaction of Rudolfo's account"}
	case errors.Is(err, ErrTransactionClosed):
		return recordedExpense{}, &ValidationError{Field: "related_transaction_id", Message: "is voided or merged; link to the transaction that is still open"}
	case err != nil:
		return recordedExpense{}, err
	}
	if current.Nature != report.Nature {
		return recordedExpense{}, &ValidationError{Field: "related_transaction_id", Message: fmt.Sprintf("must be a transaction of nature %s, like what Rudolfo told", report.Nature)}
	}
	if err := t.assertDistinctSources(transactionID, observationID, SourceConversation); err != nil {
		return recordedExpense{}, err
	}
	if _, err := t.linkEvidence(transactionID, observationID, EvidenceLink{Status: EvidenceConfirmed, Origin: OriginAgent, Reasons: []string{"same purchase, identified by Case"}}); err != nil {
		return recordedExpense{}, err
	}
	actor.ObservationID = &observationID
	correction, err := t.correct(transactionID, conversationGapPatch(current, report, occurred), reasonConversationLink, actor)
	if err != nil {
		return recordedExpense{}, err
	}
	if slices.Contains(correction.Changed, FieldInstallments) {
		if err := t.syncPlan(correction.Transaction, calendar.DateOf(report.messageAt(), loc), report.Installments.Amount != nil); err != nil {
			return recordedExpense{}, err
		}
	}
	if err := t.categorizeReport(correction.Transaction, report, actor, rule, true); err != nil {
		return recordedExpense{}, err
	}
	if err := t.refreshClarifications(transactionID); err != nil {
		return recordedExpense{}, err
	}
	return recordedExpense{TransactionID: transactionID, Linked: true, Changed: correction.Changed}, nil
}

func conversationGapPatch(current Transaction, report ExpenseReport, occurred calendar.Occurrence) Patch {
	var patch Patch
	if current.Merchant == nil && report.Merchant != nil {
		patch.Merchant = Value(*report.Merchant)
	}
	if current.Purpose == nil && report.Purpose != nil {
		patch.Purpose = Value(*report.Purpose)
	}
	if ref := report.PaymentMethod; ref != nil {
		method := current.PaymentMethod
		if method == nil || (method.Kind == ref.Kind && method.CardLast4 == nil && ref.CardLast4 != nil) {
			patch.PaymentMethod = Value(*ref)
		}
	}
	if occurred.Known() && (current.Occurred.Precision == calendar.PrecisionUnknown || current.Occurred.Precision == calendar.PrecisionMonth) {
		patch.Occurred = &occurred
	}
	if current.Installments == nil && report.Installments != nil && (report.Installments.Amount == nil || report.Installments.Amount.Currency == current.Amount.Currency) {
		patch.Installments = Value(*report.Installments)
	}
	return patch
}

func (t *tx) categorizeReport(transaction Transaction, report ExpenseReport, actor Actor, rule *ClassificationRule, onlyWhenMissing bool) error {
	if report.Category == nil {
		return nil
	}
	categorizer, reason := classifierOf(actor, rule)
	if report.CategoryStatus == AllocationConfirmed {
		categorizer.Author, reason = AuthorUser, "Categoria informada por Rudolfo."
	} else if onlyWhenMissing {
		current, err := t.currentCategory(transaction.ID)
		if err != nil || current != nil {
			return err
		}
	}
	_, err := t.categorize(transaction, *report.Category, report.CategoryStatus, reason, categorizer)
	return err
}

func (t *tx) expenseOutcome(transactionID uuid.UUID, report ExpenseReport, loc *time.Location) (ExpenseOutcome, error) {
	transaction, err := t.transaction(transactionID)
	if err != nil {
		return ExpenseOutcome{}, err
	}
	current, err := t.currentCategory(transactionID)
	if err != nil {
		return ExpenseOutcome{}, err
	}
	rows, err := t.q.OpenTransactionClarifications(t.ctx, tenantdb.OpenTransactionClarificationsParams{AccountID: t.accountID, TransactionID: &transactionID})
	if err != nil {
		return ExpenseOutcome{}, fmt.Errorf("list open clarifications: %w", err)
	}
	outcome := ExpenseOutcome{
		Result:      ExpenseRecorded,
		Transaction: &transaction,
		Category:    EffectiveCategory(transaction, current),
		Location:    loc,
	}
	for _, row := range rows {
		clarification, err := t.withTarget(row)
		if err != nil {
			return ExpenseOutcome{}, err
		}
		outcome.Clarifications = append(outcome.Clarifications, clarification)
	}
	if transaction.Occurred.Source == calendar.SourceInferred {
		outcome.Inferred = append(outcome.Inferred, "date")
	}
	if report.MessageAt == nil && report.DateReference != nil {
		outcome.Inferred = append(outcome.Inferred, "message_at")
	}
	if transaction.PaymentMethod == nil {
		outcome.Pending = append(outcome.Pending, "payment_method")
	}
	if current == nil {
		outcome.Pending = append(outcome.Pending, "category")
	}
	if !transaction.Occurred.Known() {
		outcome.Pending = append(outcome.Pending, "date")
	}
	if transaction.PaymentMethod != nil && transaction.PaymentMethod.Kind == PaymentPix && transaction.Purpose == nil {
		outcome.Pending = append(outcome.Pending, "purpose")
	}
	return outcome, nil
}

func (t *tx) location() (*time.Location, error) {
	account, err := t.q.GetAccount(t.ctx, t.accountID)
	if err != nil {
		return nil, fmt.Errorf("get account: %w", err)
	}
	return calendar.LoadTimeZone(account.Timezone)
}

func normalizeReport(report ExpenseReport) (ExpenseReport, error) {
	report.RequestID = strings.TrimSpace(report.RequestID)
	if report.RequestID == "" {
		return report, &ValidationError{Field: "request_id", Message: "is required: generate one per expense and reuse it on retries"}
	}
	if report.InputMode == "" {
		report.InputMode = InputText
	}
	if report.InputMode != InputText && report.InputMode != InputVoice {
		return report, &ValidationError{Field: "input_mode", Message: "must be text or voice"}
	}
	rawText, err := requiredText("raw_text", report.RawText, maxRawText)
	if err != nil {
		return report, err
	}
	report.RawText = rawText
	report.Description = strings.TrimSpace(report.Description)
	if len([]rune(report.Description)) > maxShortText {
		return report, &ValidationError{Field: "description", Message: fmt.Sprintf("must have at most %d characters", maxShortText)}
	}
	if report.Merchant, err = optionalText("merchant", report.Merchant, maxShortText); err != nil {
		return report, err
	}
	if report.Purpose, err = optionalText("purpose", report.Purpose, maxShortText); err != nil {
		return report, err
	}
	if report.ReceivedAt.IsZero() {
		report.ReceivedAt = time.Now()
	}
	if report.MessageAt != nil && report.MessageAt.After(report.ReceivedAt.Add(maxFutureMessageAt)) {
		return report, &ValidationError{Field: "message_at", Message: "is in the future; omit it when you do not know when the message was sent"}
	}
	dates := 0
	for _, given := range []bool{report.DateReference != nil, report.OccurredAt != nil, report.OccurredOn != nil} {
		if given {
			dates++
		}
	}
	if dates > 1 {
		return report, &ValidationError{Field: "date_reference", Message: "send only one of date_reference, occurred_at and occurred_on"}
	}
	if report.Category != nil {
		code := strings.TrimSpace(*report.Category)
		report.Category = &code
		if report.CategoryStatus == "" {
			report.CategoryStatus = AllocationSuggested
		}
	}
	if report.Amount != nil {
		if err := validateAmount("amount", *report.Amount); err != nil {
			return report, err
		}
	}
	if report.PaymentMethod != nil {
		if err := validatePaymentMethod(*report.PaymentMethod); err != nil {
			return report, err
		}
	}
	if report.RelatedTransactionID != nil && report.ConfirmDistinct {
		return report, &ValidationError{Field: "confirm_distinct", Message: "cannot be sent with related_transaction_id"}
	}
	if report.Nature == "" {
		report.Nature = NatureExpense
	}
	switch {
	case report.Nature != NatureExpense && report.Nature != NatureRefund:
		return report, &ValidationError{Field: "nature", Message: "must be expense or refund; other movements come from the statement"}
	case report.RefundOfID != nil && report.Nature != NatureRefund:
		return report, &ValidationError{Field: "refund_of_id", Message: "applies only to nature refund"}
	case report.Nature == NatureRefund && report.Installments != nil:
		return report, &ValidationError{Field: "installment_count", Message: "does not apply to a refund"}
	}
	if report.DeferMatch && (report.RelatedTransactionID != nil || report.ConfirmDistinct) {
		return report, &ValidationError{Field: "defer_match", Message: "is for when you cannot decide; leave it out with related_transaction_id or confirm_distinct"}
	}
	return report, nil
}

func missingIdentity(report ExpenseReport) string {
	switch {
	case report.Amount == nil:
		return "Qual foi o valor?"
	case report.Description == "" && report.Merchant == nil && report.RefundOfID == nil:
		return "O que foi comprado, ou onde?"
	default:
		return ""
	}
}

func occurrenceOf(report ExpenseReport, loc *time.Location) (calendar.Occurrence, error) {
	switch {
	case report.DateReference != nil:
		occurred, err := calendar.Resolve(*report.DateReference, report.messageAt(), loc)
		if err != nil {
			return calendar.Occurrence{}, &ValidationError{Field: "date_reference", Message: err.Error()}
		}
		return occurred, nil
	case report.OccurredAt != nil:
		return calendar.Exact(*report.OccurredAt, loc, calendar.SourceStated), nil
	case report.OccurredOn != nil:
		return calendar.OnDay(*report.OccurredOn, calendar.SourceStated), nil
	default:
		return calendar.Unknown(), nil
	}
}

func (r ExpenseReport) messageAt() time.Time {
	if r.MessageAt != nil {
		return *r.MessageAt
	}
	return r.ReceivedAt
}

func hashableReport(report ExpenseReport) ExpenseReport {
	report.ReceivedAt = time.Time{}
	return report
}

func conversationHash(conversation string) string {
	sum := sha256.Sum256([]byte(conversation))
	return hex.EncodeToString(sum[:])
}

func repetitionFingerprint(report ExpenseReport) []byte {
	normalized := strings.Join(strings.Fields(strings.ToLower(redaction.Text(report.RawText).Text)), " ")
	sum := sha256.Sum256([]byte(strings.Join([]string{report.Conversation, normalized, report.Amount.String()}, "\x1f")))
	return sum[:]
}

func withRedactedText(report ExpenseReport, payload map[string]any) ExpenseReport {
	report.RawText, _ = payload["raw_text"].(string)
	interpreted, _ := payload["interpreted"].(map[string]any)
	report.Description, _ = interpreted["description"].(string)
	if merchant, ok := interpreted["merchant"].(string); ok {
		report.Merchant = &merchant
	}
	if purpose, ok := interpreted["purpose"].(string); ok {
		report.Purpose = &purpose
	}
	return report
}

func PendingLabels(pending []string) []string {
	labels := map[string]string{
		"payment_method": "meio de pagamento",
		"category":       "categoria",
		"date":           "data",
		"purpose":        "finalidade",
	}
	out := make([]string, 0, len(pending))
	for _, field := range pending {
		if label, ok := labels[field]; ok && !slices.Contains(out, label) {
			out = append(out, label)
		}
	}
	return out
}
