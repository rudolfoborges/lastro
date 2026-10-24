package ledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	maxQuestion         = 500
	subjectNotification = "notification"
	reasonLinked        = "Conferido com a notificação do Google Wallet."
	reasonUpdated       = "Atualização da notificação do Google Wallet."
	reasonCancelled     = "Compra cancelada, segundo notificação do Google Wallet."
	reasonClassified    = "Classificado pela Case."
)

var (
	ErrAlreadyInterpreted          = errors.New("this notification version was already interpreted differently; send reinterpret with a reason if the reading changed")
	ErrReinterpretationUnsupported = errors.New("this change of reading cannot be applied automatically; correct or void the transactions with update_transaction or void_transaction")
)

var nonExpenseEvents = []EventType{EventDeclined, EventNotice, EventUnrelated, EventUnknown}

type NotificationInterpretation struct {
	ObservationID        uuid.UUID
	EventType            EventType
	Amount               *money.Money
	Description          string
	Merchant             *string
	PaymentMethod        *PaymentMethodRef
	OccurredAt           *time.Time
	Category             *string
	Purpose              *string
	RelatedTransactionID *uuid.UUID
	ConfirmDistinct      bool
	Question             string
	Reinterpret          bool
	Reason               string
}

type InterpretationResult string

const (
	InterpretationApplied        InterpretationResult = "applied"
	InterpretationAlreadyApplied InterpretationResult = "already_applied"
	InterpretationPossibleMatch  InterpretationResult = "possible_match"
)

type InterpretationOutcome struct {
	Result         InterpretationResult
	EventType      EventType
	Effect         InterpretationEffect
	Transaction    *Transaction
	Category       Allocation
	Changed        []Field
	Conflicts      []FieldConflict
	Candidates     []MatchCandidate
	Clarifications []Clarification
	Location       *time.Location
}

type interpreter struct {
	t       *tx
	in      NotificationInterpretation
	version tenantdb.LockNotificationVersionRow
	content NotificationVersion
	loc     *time.Location
	actor   Actor
}

type applied struct {
	effect        InterpretationEffect
	transactionID *uuid.UUID
	changed       []Field
	conflicts     []FieldConflict
	candidates    []MatchCandidate
}

func (s *Service) InterpretNotification(ctx context.Context, accountID uuid.UUID, in NotificationInterpretation, actor Actor) (InterpretationOutcome, error) {
	in, err := normalizeInterpretation(in)
	if err != nil {
		return InterpretationOutcome{}, err
	}
	actor.ObservationID = &in.ObservationID
	var outcome InterpretationOutcome
	err = s.inTx(ctx, accountID, func(t *tx) error {
		loc, err := t.location()
		if err != nil {
			return err
		}
		version, err := t.q.LockNotificationVersion(t.ctx, tenantdb.LockNotificationVersionParams{AccountID: t.accountID, ObservationID: in.ObservationID})
		if err != nil {
			return fmt.Errorf("wallet notification %s: %w", in.ObservationID, notFound(err))
		}
		content, err := notificationVersion(version.ObservationID, version.NotificationID, version.Version, version.Event, version.PostedAt, version.ReceivedAt, version.Payload)
		if err != nil {
			return err
		}
		a := &interpreter{t: t, in: in, version: version, content: content, loc: loc, actor: actor}
		outcome, err = a.run()
		outcome.Location = loc
		return err
	})
	return outcome, err
}

func (a *interpreter) run() (InterpretationOutcome, error) {
	hash := a.in.submissionHash()
	latest, err := a.latest()
	if err != nil {
		return InterpretationOutcome{}, err
	}
	switch {
	case latest == nil:
		return a.apply(hash)
	case bytes.Equal(latest.submissionHash, hash):
		effect := EffectNone
		if latest.Effect != nil {
			effect = *latest.Effect
		}
		return a.outcome(InterpretationAlreadyApplied, applied{effect: effect, transactionID: latest.TransactionID})
	case !a.in.Reinterpret:
		return InterpretationOutcome{}, ErrAlreadyInterpreted
	default:
		return a.reinterpret(*latest, hash)
	}
}

func (a *interpreter) latest() (*InterpretationRecord, error) {
	rows, err := a.t.q.LatestInterpretations(a.t.ctx, tenantdb.LatestInterpretationsParams{AccountID: a.t.accountID, ObservationIds: []uuid.UUID{a.in.ObservationID}})
	if err != nil {
		return nil, fmt.Errorf("latest interpretation: %w", err)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	record, err := a.t.toInterpretation(rows[0])
	return &record, err
}

func (a *interpreter) apply(hash []byte) (InterpretationOutcome, error) {
	var result applied
	var err error
	switch a.in.EventType {
	case EventPurchase:
		if a.in.RelatedTransactionID != nil {
			result, err = a.linkPurchase(*a.in.RelatedTransactionID)
		} else {
			result, err = a.createPurchase()
		}
	case EventPurchaseUpdate:
		result, err = a.updatePurchase(*a.in.RelatedTransactionID)
	case EventCancellation:
		result, err = a.cancel(*a.in.RelatedTransactionID)
	case EventRefund:
		result, err = a.createRefund()
	case EventUnknown:
		result, err = applied{effect: EffectNone}, a.askAboutReading()
	default:
		result = applied{effect: EffectNone}
	}
	if err != nil {
		return InterpretationOutcome{}, err
	}
	if len(result.candidates) > 0 {
		return InterpretationOutcome{Result: InterpretationPossibleMatch, EventType: a.in.EventType, Candidates: result.candidates}, nil
	}
	return a.record(result, hash)
}

func (a *interpreter) reinterpret(previous InterpretationRecord, hash []byte) (InterpretationOutcome, error) {
	reason, err := requiredText("reason", a.in.Reason, maxReason)
	if err != nil {
		return InterpretationOutcome{}, err
	}
	effect := EffectNone
	if previous.Effect != nil {
		effect = *previous.Effect
	}
	owned := slices.Contains([]InterpretationEffect{EffectCreated, EffectLinked, EffectUpdated}, effect) && previous.TransactionID != nil
	purchase := a.in.EventType == EventPurchase || a.in.EventType == EventPurchaseUpdate
	sameTarget := a.in.RelatedTransactionID == nil || (previous.TransactionID != nil && *a.in.RelatedTransactionID == *previous.TransactionID)

	var result applied
	switch {
	case effect == EffectNone || effect == EffectUndone:
		return a.apply(hash)
	case owned && purchase && sameTarget:
		result, err = a.amend(*previous.TransactionID, a.correctionPatch, reason)
		result.effect = EffectUpdated
	case owned && slices.Contains(nonExpenseEvents, a.in.EventType):
		result, err = a.detach(*previous.TransactionID, effect == EffectCreated, reason)
		if err == nil && a.in.EventType == EventUnknown {
			err = a.askAboutReading()
		}
	default:
		return InterpretationOutcome{}, ErrReinterpretationUnsupported
	}
	if err != nil {
		return InterpretationOutcome{}, err
	}
	return a.record(result, hash)
}

func (a *interpreter) createPurchase() (applied, error) {
	if !a.in.ConfirmDistinct {
		candidates, err := a.matchCandidates()
		if err != nil || len(candidates) > 0 {
			return applied{candidates: candidates}, err
		}
	}
	description := a.in.Description
	if description == "" {
		description = *a.in.Merchant
	}
	created, err := a.t.createTransaction(a.in.ObservationID, NewTransaction{
		OriginalDescription: a.originalDescription(),
		Description:         description,
		Merchant:            a.in.Merchant,
		Amount:              *a.in.Amount,
		Occurred:            a.occurred(),
		PaymentMethod:       a.in.PaymentMethod,
		Nature:              NatureExpense,
		Status:              InitialStatus(a.in.PaymentMethod),
		Purpose:             a.in.Purpose,
	}, a.actor)
	if err != nil {
		return applied{}, err
	}
	if err := a.classify(created, false); err != nil {
		return applied{}, err
	}
	return applied{effect: EffectCreated, transactionID: &created.ID}, a.t.refreshClarifications(created.ID)
}

func (a *interpreter) linkPurchase(transactionID uuid.UUID) (applied, error) {
	current, err := a.t.lockOpenTransaction(transactionID)
	if err != nil {
		return applied{}, err
	}
	if current.Nature != NatureExpense {
		return applied{}, &ValidationError{Field: "related_transaction_id", Message: "must be a purchase (nature expense)"}
	}
	notifications, err := a.t.q.TransactionNotificationIDs(a.t.ctx, tenantdb.TransactionNotificationIDsParams{AccountID: a.t.accountID, TransactionID: transactionID})
	if err != nil {
		return applied{}, fmt.Errorf("list transaction notifications: %w", err)
	}
	if slices.ContainsFunc(notifications, func(id uuid.UUID) bool { return id != a.version.NotificationID }) {
		return applied{}, &ValidationError{Field: "related_transaction_id", Message: "is already confirmed by another Google Wallet notification; two notifications are two purchases unless Rudolfo says otherwise"}
	}
	if err := a.confirmEvidence(transactionID, "same purchase, identified by Case"); err != nil {
		return applied{}, err
	}
	result, err := a.amend(transactionID, a.gapPatch, reasonLinked)
	result.effect = EffectLinked
	return result, err
}

func (a *interpreter) updatePurchase(transactionID uuid.UUID) (applied, error) {
	ids, err := a.t.q.NotificationTransactionIDs(a.t.ctx, tenantdb.NotificationTransactionIDsParams{AccountID: a.t.accountID, NotificationID: a.version.NotificationID})
	if err != nil {
		return applied{}, fmt.Errorf("list notification transactions: %w", err)
	}
	if !slices.Contains(ids, transactionID) {
		return applied{}, &ValidationError{Field: "related_transaction_id", Message: "must be the transaction of an earlier version of this notification; to match a record from the conversation, send purchase with related_transaction_id"}
	}
	if _, err := a.t.lockOpenTransaction(transactionID); err != nil {
		return applied{}, err
	}
	if err := a.confirmEvidence(transactionID, "update of the same notification"); err != nil {
		return applied{}, err
	}
	result, err := a.amend(transactionID, a.correctionPatch, reasonUpdated)
	result.effect = EffectUpdated
	return result, err
}

func (a *interpreter) cancel(transactionID uuid.UUID) (applied, error) {
	if _, err := a.t.lockOpenTransaction(transactionID); err != nil {
		return applied{}, err
	}
	charged, err := a.t.q.HasConfirmedEvidenceFrom(a.t.ctx, tenantdb.HasConfirmedEvidenceFromParams{AccountID: a.t.accountID, TransactionID: transactionID, Source: string(SourceStatementLine)})
	if err != nil {
		return applied{}, fmt.Errorf("check statement evidence: %w", err)
	}
	if charged {
		return applied{}, &ValidationError{Field: "event_type", Message: "the statement already charges this purchase; a cancellation after the charge is a refund"}
	}
	if err := a.confirmEvidence(transactionID, "cancellation of this transaction"); err != nil {
		return applied{}, err
	}
	if _, err := a.t.void(transactionID, reasonCancelled, a.actor); err != nil {
		return applied{}, err
	}
	return applied{effect: EffectVoided, transactionID: &transactionID}, nil
}

func (a *interpreter) createRefund() (applied, error) {
	refund := NewTransaction{
		OriginalDescription: a.originalDescription(),
		Description:         a.in.Description,
		Merchant:            a.in.Merchant,
		Amount:              *a.in.Amount,
		Occurred:            a.occurred(),
		PaymentMethod:       a.in.PaymentMethod,
		Nature:              NatureRefund,
	}
	var original *Transaction
	if a.in.RelatedTransactionID != nil {
		purchase, err := a.t.refundable(*a.in.RelatedTransactionID, *a.in.Amount, "related_transaction_id")
		if err != nil {
			return applied{}, err
		}
		original, refund.RefundOfID = &purchase, &purchase.ID
		if refund.Merchant == nil {
			refund.Merchant = purchase.Merchant
		}
		if refund.PaymentMethod == nil && purchase.PaymentMethod != nil {
			refund.PaymentMethod = &PaymentMethodRef{Kind: purchase.PaymentMethod.Kind, CardLast4: purchase.PaymentMethod.CardLast4}
		}
	}
	if refund.Description == "" {
		refund.Description = refundDescription(original, refund.Merchant)
	}
	refund.Status = InitialStatus(refund.PaymentMethod)
	created, err := a.t.createTransaction(a.in.ObservationID, refund, a.actor)
	if err != nil {
		return applied{}, err
	}
	if a.in.Category == nil && original != nil {
		current, err := a.t.currentCategory(original.ID)
		if err != nil {
			return applied{}, err
		}
		if current != nil {
			a.in.Category = &current.Code
		}
	}
	if err := a.classify(created, false); err != nil {
		return applied{}, err
	}
	return applied{effect: EffectRefundCreated, transactionID: &created.ID}, a.t.refreshClarifications(created.ID)
}

func (t *tx) refundable(transactionID uuid.UUID, amount money.Money, field string) (Transaction, error) {
	purchase, err := t.lockOpenTransaction(transactionID)
	if err != nil {
		return Transaction{}, err
	}
	if purchase.Nature != NatureExpense {
		return Transaction{}, &ValidationError{Field: field, Message: "must be the purchase being refunded (nature expense)"}
	}
	if purchase.Amount.Currency != amount.Currency {
		return Transaction{}, &ValidationError{Field: "currency", Message: fmt.Sprintf("must be %s, the currency of the refunded purchase", purchase.Amount.Currency)}
	}
	refunded, err := t.q.SumRefunds(t.ctx, tenantdb.SumRefundsParams{AccountID: t.accountID, RefundOfID: &transactionID})
	if err != nil {
		return Transaction{}, fmt.Errorf("sum refunds: %w", err)
	}
	if refunded+amount.Minor > purchase.Amount.Minor {
		return Transaction{}, &ValidationError{Field: "amount", Message: fmt.Sprintf("refunds would exceed the purchase of %s", purchase.Amount.Format())}
	}
	return purchase, nil
}

func refundDescription(original *Transaction, merchant *string) string {
	switch {
	case original != nil:
		return truncateRunes("Estorno: "+original.Description, maxShortText)
	case merchant != nil:
		return truncateRunes("Estorno: "+*merchant, maxShortText)
	default:
		return "Estorno"
	}
}

func (a *interpreter) askAboutReading() error {
	question := a.in.Question
	if question == "" {
		question = fmt.Sprintf("Não entendi esta notificação do Google Wallet: \"%s\". Era uma compra?", truncateRunes(a.content.Content(), 300))
	}
	return a.t.openClarification(a.readingDoubt(truncateRunes(question, maxQuestion)))
}

func (a *interpreter) readingDoubt(question string) newClarification {
	return newClarification{
		kind:          ClarificationReadingDoubt,
		observationID: &a.in.ObservationID,
		subject:       subjectNotification,
		question:      question,
		context: map[string]any{
			"notification_id": a.version.NotificationID.String(),
			"version":         a.version.Version,
			"posted_at":       a.version.PostedAt.UTC().Format(time.RFC3339),
		},
	}
}

func (a *interpreter) closeReadingDoubt() error {
	row, err := a.t.q.GetOpenClarificationByDedupKey(a.t.ctx, tenantdb.GetOpenClarificationByDedupKeyParams{AccountID: a.t.accountID, DedupKey: dedupKey(a.readingDoubt(""))})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find reading doubt: %w", err)
	}
	_, err = a.t.closeClarification(row, ClarificationResolved, "Notificação reinterpretada.", AuthorAgent)
	return err
}

func (a *interpreter) detach(transactionID uuid.UUID, created bool, reason string) (applied, error) {
	evidence, err := a.t.q.LockConfirmedEvidence(a.t.ctx, tenantdb.LockConfirmedEvidenceParams{AccountID: a.t.accountID, TransactionID: transactionID, ObservationID: a.in.ObservationID})
	if err != nil {
		return applied{}, fmt.Errorf("evidence of %s: %w", transactionID, notFound(err))
	}
	if _, err := a.t.q.SetEvidenceStatus(a.t.ctx, tenantdb.SetEvidenceStatusParams{AccountID: a.t.accountID, ID: evidence.ID, Status: string(EvidenceUndone), Origin: string(OriginAgent)}); err != nil {
		return applied{}, fmt.Errorf("undo evidence: %w", err)
	}
	remaining, err := a.t.q.CountConfirmedEvidence(a.t.ctx, tenantdb.CountConfirmedEvidenceParams{AccountID: a.t.accountID, TransactionID: transactionID})
	if err != nil {
		return applied{}, fmt.Errorf("count evidence: %w", err)
	}
	if created && remaining == 0 {
		if _, err := a.t.void(transactionID, reason, a.actor); err != nil {
			return applied{}, err
		}
	}
	return applied{effect: EffectUndone, transactionID: &transactionID}, nil
}

func (a *interpreter) confirmEvidence(transactionID uuid.UUID, reason string) error {
	_, err := a.t.linkEvidence(transactionID, a.in.ObservationID, EvidenceLink{Status: EvidenceConfirmed, Origin: OriginAgent, Reasons: []string{reason}})
	return err
}

func (a *interpreter) amend(transactionID uuid.UUID, patchFor func(Transaction) Patch, reason string) (applied, error) {
	current, err := a.t.lockOpenTransaction(transactionID)
	if err != nil {
		return applied{}, err
	}
	correction, err := a.t.correct(transactionID, patchFor(current), reason, a.actor)
	if err != nil {
		return applied{}, err
	}
	if err := a.classify(correction.Transaction, true); err != nil {
		return applied{}, err
	}
	return applied{transactionID: &transactionID, changed: correction.Changed, conflicts: correction.Conflicts}, nil
}

func (a *interpreter) gapPatch(current Transaction) Patch {
	var patch Patch
	if a.in.Amount.Currency == current.Amount.Currency && a.in.Amount.Minor != current.Amount.Minor {
		patch.Amount = a.in.Amount
	}
	if current.Merchant == nil && a.in.Merchant != nil {
		patch.Merchant = Value(*a.in.Merchant)
	}
	if current.Occurred.Precision != calendar.PrecisionExact {
		occurred := a.occurred()
		patch.Occurred = &occurred
	}
	if current.PaymentMethod == nil && a.in.PaymentMethod != nil {
		patch.PaymentMethod = Value(*a.in.PaymentMethod)
	}
	if current.Purpose == nil && a.in.Purpose != nil {
		patch.Purpose = Value(*a.in.Purpose)
	}
	return patch
}

func (a *interpreter) correctionPatch(current Transaction) Patch {
	var patch Patch
	if a.in.Amount != nil && *a.in.Amount != current.Amount {
		patch.Amount = a.in.Amount
	}
	if a.in.Merchant != nil && (current.Merchant == nil || *current.Merchant != *a.in.Merchant) {
		patch.Merchant = Value(*a.in.Merchant)
	}
	if a.in.OccurredAt != nil {
		occurred := a.occurred()
		patch.Occurred = &occurred
	}
	if a.in.PaymentMethod != nil {
		patch.PaymentMethod = Value(*a.in.PaymentMethod)
	}
	if a.in.Purpose != nil {
		patch.Purpose = Value(*a.in.Purpose)
	}
	return patch
}

func (a *interpreter) classify(transaction Transaction, onlyWhenMissing bool) error {
	if a.in.Category == nil {
		return nil
	}
	if onlyWhenMissing {
		current, err := a.t.currentCategory(transaction.ID)
		if err != nil || current != nil {
			return err
		}
	}
	if _, err := a.t.categorize(transaction, *a.in.Category, AllocationSuggested, reasonClassified, a.actor); err != nil {
		return err
	}
	return a.t.refreshClarifications(transaction.ID)
}

func (a *interpreter) matchCandidates() ([]MatchCandidate, error) {
	var candidates []MatchCandidate
	earlier, err := a.t.q.NotificationTransactionIDs(a.t.ctx, tenantdb.NotificationTransactionIDsParams{AccountID: a.t.accountID, NotificationID: a.version.NotificationID})
	if err != nil {
		return nil, fmt.Errorf("list notification transactions: %w", err)
	}
	for _, id := range earlier {
		transaction, err := a.t.transaction(id)
		if err != nil {
			return nil, err
		}
		if transaction.Status == StatusVoided || transaction.Status == StatusMerged {
			continue
		}
		candidate, err := a.t.candidate(transaction, []string{"registrado a partir de uma versão anterior desta mesma notificação"})
		if err != nil {
			return nil, err
		}
		candidates = append(candidates, candidate)
	}
	occurred := a.occurred()
	probe := matchProbe{
		nature:          NatureExpense,
		amount:          *a.in.Amount,
		merchant:        a.in.Merchant,
		occurred:        occurred,
		method:          a.in.PaymentMethod,
		excludedIDs:     earlier,
		excludedSources: []Source{SourceWalletNotification},
		paymentKinds:    []PaymentKind{PaymentCreditCard, PaymentDebitCard},
		label:           "na notificação",
	}
	probe.from, probe.to = occurrenceWindow(occurred, *occurred.On, matchWindowDays)
	found, err := a.t.findCandidates(probe, a.loc)
	if err != nil {
		return nil, err
	}
	return append(candidates, found...), nil
}

func (a *interpreter) record(result applied, hash []byte) (InterpretationOutcome, error) {
	details := map[string]any{}
	if a.in.Description != "" {
		details["description"] = a.in.Description
	}
	if a.in.Category != nil {
		details["category"] = *a.in.Category
	}
	if a.in.Purpose != nil {
		details["purpose"] = *a.in.Purpose
	}
	if a.in.Question != "" {
		details["question"] = a.in.Question
	}
	if a.in.Reinterpret {
		details["reason"] = a.in.Reason
	}
	interpretation := Interpretation{
		EventType:      a.in.EventType,
		Amount:         a.in.Amount,
		Merchant:       a.in.Merchant,
		Occurred:       a.occurred(),
		PaymentMethod:  a.in.PaymentMethod,
		Details:        details,
		Author:         AuthorAgent,
		Effect:         &result.effect,
		TransactionID:  result.transactionID,
		submissionHash: hash,
	}
	if _, err := a.t.recordInterpretation(a.in.ObservationID, interpretation); err != nil {
		return InterpretationOutcome{}, err
	}
	if a.in.EventType != EventUnknown {
		if err := a.closeReadingDoubt(); err != nil {
			return InterpretationOutcome{}, err
		}
	}
	return a.outcome(InterpretationApplied, result)
}

func (a *interpreter) outcome(result InterpretationResult, applied applied) (InterpretationOutcome, error) {
	outcome := InterpretationOutcome{
		Result:    result,
		EventType: a.in.EventType,
		Effect:    applied.effect,
		Changed:   applied.changed,
		Conflicts: applied.conflicts,
	}
	if applied.transactionID != nil {
		transaction, err := a.t.transaction(*applied.transactionID)
		if err != nil {
			return InterpretationOutcome{}, err
		}
		current, err := a.t.currentCategory(transaction.ID)
		if err != nil {
			return InterpretationOutcome{}, err
		}
		outcome.Transaction, outcome.Category = &transaction, EffectiveCategory(transaction, current)
		rows, err := a.t.q.OpenTransactionClarifications(a.t.ctx, tenantdb.OpenTransactionClarificationsParams{AccountID: a.t.accountID, TransactionID: &transaction.ID})
		if err != nil {
			return InterpretationOutcome{}, fmt.Errorf("list open clarifications: %w", err)
		}
		for _, row := range rows {
			clarification, err := a.t.withTarget(row)
			if err != nil {
				return InterpretationOutcome{}, err
			}
			outcome.Clarifications = append(outcome.Clarifications, clarification)
		}
	}
	row, err := a.t.q.GetOpenClarificationByDedupKey(a.t.ctx, tenantdb.GetOpenClarificationByDedupKeyParams{AccountID: a.t.accountID, DedupKey: dedupKey(a.readingDoubt(""))})
	switch {
	case err == nil:
		clarification, err := a.t.withTarget(row)
		if err != nil {
			return InterpretationOutcome{}, err
		}
		outcome.Clarifications = append(outcome.Clarifications, clarification)
	case !errors.Is(err, pgx.ErrNoRows):
		return InterpretationOutcome{}, fmt.Errorf("find reading doubt: %w", err)
	}
	return outcome, nil
}

func (a *interpreter) occurred() calendar.Occurrence {
	if a.in.OccurredAt != nil {
		return calendar.Exact(*a.in.OccurredAt, a.loc, calendar.SourceDocument)
	}
	return calendar.Exact(a.version.PostedAt, a.loc, calendar.SourceInferred)
}

func (a *interpreter) originalDescription() string {
	return truncateRunes(a.content.Content(), maxOriginalDescription)
}

func (in NotificationInterpretation) submissionHash() []byte {
	reading := in
	reading.ConfirmDistinct, reading.Reinterpret, reading.Reason = false, false, ""
	if reading.OccurredAt != nil {
		at := reading.OccurredAt.UTC()
		reading.OccurredAt = &at
	}
	encoded, _ := json.Marshal(reading)
	sum := sha256.Sum256(encoded)
	return sum[:]
}

func normalizeInterpretation(in NotificationInterpretation) (NotificationInterpretation, error) {
	if !slices.Contains(eventTypes, in.EventType) {
		return in, &ValidationError{Field: "event_type", Message: fmt.Sprintf("must be one of %v", eventTypes)}
	}
	var err error
	in.Description = strings.TrimSpace(in.Description)
	if len([]rune(in.Description)) > maxShortText {
		return in, &ValidationError{Field: "description", Message: fmt.Sprintf("must have at most %d characters", maxShortText)}
	}
	if in.Merchant, err = optionalText("merchant", in.Merchant, maxShortText); err != nil {
		return in, err
	}
	if in.Purpose, err = optionalText("purpose", in.Purpose, maxShortText); err != nil {
		return in, err
	}
	if in.Category != nil {
		code := strings.TrimSpace(*in.Category)
		in.Category = &code
	}
	in.Question = strings.TrimSpace(in.Question)
	if len([]rune(in.Question)) > maxQuestion {
		return in, &ValidationError{Field: "question", Message: fmt.Sprintf("must have at most %d characters", maxQuestion)}
	}
	if in.Amount != nil {
		if err := validateAmount("amount", *in.Amount); err != nil {
			return in, err
		}
	}
	if in.PaymentMethod != nil {
		if err := validatePaymentMethod(*in.PaymentMethod); err != nil {
			return in, err
		}
	}
	return in, validateEventFields(in)
}

func validateEventFields(in NotificationInterpretation) error {
	needsAmount := in.EventType == EventPurchase || in.EventType == EventRefund
	needsRelated := in.EventType == EventPurchaseUpdate || in.EventType == EventCancellation
	nonExpense := slices.Contains(nonExpenseEvents, in.EventType)
	switch {
	case needsAmount && in.Amount == nil:
		return &ValidationError{Field: "amount", Message: fmt.Sprintf("is required for %s; send unknown when the notification does not show it", in.EventType)}
	case in.EventType == EventPurchase && in.Description == "" && in.Merchant == nil:
		return &ValidationError{Field: "merchant", Message: "or description is required for a purchase"}
	case needsRelated && in.RelatedTransactionID == nil:
		return &ValidationError{Field: "related_transaction_id", Message: fmt.Sprintf("is required for %s", in.EventType)}
	case nonExpense && in.RelatedTransactionID != nil:
		return &ValidationError{Field: "related_transaction_id", Message: fmt.Sprintf("is not accepted for %s, which never changes a transaction", in.EventType)}
	case in.ConfirmDistinct && in.EventType != EventPurchase:
		return &ValidationError{Field: "confirm_distinct", Message: "applies only to purchase"}
	case in.Question != "" && in.EventType != EventUnknown:
		return &ValidationError{Field: "question", Message: "applies only to unknown"}
	}
	return nil
}

func truncateRunes(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit-1]) + "…"
}
