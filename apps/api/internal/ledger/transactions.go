package ledger

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	maxOriginalDescription = 2000
	maxShortText           = 200
	minInstallments        = 2
	maxInstallments        = 120
)

type TransactionDetail struct {
	Transaction
	Categories []Allocation
	Evidence   []Evidence
	Fields     []FieldSource
}

func (s *Service) CreateTransaction(ctx context.Context, accountID, observationID uuid.UUID, in NewTransaction, actor Actor) (Transaction, error) {
	var created Transaction
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		created, err = t.createTransaction(observationID, in, actor)
		return err
	})
	return created, err
}

func (s *Service) GetTransaction(ctx context.Context, accountID, id uuid.UUID) (TransactionDetail, error) {
	var detail TransactionDetail
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		detail, err = t.transactionDetail(id)
		return err
	})
	return detail, err
}

func (t *tx) createTransaction(observationID uuid.UUID, in NewTransaction, actor Actor) (Transaction, error) {
	in, err := normalizeNewTransaction(in)
	if err != nil {
		return Transaction{}, err
	}
	observation, err := t.q.GetObservation(t.ctx, tenantdb.GetObservationParams{AccountID: t.accountID, ID: observationID})
	if err != nil {
		return Transaction{}, fmt.Errorf("observation %s: %w", observationID, notFound(err))
	}
	var method *PaymentMethod
	if in.PaymentMethod != nil {
		found, err := t.ensurePaymentMethod(*in.PaymentMethod)
		if err != nil {
			return Transaction{}, err
		}
		method = &found
	}
	params := tenantdb.InsertTransactionParams{
		ID:                  store.NewID(),
		AccountID:           t.accountID,
		OriginalDescription: in.OriginalDescription,
		Description:         in.Description,
		Merchant:            in.Merchant,
		Payee:               in.Payee,
		AmountMinor:         in.Amount.Minor,
		Currency:            in.Amount.Currency,
		OccurredAt:          in.Occurred.At,
		OccurredOn:          dateTime(in.Occurred.On),
		DatePrecision:       string(in.Occurred.Precision),
		DateSource:          string(in.Occurred.Source),
		Nature:              string(in.Nature),
		Status:              string(in.Status),
		Purpose:             in.Purpose,
	}
	if method != nil {
		params.PaymentMethodID = &method.ID
	}
	if in.OriginalAmount != nil {
		params.OriginalAmountMinor = &in.OriginalAmount.Minor
		params.OriginalCurrency = &in.OriginalAmount.Currency
	}
	if in.Installments != nil {
		count := int32(in.Installments.Count) //nolint:gosec // bounded by validation
		params.InstallmentCount = &count
		if in.Installments.Amount != nil {
			params.InstallmentAmountMinor = &in.Installments.Amount.Minor
		}
	}
	row, err := t.q.InsertTransaction(t.ctx, params)
	if err != nil {
		return Transaction{}, fmt.Errorf("insert transaction: %w", err)
	}
	if _, err := t.linkEvidence(row.ID, observationID, EvidenceLink{Status: EvidenceConfirmed, Origin: OriginSystem, Reasons: []string{"created from this observation"}}); err != nil {
		return Transaction{}, err
	}
	for _, field := range providedFields(in) {
		if err := t.setFieldSource(row.ID, FieldSource{Field: field, ObservationID: &observationID}); err != nil {
			return Transaction{}, err
		}
	}
	created := toTransaction(row, method)
	actor.ObservationID = &observationID
	if err := t.recordRevision(row.ID, fieldCreated, jsonValue(nil), snapshot(created), creationReasons[Source(observation.Source)], actor); err != nil {
		return Transaction{}, err
	}
	return created, nil
}

func (t *tx) transactionDetail(id uuid.UUID) (TransactionDetail, error) {
	transaction, err := t.transaction(id)
	if err != nil {
		return TransactionDetail{}, err
	}
	evidence, err := t.transactionEvidence(id)
	if err != nil {
		return TransactionDetail{}, err
	}
	fields, err := t.fieldSources(id)
	if err != nil {
		return TransactionDetail{}, err
	}
	allocations, err := t.allocations(id)
	if err != nil {
		return TransactionDetail{}, err
	}
	return TransactionDetail{
		Transaction: transaction,
		Categories:  EffectiveAllocations(transaction, allocations),
		Evidence:    evidence,
		Fields:      fields,
	}, nil
}

func (t *tx) transaction(id uuid.UUID) (Transaction, error) {
	row, err := t.q.GetTransaction(t.ctx, tenantdb.GetTransactionParams{AccountID: t.accountID, ID: id})
	if err != nil {
		return Transaction{}, fmt.Errorf("transaction %s: %w", id, notFound(err))
	}
	return t.withPaymentMethod(row)
}

func (t *tx) withPaymentMethod(row tenantdb.Transaction) (Transaction, error) {
	if row.PaymentMethodID == nil {
		return toTransaction(row, nil), nil
	}
	method, err := t.paymentMethod(*row.PaymentMethodID)
	if err != nil {
		return Transaction{}, err
	}
	return toTransaction(row, &method), nil
}

func (t *tx) setFieldSource(transactionID uuid.UUID, source FieldSource) error {
	err := t.q.SetFieldProvenance(t.ctx, tenantdb.SetFieldProvenanceParams{
		AccountID:       t.accountID,
		TransactionID:   transactionID,
		Field:           string(source.Field),
		ObservationID:   source.ObservationID,
		ConfirmedByUser: source.ConfirmedByUser,
	})
	if err != nil {
		return fmt.Errorf("record provenance of %s: %w", source.Field, err)
	}
	return nil
}

func (t *tx) fieldSources(transactionID uuid.UUID) ([]FieldSource, error) {
	rows, err := t.q.ListFieldProvenance(t.ctx, tenantdb.ListFieldProvenanceParams{AccountID: t.accountID, TransactionID: transactionID})
	if err != nil {
		return nil, fmt.Errorf("list provenance: %w", err)
	}
	sources := make([]FieldSource, len(rows))
	for i, row := range rows {
		sources[i] = FieldSource{Field: Field(row.Field), ObservationID: row.ObservationID, ConfirmedByUser: row.ConfirmedByUser}
	}
	return sources, nil
}

func providedFields(in NewTransaction) []Field {
	fields := []Field{FieldDescription, FieldAmount, FieldNature}
	if in.Occurred.Known() {
		fields = append(fields, FieldOccurred)
	}
	optional := map[Field]bool{
		FieldMerchant:      in.Merchant != nil,
		FieldPayee:         in.Payee != nil,
		FieldPaymentMethod: in.PaymentMethod != nil,
		FieldPurpose:       in.Purpose != nil,
		FieldInstallments:  in.Installments != nil,
	}
	for _, field := range []Field{FieldMerchant, FieldPayee, FieldPaymentMethod, FieldPurpose, FieldInstallments} {
		if optional[field] {
			fields = append(fields, field)
		}
	}
	return fields
}

func normalizeNewTransaction(in NewTransaction) (NewTransaction, error) {
	var err error
	if in.OriginalDescription, err = requiredText("original_description", in.OriginalDescription, maxOriginalDescription); err != nil {
		return in, err
	}
	if in.Description, err = requiredText("description", in.Description, maxShortText); err != nil {
		return in, err
	}
	if in.Merchant, err = optionalText("merchant", in.Merchant, maxShortText); err != nil {
		return in, err
	}
	if in.Payee, err = optionalText("payee", in.Payee, maxShortText); err != nil {
		return in, err
	}
	if in.Purpose, err = optionalText("purpose", in.Purpose, maxShortText); err != nil {
		return in, err
	}
	if err := validateAmount("amount", in.Amount); err != nil {
		return in, err
	}
	if in.OriginalAmount != nil {
		if err := validateAmount("original_amount", *in.OriginalAmount); err != nil {
			return in, err
		}
		if in.OriginalAmount.Currency == in.Amount.Currency {
			return in, &ValidationError{Field: "original_amount", Message: "must be in a currency different from amount"}
		}
	}
	if err := validateOccurrence(in.Occurred); err != nil {
		return in, err
	}
	if in.PaymentMethod != nil {
		if err := validatePaymentMethod(*in.PaymentMethod); err != nil {
			return in, err
		}
	}
	if !slices.Contains(natures, in.Nature) {
		return in, &ValidationError{Field: "nature", Message: fmt.Sprintf("must be one of %v", natures)}
	}
	if !slices.Contains([]Status{StatusProvisional, StatusConfirmed, StatusPendingClarification}, in.Status) {
		return in, &ValidationError{Field: "status", Message: "must be provisional, confirmed or pending_clarification for a new transaction"}
	}
	if in.Installments != nil {
		if err := validateInstallments(*in.Installments, in.Amount.Currency); err != nil {
			return in, err
		}
	}
	return in, nil
}

func requiredText(field, value string, limit int) (string, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" || len([]rune(trimmed)) > limit {
		return "", &ValidationError{Field: field, Message: fmt.Sprintf("must have 1 to %d characters", limit)}
	}
	return trimmed, nil
}

func optionalText(field string, value *string, limit int) (*string, error) {
	if value == nil {
		return nil, nil
	}
	trimmed, err := requiredText(field, *value, limit)
	if err != nil {
		return nil, err
	}
	return &trimmed, nil
}

func validateAmount(field string, amount money.Money) error {
	if err := money.ValidCurrency(amount.Currency); err != nil {
		return &ValidationError{Field: field, Message: err.Error()}
	}
	if amount.Minor <= 0 {
		return &ValidationError{Field: field, Message: "must be greater than zero; the nature tells whether money went out or came back"}
	}
	return nil
}

func validateOccurrence(o calendar.Occurrence) error {
	if !slices.Contains([]calendar.Source{calendar.SourceStated, calendar.SourceInferred, calendar.SourceDocument}, o.Source) {
		return &ValidationError{Field: "date_source", Message: "must be stated, inferred or document"}
	}
	consistent := false
	switch o.Precision {
	case calendar.PrecisionExact:
		consistent = o.At != nil && o.On != nil
	case calendar.PrecisionDay:
		consistent = o.At == nil && o.On != nil
	case calendar.PrecisionMonth:
		consistent = o.At == nil && o.On != nil && o.On.Day == 1
	case calendar.PrecisionUnknown:
		consistent = o.At == nil && o.On == nil
	}
	if !consistent {
		return &ValidationError{Field: "date_precision", Message: fmt.Sprintf("%q does not match the date given", o.Precision)}
	}
	return nil
}

func validatePaymentMethod(ref PaymentMethodRef) error {
	if !slices.Contains(paymentKinds, ref.Kind) {
		return &ValidationError{Field: "payment_method", Message: fmt.Sprintf("must be one of %v", paymentKinds)}
	}
	if ref.CardLast4 == nil {
		return nil
	}
	if !ref.Kind.IsCard() {
		return &ValidationError{Field: "card_last4", Message: "only applies to credit_card and debit_card"}
	}
	if len(*ref.CardLast4) != 4 || strings.Trim(*ref.CardLast4, "0123456789") != "" {
		return &ValidationError{Field: "card_last4", Message: "must be the last 4 digits of the card"}
	}
	return nil
}

func validateInstallments(in Installments, currency string) error {
	if in.Count < minInstallments || in.Count > maxInstallments {
		return &ValidationError{Field: "installment_count", Message: fmt.Sprintf("must be between %d and %d", minInstallments, maxInstallments)}
	}
	if in.Amount == nil {
		return nil
	}
	if in.Amount.Currency != currency {
		return &ValidationError{Field: "installment_amount", Message: "must be in the same currency as amount"}
	}
	return validateAmount("installment_amount", *in.Amount)
}

func toTransaction(row tenantdb.Transaction, method *PaymentMethod) Transaction {
	transaction := Transaction{
		ID:                  row.ID,
		OriginalDescription: row.OriginalDescription,
		Description:         row.Description,
		Merchant:            row.Merchant,
		Payee:               row.Payee,
		Amount:              money.Money{Minor: row.AmountMinor, Currency: row.Currency},
		Occurred:            toOccurrence(row.OccurredAt, row.OccurredOn, row.DatePrecision, row.DateSource),
		PaymentMethod:       method,
		Nature:              Nature(row.Nature),
		Status:              Status(row.Status),
		Purpose:             row.Purpose,
		RefundOfID:          row.RefundOfID,
		MergedIntoID:        row.MergedIntoID,
		CreatedAt:           row.CreatedAt,
		UpdatedAt:           row.UpdatedAt,
	}
	if row.OriginalAmountMinor != nil && row.OriginalCurrency != nil {
		transaction.OriginalAmount = &money.Money{Minor: *row.OriginalAmountMinor, Currency: *row.OriginalCurrency}
	}
	if row.InstallmentCount != nil {
		transaction.Installments = &Installments{Count: int(*row.InstallmentCount)}
		if row.InstallmentAmountMinor != nil {
			transaction.Installments.Amount = &money.Money{Minor: *row.InstallmentAmountMinor, Currency: row.Currency}
		}
	}
	return transaction
}

func toOccurrence(at, on *time.Time, precision, source string) calendar.Occurrence {
	occurrence := calendar.Occurrence{Precision: calendar.Precision(precision), Source: calendar.Source(source)}
	if at != nil {
		value := at.UTC()
		occurrence.At = &value
	}
	if on != nil {
		day := calendar.DateFromTime(*on)
		occurrence.On = &day
	}
	return occurrence
}

func dateTime(day *calendar.Date) *time.Time {
	if day == nil {
		return nil
	}
	value := day.Time()
	return &value
}
