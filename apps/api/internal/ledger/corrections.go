package ledger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	maxReason            = 500
	entityTransaction    = "transaction"
	fieldCreated         = "created"
	fieldStatus          = "status"
	statusFollowsPayment = "Situação ajustada ao meio de pagamento."
)

var creationReasons = map[Source]string{
	SourceConversation:       "Registrado a partir da conversa.",
	SourceWalletNotification: "Registrado a partir de uma notificação.",
	SourceStatementLine:      "Registrado a partir da fatura.",
	SourceManual:             "Registrado manualmente.",
	SourceImport:             "Registrado por importação.",
}

var ErrTransactionClosed = errors.New("the transaction is voided or merged and can no longer change")

type Actor struct {
	Author        Author
	Ref           string
	ObservationID *uuid.UUID
	RequestID     string
}

type Optional[T any] struct {
	Set   bool
	Value *T
}

func Value[T any](v T) Optional[T] {
	return Optional[T]{Set: true, Value: &v}
}

func Cleared[T any]() Optional[T] {
	return Optional[T]{Set: true}
}

type Patch struct {
	Description   *string
	Merchant      Optional[string]
	Payee         Optional[string]
	Amount        *money.Money
	Occurred      *calendar.Occurrence
	PaymentMethod Optional[PaymentMethodRef]
	Nature        *Nature
	Purpose       Optional[string]
	Installments  Optional[Installments]
	Categories    []Split
}

type FieldConflict struct {
	Field    Field
	Current  json.RawMessage
	Proposed json.RawMessage
}

type Correction struct {
	Transaction Transaction
	Changed     []Field
	Conflicts   []FieldConflict
	Categories  []Allocation
}

type Revision struct {
	ID            uuid.UUID
	EntityID      uuid.UUID
	Field         string
	OldValue      json.RawMessage
	NewValue      json.RawMessage
	Reason        string
	Author        Author
	ActorRef      *string
	ObservationID *uuid.UUID
	CreatedAt     time.Time
}

func (s *Service) Correct(ctx context.Context, accountID, transactionID uuid.UUID, patch Patch, reason string, actor Actor) (Correction, error) {
	var correction Correction
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		correction, err = once(t, "update_transaction", actor, []any{transactionID, patch, reason}, func() (Correction, error) {
			return t.correct(transactionID, patch, reason, actor)
		})
		return err
	})
	return correction, err
}

func (s *Service) Void(ctx context.Context, accountID, transactionID uuid.UUID, reason string, actor Actor) (Transaction, error) {
	var voided Transaction
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		voided, err = once(t, "void_transaction", actor, []any{transactionID, reason}, func() (Transaction, error) {
			return t.void(transactionID, reason, actor)
		})
		return err
	})
	return voided, err
}

func (s *Service) History(ctx context.Context, accountID, transactionID uuid.UUID) ([]Revision, error) {
	var history []Revision
	err := s.inTx(ctx, accountID, func(t *tx) error {
		if _, err := t.transaction(transactionID); err != nil {
			return err
		}
		var err error
		history, err = t.history(transactionID)
		return err
	})
	return history, err
}

func InitialStatus(method *PaymentMethodRef) Status {
	if method == nil || method.Kind == PaymentCreditCard {
		return StatusProvisional
	}
	return StatusConfirmed
}

func (t *tx) correct(transactionID uuid.UUID, patch Patch, reason string, actor Actor) (Correction, error) {
	correction, err := t.applyCorrection(transactionID, patch, reason, actor)
	if err != nil {
		return Correction{}, err
	}
	if actor.Author == AuthorUser {
		if err := t.closeConflictsOn(transactionID, correction.Changed); err != nil {
			return Correction{}, err
		}
	}
	if err := t.openConflicts(correction.Transaction, correction.Conflicts, actor); err != nil {
		return Correction{}, err
	}
	if err := t.refreshClarifications(transactionID); err != nil {
		return Correction{}, err
	}
	return correction, nil
}

func (t *tx) applyCorrection(transactionID uuid.UUID, patch Patch, reason string, actor Actor) (Correction, error) {
	reason, err := requiredText("reason", reason, maxReason)
	if err != nil {
		return Correction{}, err
	}
	current, err := t.lockOpenTransaction(transactionID)
	if err != nil {
		return Correction{}, err
	}
	locked, err := t.lockedFields(transactionID)
	if err != nil {
		return Correction{}, err
	}
	proposed, err := applyPatch(current, patch)
	if err != nil {
		return Correction{}, err
	}

	next := current
	var correction Correction
	for _, field := range []Field{FieldDescription, FieldMerchant, FieldPayee, FieldAmount, FieldOccurred, FieldPaymentMethod, FieldNature, FieldPurpose, FieldInstallments} {
		before, after := fieldValue(current, field), fieldValue(proposed, field)
		if bytes.Equal(before, after) {
			continue
		}
		if actor.Author != AuthorUser && locked[field] {
			correction.Conflicts = append(correction.Conflicts, FieldConflict{Field: field, Current: before, Proposed: after})
			continue
		}
		copyField(&next, proposed, field)
		correction.Changed = append(correction.Changed, field)
	}
	if len(correction.Changed) == 0 {
		correction.Transaction = current
		return t.withCategories(correction, patch.Categories, reason, actor)
	}
	if _, err := normalizeNewTransaction(asNewTransaction(next)); err != nil {
		return Correction{}, err
	}

	if next.PaymentMethod != nil && slices.Contains(correction.Changed, FieldPaymentMethod) {
		method, err := t.ensurePaymentMethod(PaymentMethodRef{Kind: next.PaymentMethod.Kind, CardLast4: next.PaymentMethod.CardLast4})
		if err != nil {
			return Correction{}, err
		}
		next.PaymentMethod = &method
	}
	statusChanged := false
	if slices.Contains(correction.Changed, FieldPaymentMethod) {
		status, err := t.statusFollowingPayment(next)
		if err != nil {
			return Correction{}, err
		}
		statusChanged = status != next.Status
		next.Status = status
	}

	saved, err := t.saveTransaction(next)
	if err != nil {
		return Correction{}, err
	}
	for _, field := range correction.Changed {
		if err := t.recordRevision(transactionID, string(field), fieldValue(current, field), fieldValue(saved, field), reason, actor); err != nil {
			return Correction{}, err
		}
		source := FieldSource{Field: field, ObservationID: actor.ObservationID, ConfirmedByUser: actor.Author == AuthorUser}
		if err := t.setFieldSource(transactionID, source); err != nil {
			return Correction{}, err
		}
	}
	if statusChanged {
		if err := t.recordStatusRevision(transactionID, current.Status, saved.Status, statusFollowsPayment); err != nil {
			return Correction{}, err
		}
	}
	correction.Transaction = saved
	if slices.Contains(correction.Changed, FieldAmount) {
		if err := t.followAmount(saved, patch.Categories, reason, actor); err != nil {
			return Correction{}, err
		}
		return t.withCategories(correction, nil, reason, actor)
	}
	return t.withCategories(correction, patch.Categories, reason, actor)
}

func (t *tx) withCategories(correction Correction, split []Split, reason string, actor Actor) (Correction, error) {
	if split != nil {
		result, err := t.categorize(correction.Transaction, split, statusChosenBy(actor), reason, actor)
		if err != nil {
			return Correction{}, err
		}
		if result.Conflict != nil {
			correction.Conflicts = append(correction.Conflicts, *result.Conflict)
		}
	}
	current, err := t.allocations(correction.Transaction.ID)
	if err != nil {
		return Correction{}, err
	}
	correction.Categories = EffectiveAllocations(correction.Transaction, current)
	return correction, nil
}

func statusChosenBy(actor Actor) AllocationStatus {
	if actor.Author == AuthorUser {
		return AllocationConfirmed
	}
	return AllocationSuggested
}

func (t *tx) void(transactionID uuid.UUID, reason string, actor Actor) (Transaction, error) {
	reason, err := requiredText("reason", reason, maxReason)
	if err != nil {
		return Transaction{}, err
	}
	row, err := t.q.LockTransaction(t.ctx, tenantdb.LockTransactionParams{AccountID: t.accountID, ID: transactionID})
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction %s: %w", transactionID, notFound(err))
	}
	current, err := t.withPaymentMethod(row)
	if err != nil {
		return Transaction{}, err
	}
	switch current.Status {
	case StatusVoided:
		return current, nil
	case StatusMerged:
		return Transaction{}, ErrTransactionClosed
	}
	next := current
	next.Status = StatusVoided
	saved, err := t.saveTransaction(next)
	if err != nil {
		return Transaction{}, err
	}
	if err := t.recordRevision(transactionID, fieldStatus, jsonValue(current.Status), jsonValue(saved.Status), reason, actor); err != nil {
		return Transaction{}, err
	}
	if err := t.refreshClarifications(transactionID); err != nil {
		return Transaction{}, err
	}
	return saved, nil
}

func (t *tx) lockOpenTransaction(transactionID uuid.UUID) (Transaction, error) {
	row, err := t.q.LockTransaction(t.ctx, tenantdb.LockTransactionParams{AccountID: t.accountID, ID: transactionID})
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction %s: %w", transactionID, notFound(err))
	}
	if Status(row.Status) == StatusVoided || Status(row.Status) == StatusMerged {
		return Transaction{}, ErrTransactionClosed
	}
	return t.withPaymentMethod(row)
}

func (t *tx) lockedFields(transactionID uuid.UUID) (map[Field]bool, error) {
	sources, err := t.fieldSources(transactionID)
	if err != nil {
		return nil, err
	}
	locked := map[Field]bool{}
	for _, source := range sources {
		locked[source.Field] = source.ConfirmedByUser
	}
	return locked, nil
}

func (t *tx) statusFollowingPayment(next Transaction) (Status, error) {
	if next.Status != StatusProvisional && next.Status != StatusConfirmed {
		return next.Status, nil
	}
	charged, err := t.q.HasConfirmedEvidenceFrom(t.ctx, tenantdb.HasConfirmedEvidenceFromParams{
		AccountID:     t.accountID,
		TransactionID: next.ID,
		Source:        string(SourceStatementLine),
	})
	if err != nil {
		return "", fmt.Errorf("check statement evidence: %w", err)
	}
	if charged {
		return StatusConfirmed, nil
	}
	var ref *PaymentMethodRef
	if next.PaymentMethod != nil {
		ref = &PaymentMethodRef{Kind: next.PaymentMethod.Kind, CardLast4: next.PaymentMethod.CardLast4}
	}
	return InitialStatus(ref), nil
}

func (t *tx) saveTransaction(next Transaction) (Transaction, error) {
	params := tenantdb.UpdateTransactionParams{
		AccountID:     t.accountID,
		ID:            next.ID,
		Description:   next.Description,
		Merchant:      next.Merchant,
		Payee:         next.Payee,
		AmountMinor:   next.Amount.Minor,
		Currency:      next.Amount.Currency,
		OccurredAt:    next.Occurred.At,
		OccurredOn:    dateTime(next.Occurred.On),
		DatePrecision: string(next.Occurred.Precision),
		DateSource:    string(next.Occurred.Source),
		Nature:        string(next.Nature),
		Status:        string(next.Status),
		Purpose:       next.Purpose,
	}
	if next.PaymentMethod != nil {
		params.PaymentMethodID = &next.PaymentMethod.ID
	}
	if next.OriginalAmount != nil {
		params.OriginalAmountMinor = &next.OriginalAmount.Minor
		params.OriginalCurrency = &next.OriginalAmount.Currency
	}
	if next.Installments != nil {
		count := int32(next.Installments.Count) //nolint:gosec // bounded by validation
		params.InstallmentCount = &count
		if next.Installments.Amount != nil {
			params.InstallmentAmountMinor = &next.Installments.Amount.Minor
		}
	}
	row, err := t.q.UpdateTransaction(t.ctx, params)
	if err != nil {
		return Transaction{}, fmt.Errorf("update transaction: %w", err)
	}
	return toTransaction(row, next.PaymentMethod), nil
}

func (t *tx) recordRevision(entityID uuid.UUID, field string, oldValue, newValue json.RawMessage, reason string, actor Actor) error {
	params := tenantdb.InsertRevisionParams{
		ID:            store.NewID(),
		AccountID:     t.accountID,
		EntityType:    entityTransaction,
		EntityID:      entityID,
		Field:         field,
		OldValue:      oldValue,
		NewValue:      newValue,
		Reason:        reason,
		Author:        string(actor.Author),
		ObservationID: actor.ObservationID,
	}
	if actor.Ref != "" {
		params.ActorRef = &actor.Ref
	}
	if err := t.q.InsertRevision(t.ctx, params); err != nil {
		return fmt.Errorf("record revision of %s: %w", field, err)
	}
	return nil
}

func (t *tx) recordStatusRevision(transactionID uuid.UUID, before, after Status, reason string) error {
	return t.recordRevision(transactionID, fieldStatus, jsonValue(before), jsonValue(after), reason, Actor{Author: AuthorSystem})
}

func (t *tx) history(transactionID uuid.UUID) ([]Revision, error) {
	rows, err := t.q.ListRevisions(t.ctx, tenantdb.ListRevisionsParams{AccountID: t.accountID, EntityType: entityTransaction, EntityID: transactionID})
	if err != nil {
		return nil, fmt.Errorf("list revisions: %w", err)
	}
	history := make([]Revision, len(rows))
	for i, row := range rows {
		history[i] = Revision{
			ID:            row.ID,
			EntityID:      row.EntityID,
			Field:         row.Field,
			OldValue:      row.OldValue,
			NewValue:      row.NewValue,
			Reason:        row.Reason,
			Author:        Author(row.Author),
			ActorRef:      row.ActorRef,
			ObservationID: row.ObservationID,
			CreatedAt:     row.CreatedAt,
		}
	}
	return history, nil
}

func applyPatch(current Transaction, patch Patch) (Transaction, error) {
	next := current
	if patch.Description != nil {
		next.Description = *patch.Description
	}
	if patch.Merchant.Set {
		next.Merchant = patch.Merchant.Value
	}
	if patch.Payee.Set {
		next.Payee = patch.Payee.Value
	}
	if patch.Amount != nil {
		next.Amount = *patch.Amount
		if next.Installments != nil && next.Installments.Amount != nil && next.Installments.Amount.Currency != next.Amount.Currency {
			return Transaction{}, &ValidationError{Field: "amount", Message: "must keep the currency of the installments"}
		}
	}
	if patch.Occurred != nil {
		next.Occurred = *patch.Occurred
	}
	if patch.PaymentMethod.Set {
		next.PaymentMethod = nil
		if ref := patch.PaymentMethod.Value; ref != nil {
			if err := validatePaymentMethod(*ref); err != nil {
				return Transaction{}, err
			}
			next.PaymentMethod = &PaymentMethod{Kind: ref.Kind, CardLast4: ref.CardLast4}
		}
	}
	if patch.Nature != nil {
		next.Nature = *patch.Nature
	}
	if patch.Purpose.Set {
		next.Purpose = patch.Purpose.Value
	}
	if patch.Installments.Set {
		next.Installments = patch.Installments.Value
	}
	normalized, err := normalizeNewTransaction(asNewTransaction(next))
	if err != nil {
		return Transaction{}, err
	}
	next.Description, next.Merchant, next.Payee, next.Purpose = normalized.Description, normalized.Merchant, normalized.Payee, normalized.Purpose
	return next, nil
}

func asNewTransaction(tr Transaction) NewTransaction {
	in := NewTransaction{
		OriginalDescription: tr.OriginalDescription,
		Description:         tr.Description,
		Merchant:            tr.Merchant,
		Payee:               tr.Payee,
		Amount:              tr.Amount,
		OriginalAmount:      tr.OriginalAmount,
		Occurred:            tr.Occurred,
		Nature:              tr.Nature,
		Status:              tr.Status,
		Purpose:             tr.Purpose,
		Installments:        tr.Installments,
	}
	if tr.PaymentMethod != nil {
		in.PaymentMethod = &PaymentMethodRef{Kind: tr.PaymentMethod.Kind, CardLast4: tr.PaymentMethod.CardLast4}
	}
	return in
}

func copyField(dst *Transaction, src Transaction, field Field) {
	switch field {
	case FieldDescription:
		dst.Description = src.Description
	case FieldMerchant:
		dst.Merchant = src.Merchant
	case FieldPayee:
		dst.Payee = src.Payee
	case FieldAmount:
		dst.Amount = src.Amount
	case FieldOccurred:
		dst.Occurred = src.Occurred
	case FieldPaymentMethod:
		dst.PaymentMethod = src.PaymentMethod
	case FieldNature:
		dst.Nature = src.Nature
	case FieldPurpose:
		dst.Purpose = src.Purpose
	case FieldInstallments:
		dst.Installments = src.Installments
	}
}

func fieldValue(tr Transaction, field Field) json.RawMessage {
	switch field {
	case FieldDescription:
		return jsonValue(tr.Description)
	case FieldMerchant:
		return jsonValue(tr.Merchant)
	case FieldPayee:
		return jsonValue(tr.Payee)
	case FieldAmount:
		return jsonValue(amountJSON(tr.Amount))
	case FieldOccurred:
		return jsonValue(occurrenceJSON(tr.Occurred))
	case FieldPaymentMethod:
		if tr.PaymentMethod == nil {
			return jsonValue(nil)
		}
		return jsonValue(map[string]any{"kind": tr.PaymentMethod.Kind, "card_last4": tr.PaymentMethod.CardLast4})
	case FieldNature:
		return jsonValue(tr.Nature)
	case FieldPurpose:
		return jsonValue(tr.Purpose)
	case FieldInstallments:
		if tr.Installments == nil {
			return jsonValue(nil)
		}
		value := map[string]any{"count": tr.Installments.Count}
		if tr.Installments.Amount != nil {
			value["amount"] = amountJSON(*tr.Installments.Amount)
		}
		return jsonValue(value)
	}
	return jsonValue(nil)
}

func snapshot(tr Transaction) json.RawMessage {
	return jsonValue(map[string]any{
		"description": tr.Description,
		"amount":      amountJSON(tr.Amount),
		"occurred":    occurrenceJSON(tr.Occurred),
		"nature":      tr.Nature,
		"status":      tr.Status,
	})
}

func amountJSON(m money.Money) map[string]string {
	return map[string]string{"amount": m.Decimal(), "currency": m.Currency}
}

func occurrenceJSON(o calendar.Occurrence) map[string]any {
	value := map[string]any{"precision": o.Precision, "source": o.Source}
	if o.At != nil {
		value["at"] = o.At.UTC().Format(time.RFC3339)
	}
	if o.On != nil {
		value["on"] = o.On.String()
	}
	return value
}

func jsonValue(value any) json.RawMessage {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(fmt.Sprintf("encode revision value: %v", err))
	}
	return encoded
}
