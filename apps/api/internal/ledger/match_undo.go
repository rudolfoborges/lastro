package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const reasonUndoneLink = "restored when a wrong match was undone"

var errNoPurchase = errors.New("the observation does not describe a purchase of its own")

type UndoEffect string

const (
	UndoUnmerged  UndoEffect = "unmerged"
	UndoSeparated UndoEffect = "separated"
	UndoDetached  UndoEffect = "detached"
)

type UndoRequest struct {
	TransactionID uuid.UUID
	ObservationID uuid.UUID
	Reason        string
}

type UndoOutcome struct {
	Effect       UndoEffect
	Transactions []Transaction
}

type undoRecord struct {
	Effect         UndoEffect  `json:"effect"`
	TransactionIDs []uuid.UUID `json:"transaction_ids"`
}

func (s *Service) UndoMatch(ctx context.Context, accountID uuid.UUID, req UndoRequest, actor Actor) (UndoOutcome, error) {
	var outcome UndoOutcome
	err := s.inTx(ctx, accountID, func(t *tx) error {
		record, err := once(t, "undo_match", actor, req, func() (undoRecord, error) {
			return t.undo(req, actor)
		})
		if err != nil {
			return err
		}
		outcome.Effect = record.Effect
		for _, id := range record.TransactionIDs {
			transaction, err := t.transaction(id)
			if err != nil {
				return err
			}
			outcome.Transactions = append(outcome.Transactions, transaction)
		}
		return nil
	})
	return outcome, err
}

func (t *tx) undo(req UndoRequest, actor Actor) (undoRecord, error) {
	reason, err := requiredText("reason", req.Reason, maxReason)
	if err != nil {
		return undoRecord{}, err
	}
	holder, err := t.lockOpenTransaction(req.TransactionID)
	switch {
	case errors.Is(err, ErrNotFound):
		return undoRecord{}, &ValidationError{Field: "transaction_id", Message: "is not a transaction of Rudolfo's account"}
	case errors.Is(err, ErrTransactionClosed):
		return undoRecord{}, &ValidationError{Field: "transaction_id", Message: "is voided or merged; use the transaction that holds the evidence now"}
	case err != nil:
		return undoRecord{}, err
	}
	evidence, err := t.q.LockConfirmedEvidence(t.ctx, tenantdb.LockConfirmedEvidenceParams{AccountID: t.accountID, TransactionID: holder.ID, ObservationID: req.ObservationID})
	if errors.Is(err, pgx.ErrNoRows) {
		return undoRecord{}, &ValidationError{Field: "observation_id", Message: "is not confirmed evidence of this transaction; get_transaction lists its evidence"}
	}
	if err != nil {
		return undoRecord{}, fmt.Errorf("evidence: %w", err)
	}
	merge, err := t.q.ActiveMergeOfObservation(t.ctx, tenantdb.ActiveMergeOfObservationParams{AccountID: t.accountID, SurvivorID: holder.ID, ObservationID: req.ObservationID})
	switch {
	case err == nil:
		return t.unmerge(merge, reason, actor)
	case !errors.Is(err, pgx.ErrNoRows):
		return undoRecord{}, fmt.Errorf("find merge: %w", err)
	}
	return t.separate(holder, evidence, reason, actor)
}

func (t *tx) unmerge(merge tenantdb.TransactionMerge, reason string, actor Actor) (undoRecord, error) {
	merged, err := t.q.LockTransaction(t.ctx, tenantdb.LockTransactionParams{AccountID: t.accountID, ID: merge.MergedID})
	if err != nil {
		return undoRecord{}, fmt.Errorf("merged transaction: %w", err)
	}
	for _, observationID := range merge.ObservationIds {
		evidence, err := t.q.LockConfirmedEvidence(t.ctx, tenantdb.LockConfirmedEvidenceParams{AccountID: t.accountID, TransactionID: merge.SurvivorID, ObservationID: observationID})
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return undoRecord{}, fmt.Errorf("evidence: %w", err)
		}
		if _, err := t.q.SetEvidenceStatus(t.ctx, tenantdb.SetEvidenceStatusParams{AccountID: t.accountID, ID: evidence.ID, Status: string(EvidenceUndone), Origin: string(evidenceOrigin(actor))}); err != nil {
			return undoRecord{}, fmt.Errorf("undo evidence: %w", err)
		}
		if _, err := t.linkEvidence(merged.ID, observationID, EvidenceLink{Status: EvidenceConfirmed, Origin: evidenceOrigin(actor), Reasons: []string{reasonUndoneLink}}); err != nil {
			return undoRecord{}, err
		}
	}
	var changes []fieldChange
	if err := json.Unmarshal(merge.Changes, &changes); err != nil {
		return undoRecord{}, fmt.Errorf("decode merge changes: %w", err)
	}
	if err := t.revertChanges(merge.SurvivorID, changes, reason); err != nil {
		return undoRecord{}, err
	}
	if _, err := t.moveRefunds(merge.SurvivorID, merged.ID, merge.RefundIds); err != nil {
		return undoRecord{}, err
	}
	if _, err := t.q.RestoreMergedTransaction(t.ctx, tenantdb.RestoreMergedTransactionParams{AccountID: t.accountID, ID: merged.ID, Status: merge.MergedStatus}); err != nil {
		return undoRecord{}, fmt.Errorf("restore merged transaction: %w", err)
	}
	if err := t.recordRevision(merged.ID, fieldStatus, jsonValue(StatusMerged), jsonValue(merge.MergedStatus), reason, actor); err != nil {
		return undoRecord{}, err
	}
	if err := t.recordRevision(merge.SurvivorID, fieldMatch, jsonValue(map[string]any{"merged": merged.ID}), jsonValue(nil), reason, actor); err != nil {
		return undoRecord{}, err
	}
	if err := t.q.UndoTransactionMerge(t.ctx, tenantdb.UndoTransactionMergeParams{AccountID: t.accountID, ID: merge.ID, UndoReason: &reason}); err != nil {
		return undoRecord{}, fmt.Errorf("undo merge: %w", err)
	}
	if err := t.rejectPair(merge.SurvivorID, merged.ID, reason, actor); err != nil {
		return undoRecord{}, err
	}
	ids := []uuid.UUID{merge.SurvivorID, merged.ID}
	return undoRecord{Effect: UndoUnmerged, TransactionIDs: ids}, t.afterUndo(ids)
}

func (t *tx) separate(holder Transaction, evidence tenantdb.TransactionEvidence, reason string, actor Actor) (undoRecord, error) {
	confirmed, err := t.q.CountConfirmedEvidence(t.ctx, tenantdb.CountConfirmedEvidenceParams{AccountID: t.accountID, TransactionID: holder.ID})
	if err != nil {
		return undoRecord{}, fmt.Errorf("count evidence: %w", err)
	}
	creator, err := t.creatorOf(holder.ID)
	if err != nil {
		return undoRecord{}, err
	}
	if confirmed < 2 || (creator != nil && *creator == evidence.ObservationID) {
		return undoRecord{}, &ValidationError{Field: "observation_id", Message: "created this transaction; undo the record that was linked to it later, or void the transaction if it was a mistake"}
	}
	if _, err := t.q.SetEvidenceStatus(t.ctx, tenantdb.SetEvidenceStatusParams{AccountID: t.accountID, ID: evidence.ID, Status: string(EvidenceUndone), Origin: string(evidenceOrigin(actor))}); err != nil {
		return undoRecord{}, fmt.Errorf("undo evidence: %w", err)
	}
	revisions, err := t.q.ObservationRevisions(t.ctx, tenantdb.ObservationRevisionsParams{AccountID: t.accountID, EntityID: holder.ID, ObservationID: &evidence.ObservationID})
	if err != nil {
		return undoRecord{}, fmt.Errorf("list revisions: %w", err)
	}
	changes := make([]fieldChange, 0, len(revisions))
	for _, revision := range revisions {
		if revision.Field != fieldCreated && revision.Field != fieldStatus && revision.Field != fieldClarification && revision.Field != fieldMatch {
			changes = append(changes, fieldChange{Field: revision.Field, Before: revision.OldValue, After: revision.NewValue})
		}
	}
	if err := t.revertChanges(holder.ID, changes, reason); err != nil {
		return undoRecord{}, err
	}
	ids := []uuid.UUID{holder.ID}
	separated, err := t.recreate(evidence.ObservationID, actor)
	switch {
	case errors.Is(err, errNoPurchase):
		return undoRecord{Effect: UndoDetached, TransactionIDs: ids}, t.afterUndo(ids)
	case err != nil:
		return undoRecord{}, err
	}
	if err := t.rejectPair(holder.ID, separated.ID, reason, actor); err != nil {
		return undoRecord{}, err
	}
	ids = append(ids, separated.ID)
	return undoRecord{Effect: UndoSeparated, TransactionIDs: ids}, t.afterUndo(ids)
}

func (t *tx) afterUndo(ids []uuid.UUID) error {
	for _, id := range ids {
		if err := t.settleStatus(id); err != nil {
			return err
		}
		if err := t.refreshClarifications(id); err != nil {
			return err
		}
	}
	return nil
}

func (t *tx) creatorOf(transactionID uuid.UUID) (*uuid.UUID, error) {
	history, err := t.history(transactionID)
	if err != nil {
		return nil, err
	}
	for _, revision := range history {
		if revision.Field == fieldCreated {
			return revision.ObservationID, nil
		}
	}
	return nil, nil
}

func (t *tx) recreate(observationID uuid.UUID, actor Actor) (Transaction, error) {
	row, err := t.q.GetObservation(t.ctx, tenantdb.GetObservationParams{AccountID: t.accountID, ID: observationID})
	if err != nil {
		return Transaction{}, fmt.Errorf("observation %s: %w", observationID, notFound(err))
	}
	var rebuilt NewTransaction
	var category *string
	switch Source(row.Source) {
	case SourceStatementLine:
		rebuilt, category, err = statementLineTransaction(row.Payload)
	default:
		rebuilt, category, err = t.interpretedTransaction(row)
	}
	if err != nil {
		return Transaction{}, err
	}
	created, err := t.createTransaction(observationID, rebuilt, Actor{Author: AuthorSystem, ObservationID: &observationID})
	if err != nil {
		return Transaction{}, err
	}
	if rebuilt.AmountBasis == AmountInstallment {
		if err := t.replanLine(created, observationID, row.Payload); err != nil {
			return Transaction{}, err
		}
	}
	if category != nil {
		if _, err := t.categorize(created, *category, AllocationSuggested, reasonClassified, Actor{Author: decisionAuthor(actor), Ref: actor.Ref}); err != nil {
			return Transaction{}, err
		}
	}
	return created, nil
}

type storedLine struct {
	Kind                StatementItemKind `json:"kind"`
	OriginalDescription string            `json:"original_description"`
	Description         string            `json:"description"`
	Merchant            *string           `json:"merchant"`
	Amount              string            `json:"amount"`
	Currency            string            `json:"currency"`
	OriginalAmount      string            `json:"original_amount"`
	OriginalCurrency    string            `json:"original_currency"`
	Date                string            `json:"date"`
	CardLast4           string            `json:"card_last4"`
	InstallmentNumber   int               `json:"installment_number"`
	InstallmentCount    int               `json:"installment_count"`
	Category            *string           `json:"category"`
	Purpose             *string           `json:"purpose"`
}

func statementLineTransaction(payload []byte) (NewTransaction, *string, error) {
	var line storedLine
	if err := json.Unmarshal(payload, &line); err != nil {
		return NewTransaction{}, nil, fmt.Errorf("decode statement line: %w", err)
	}
	nature, ok := itemNatures[line.Kind]
	if !ok || (line.Kind.installment() && line.InstallmentCount < minInstallments) {
		return NewTransaction{}, nil, errNoPurchase
	}
	amount, err := money.Parse(line.Amount, line.Currency)
	if err != nil {
		return NewTransaction{}, nil, fmt.Errorf("decode statement line amount: %w", err)
	}
	description := line.Description
	if description == "" {
		description = line.OriginalDescription
	}
	rebuilt := NewTransaction{
		OriginalDescription: line.OriginalDescription,
		Description:         description,
		Merchant:            line.Merchant,
		Amount:              amount,
		Occurred:            calendar.Unknown(),
		PaymentMethod:       &PaymentMethodRef{Kind: PaymentCreditCard, CardLast4: &line.CardLast4},
		Nature:              nature,
		Status:              StatusConfirmed,
		Purpose:             line.Purpose,
	}
	if line.Kind.installment() {
		rebuilt.AmountBasis = AmountInstallment
		rebuilt.Installments = &Installments{Count: line.InstallmentCount, Amount: &amount}
	}
	if line.OriginalAmount != "" {
		original, err := money.Parse(line.OriginalAmount, line.OriginalCurrency)
		if err != nil {
			return NewTransaction{}, nil, fmt.Errorf("decode statement line original amount: %w", err)
		}
		rebuilt.OriginalAmount = &original
	}
	if line.Date != "" {
		day, err := calendar.ParseDate(line.Date)
		if err != nil {
			return NewTransaction{}, nil, fmt.Errorf("decode statement line date: %w", err)
		}
		rebuilt.Occurred = calendar.OnDay(day, calendar.SourceDocument)
	}
	return rebuilt, line.Category, nil
}

func (t *tx) replanLine(transaction Transaction, observationID uuid.UUID, payload []byte) error {
	var line storedLine
	if err := json.Unmarshal(payload, &line); err != nil {
		return fmt.Errorf("decode statement line: %w", err)
	}
	charged, err := t.q.InstallmentByObservation(t.ctx, tenantdb.InstallmentByObservationParams{AccountID: t.accountID, ObservationID: &observationID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("charged installment: %w", err)
	}
	modality := ModalityCardInstallment
	if line.Kind == ItemPixInstallment {
		modality = ModalityPixInstallment
	}
	key := chainKey(StatementItem{OriginalDescription: line.OriginalDescription, Installment: &InstallmentPosition{Number: line.InstallmentNumber, Count: line.InstallmentCount}})
	plan, err := t.q.InsertInstallmentPlan(t.ctx, tenantdb.InsertInstallmentPlanParams{
		ID:                     store.NewID(),
		AccountID:              t.accountID,
		TransactionID:          transaction.ID,
		PaymentModality:        string(modality),
		InstallmentCount:       int32(line.InstallmentCount), //nolint:gosec // bounded by validation
		Currency:               transaction.Amount.Currency,
		InstallmentAmountMinor: &charged.AmountMinor,
		CardLast4:              &line.CardLast4,
		ChainKey:               &key,
		InformedOn:             charged.InformedOn,
	})
	if err != nil {
		return fmt.Errorf("insert installment plan: %w", err)
	}
	return t.q.MoveInstallmentByObservation(t.ctx, tenantdb.MoveInstallmentByObservationParams{AccountID: t.accountID, ObservationID: &observationID, ToPlanID: plan.ID})
}

func (t *tx) interpretedTransaction(observation tenantdb.Observation) (NewTransaction, *string, error) {
	rows, err := t.q.LatestInterpretations(t.ctx, tenantdb.LatestInterpretationsParams{AccountID: t.accountID, ObservationIds: []uuid.UUID{observation.ID}})
	if err != nil {
		return NewTransaction{}, nil, fmt.Errorf("latest interpretation: %w", err)
	}
	if len(rows) == 0 {
		return NewTransaction{}, nil, errNoPurchase
	}
	reading, err := t.toInterpretation(rows[0])
	if err != nil {
		return NewTransaction{}, nil, err
	}
	if (reading.EventType != EventPurchase && reading.EventType != EventPurchaseUpdate) || reading.Amount == nil {
		return NewTransaction{}, nil, errNoPurchase
	}
	original, err := t.originalText(observation)
	if err != nil {
		return NewTransaction{}, nil, err
	}
	description, _ := reading.Details["description"].(string)
	if description == "" && reading.Merchant != nil {
		description = *reading.Merchant
	}
	if description == "" {
		description = truncateRunes(original, maxShortText)
	}
	rebuilt := NewTransaction{
		OriginalDescription: original,
		Description:         description,
		Merchant:            reading.Merchant,
		Amount:              *reading.Amount,
		Occurred:            reading.Occurred,
		PaymentMethod:       reading.PaymentMethod,
		Nature:              NatureExpense,
		Status:              InitialStatus(reading.PaymentMethod),
	}
	if purpose, ok := reading.Details["purpose"].(string); ok {
		rebuilt.Purpose = &purpose
	}
	var category *string
	if code, ok := reading.Details["category"].(string); ok {
		category = &code
	}
	return rebuilt, category, nil
}

func (t *tx) originalText(observation tenantdb.Observation) (string, error) {
	if Source(observation.Source) != SourceWalletNotification {
		var payload struct {
			RawText string `json:"raw_text"`
		}
		if err := json.Unmarshal(observation.Payload, &payload); err != nil {
			return "", fmt.Errorf("decode observation: %w", err)
		}
		return payload.RawText, nil
	}
	version, err := t.q.LockNotificationVersion(t.ctx, tenantdb.LockNotificationVersionParams{AccountID: t.accountID, ObservationID: observation.ID})
	if err != nil {
		return "", fmt.Errorf("notification version: %w", err)
	}
	content, err := notificationVersion(version.ObservationID, version.NotificationID, version.Version, version.Event, version.PostedAt, version.ReceivedAt, version.Payload)
	if err != nil {
		return "", err
	}
	return truncateRunes(content.Content(), maxOriginalDescription), nil
}

func (t *tx) revertChanges(transactionID uuid.UUID, changes []fieldChange, reason string) error {
	current, err := t.transaction(transactionID)
	if err != nil {
		return err
	}
	locked, err := t.lockedFields(transactionID)
	if err != nil {
		return err
	}
	first, last := map[string]fieldChange{}, map[string]fieldChange{}
	var order []string
	for _, change := range changes {
		if _, seen := first[change.Field]; !seen {
			first[change.Field] = change
			order = append(order, change.Field)
		}
		last[change.Field] = change
	}
	var patch Patch
	for _, field := range order {
		before, after := first[field].Before, last[field].After
		if field == fieldCategory {
			if err := t.revertCategory(current, before, after, reason); err != nil {
				return err
			}
			continue
		}
		if field == fieldPlan {
			if err := t.revertPlan(before, after); err != nil {
				return err
			}
			continue
		}
		if locked[Field(field)] || !sameJSON(fieldValue(current, Field(field)), after) {
			continue
		}
		if err := patchField(&patch, Field(field), before); err != nil {
			return err
		}
	}
	if emptyPatch(patch) {
		return nil
	}
	_, err = t.correct(transactionID, patch, reason, Actor{Author: AuthorSystem})
	return err
}

func (t *tx) revertCategory(transaction Transaction, before, after json.RawMessage, reason string) error {
	current, err := t.currentCategory(transaction.ID)
	if err != nil {
		return err
	}
	if !sameJSON(allocationJSON(current), allocationJSON(decodeAllocation(after))) {
		return nil
	}
	return t.restoreCategory(transaction, decodeAllocation(before), reason)
}

func (t *tx) revertPlan(before, after json.RawMessage) error {
	var was, now struct {
		PlanID                 uuid.UUID  `json:"plan_id"`
		TransactionID          uuid.UUID  `json:"transaction_id"`
		TotalMinor             *int64     `json:"total_minor"`
		InstallmentAmountMinor *int64     `json:"installment_amount_minor"`
		InstallmentAmountKnown bool       `json:"installment_amount_known"`
		Canceled               *uuid.UUID `json:"canceled"`
	}
	if err := json.Unmarshal(before, &was); err != nil {
		return fmt.Errorf("decode plan before the merge: %w", err)
	}
	if err := json.Unmarshal(after, &now); err != nil {
		return fmt.Errorf("decode plan after the merge: %w", err)
	}
	current, err := t.q.GetInstallmentPlan(t.ctx, tenantdb.GetInstallmentPlanParams{AccountID: t.accountID, ID: was.PlanID})
	if err != nil {
		return fmt.Errorf("installment plan: %w", err)
	}
	params := planUpdate(current)
	params.TransactionID = was.TransactionID
	if was.Canceled != nil && sameJSON(jsonValue(planSnapshot(current)), after) {
		params.TotalMinor, params.InstallmentAmountMinor, params.InstallmentAmountKnown = was.TotalMinor, was.InstallmentAmountMinor, was.InstallmentAmountKnown
	}
	if current.TransactionID != now.TransactionID {
		return nil
	}
	if _, err := t.q.UpdateInstallmentPlan(t.ctx, params); err != nil {
		return fmt.Errorf("restore installment plan: %w", err)
	}
	if was.Canceled == nil {
		return nil
	}
	canceled, err := t.q.GetInstallmentPlan(t.ctx, tenantdb.GetInstallmentPlanParams{AccountID: t.accountID, ID: *was.Canceled})
	if err != nil {
		return fmt.Errorf("canceled installment plan: %w", err)
	}
	restored := planUpdate(canceled)
	restored.CanceledAt = nil
	if _, err := t.q.UpdateInstallmentPlan(t.ctx, restored); err != nil {
		return fmt.Errorf("restore canceled installment plan: %w", err)
	}
	return nil
}

func decodeAllocation(raw json.RawMessage) *Allocation {
	var value struct {
		Category string           `json:"category"`
		Status   AllocationStatus `json:"status"`
		Origin   Author           `json:"origin"`
	}
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &value) != nil || value.Category == "" {
		return nil
	}
	if value.Origin == "" {
		value.Origin = AuthorAgent
		if value.Status == AllocationConfirmed {
			value.Origin = AuthorUser
		}
	}
	return &Allocation{Code: value.Category, Status: value.Status, Origin: value.Origin}
}

func patchField(patch *Patch, field Field, raw json.RawMessage) error {
	null := len(raw) == 0 || string(raw) == "null"
	var err error
	switch field {
	case FieldDescription:
		var value string
		if err = json.Unmarshal(raw, &value); err == nil {
			patch.Description = &value
		}
	case FieldMerchant, FieldPayee, FieldPurpose:
		value := Cleared[string]()
		if !null {
			var text string
			if err = json.Unmarshal(raw, &text); err == nil {
				value = Value(text)
			}
		}
		switch field {
		case FieldMerchant:
			patch.Merchant = value
		case FieldPayee:
			patch.Payee = value
		default:
			patch.Purpose = value
		}
	case FieldAmount:
		var amount moneyJSON
		if err = json.Unmarshal(raw, &amount); err == nil {
			var parsed money.Money
			if parsed, err = money.Parse(amount.Amount, amount.Currency); err == nil {
				basis := AmountTotal
				if amount.Basis == AmountInstallment {
					basis = AmountInstallment
				}
				patch.Amount, patch.AmountBasis = &parsed, &basis
			}
		}
	case FieldOccurred:
		var occurred calendar.Occurrence
		if occurred, err = decodeOccurrence(raw); err == nil {
			patch.Occurred = &occurred
		}
	case FieldPaymentMethod:
		patch.PaymentMethod = Cleared[PaymentMethodRef]()
		if !null {
			var ref PaymentMethodRef
			var value struct {
				Kind      PaymentKind `json:"kind"`
				CardLast4 *string     `json:"card_last4"`
			}
			if err = json.Unmarshal(raw, &value); err == nil {
				ref = PaymentMethodRef{Kind: value.Kind, CardLast4: value.CardLast4}
				patch.PaymentMethod = Value(ref)
			}
		}
	case FieldNature:
		var value Nature
		if err = json.Unmarshal(raw, &value); err == nil {
			patch.Nature = &value
		}
	case FieldInstallments:
		patch.Installments = Cleared[Installments]()
		if !null {
			var value struct {
				Count  int        `json:"count"`
				Amount *moneyJSON `json:"amount"`
			}
			if err = json.Unmarshal(raw, &value); err == nil {
				installments := Installments{Count: value.Count}
				if value.Amount != nil {
					var amount money.Money
					if amount, err = money.Parse(value.Amount.Amount, value.Amount.Currency); err == nil {
						installments.Amount = &amount
					}
				}
				patch.Installments = Value(installments)
			}
		}
	}
	if err != nil {
		return fmt.Errorf("decode previous %s: %w", field, err)
	}
	return nil
}

type moneyJSON struct {
	Amount   string      `json:"amount"`
	Currency string      `json:"currency"`
	Basis    AmountBasis `json:"basis,omitempty"`
}

func decodeOccurrence(raw json.RawMessage) (calendar.Occurrence, error) {
	var value struct {
		Precision calendar.Precision `json:"precision"`
		Source    calendar.Source    `json:"source"`
		At        *string            `json:"at"`
		On        *string            `json:"on"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return calendar.Occurrence{}, err
	}
	occurrence := calendar.Occurrence{Precision: value.Precision, Source: value.Source}
	if value.At != nil {
		at, err := time.Parse(time.RFC3339, *value.At)
		if err != nil {
			return calendar.Occurrence{}, err
		}
		occurrence.At = &at
	}
	if value.On != nil {
		on, err := calendar.ParseDate(*value.On)
		if err != nil {
			return calendar.Occurrence{}, err
		}
		occurrence.On = &on
	}
	return occurrence, nil
}

func emptyPatch(p Patch) bool {
	return p.Description == nil && !p.Merchant.Set && !p.Payee.Set && p.Amount == nil && p.AmountBasis == nil && p.Occurred == nil &&
		!p.PaymentMethod.Set && p.Nature == nil && !p.Purpose.Set && !p.Installments.Set
}

func sameJSON(a, b json.RawMessage) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return reflect.DeepEqual(x, y)
}
