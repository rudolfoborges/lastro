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
	ExpenseDuplicateOf        ExpenseResult = "duplicate_of"
	ExpensePossibleDuplicate  ExpenseResult = "possible_duplicate"
	ExpenseNeedsClarification ExpenseResult = "needs_clarification"
)

type ExpenseReport struct {
	RequestID       string
	Conversation    string
	ReceivedAt      time.Time
	MessageAt       *time.Time
	InputMode       InputMode
	RawText         string
	Amount          *money.Money
	Description     string
	Merchant        *string
	DateReference   *calendar.Reference
	OccurredAt      *time.Time
	OccurredOn      *calendar.Date
	PaymentMethod   *PaymentMethodRef
	Category        *string
	CategoryStatus  AllocationStatus
	Purpose         *string
	Installments    *Installments
	ConfirmDistinct bool
}

type ExpenseOutcome struct {
	Result         ExpenseResult
	Transaction    *Transaction
	Category       Allocation
	Inferred       []string
	Pending        []string
	Clarifications []Clarification
	Question       string
	RepeatedAt     *time.Time
	Location       *time.Location
}

type recordedExpense struct {
	TransactionID uuid.UUID `json:"transaction_id"`
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
		if replayed {
			outcome.Result = ExpenseDuplicateOf
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
	fingerprint := repetitionFingerprint(report)
	if !report.ConfirmDistinct && report.Conversation != "" {
		repeated, err := t.q.FindRecentRepetition(t.ctx, tenantdb.FindRecentRepetitionParams{
			AccountID:   t.accountID,
			Source:      string(SourceConversation),
			Fingerprint: fingerprint,
			Since:       report.ReceivedAt.Add(-repetitionWindow),
		})
		switch {
		case err == nil:
			existing, err := t.transaction(repeated.TransactionID)
			if err != nil {
				return recordedExpense{}, err
			}
			current, err := t.currentCategory(existing.ID)
			if err != nil {
				return recordedExpense{}, err
			}
			repeatedAt := repeated.ReceivedAt
			return recordedExpense{}, &pendingExpense{outcome: ExpenseOutcome{
				Result:      ExpensePossibleDuplicate,
				Transaction: &existing,
				Category:    EffectiveCategory(existing, current),
				RepeatedAt:  &repeatedAt,
				Location:    loc,
			}}
		case !errors.Is(err, pgx.ErrNoRows):
			return recordedExpense{}, fmt.Errorf("look for repetitions: %w", err)
		}
	}

	occurred, err := occurrenceOf(report, loc)
	if err != nil {
		return recordedExpense{}, err
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
	_, err = t.recordInterpretation(observation.ID, Interpretation{
		EventType:     EventPurchase,
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

	description := report.Description
	if description == "" {
		description = *report.Merchant
	}
	created, err := t.createTransaction(observation.ID, NewTransaction{
		OriginalDescription: report.RawText,
		Description:         description,
		Merchant:            report.Merchant,
		Amount:              *report.Amount,
		Occurred:            occurred,
		PaymentMethod:       report.PaymentMethod,
		Nature:              NatureExpense,
		Status:              InitialStatus(report.PaymentMethod),
		Purpose:             report.Purpose,
		Installments:        report.Installments,
	}, actor)
	if err != nil {
		return recordedExpense{}, err
	}
	if report.Category != nil {
		categorizer, reason := actor, "Classificado pela Case."
		if report.CategoryStatus == AllocationConfirmed {
			categorizer.Author, reason = AuthorUser, "Categoria informada por Rudolfo."
		}
		if _, err := t.categorize(created, *report.Category, report.CategoryStatus, reason, categorizer); err != nil {
			return recordedExpense{}, err
		}
	}
	if err := t.refreshClarifications(created.ID); err != nil {
		return recordedExpense{}, err
	}
	return recordedExpense{TransactionID: created.ID}, nil
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
	return report, nil
}

func missingIdentity(report ExpenseReport) string {
	switch {
	case report.Amount == nil:
		return "Qual foi o valor?"
	case report.Description == "" && report.Merchant == nil:
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
