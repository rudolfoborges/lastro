package ledger

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/redaction"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const maxSourceKeyLength = 300

var sources = []Source{SourceConversation, SourceWalletNotification, SourceStatementLine, SourceManual, SourceImport}

func (s *Service) RecordObservation(ctx context.Context, accountID uuid.UUID, in NewObservation) (Observation, error) {
	var observation Observation
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		observation, err = t.recordObservation(in)
		return err
	})
	return observation, err
}

func (t *tx) recordObservation(in NewObservation) (Observation, error) {
	if !slices.Contains(sources, in.Source) {
		return Observation{}, &ValidationError{Field: "source", Message: fmt.Sprintf("must be one of %v", sources)}
	}
	key := strings.TrimSpace(in.SourceKey)
	if key == "" || len(key) > maxSourceKeyLength {
		return Observation{}, &ValidationError{Field: "source_key", Message: fmt.Sprintf("must have 1 to %d characters", maxSourceKeyLength)}
	}
	if in.ReceivedAt.IsZero() {
		return Observation{}, &ValidationError{Field: "received_at", Message: "is required"}
	}
	payload, findings := redaction.Values(nonNilPayload(in.Payload))
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return Observation{}, fmt.Errorf("encode observation payload: %w", err)
	}
	redactionsJSON, err := json.Marshal(redaction.Merge(in.Redactions, findings))
	if err != nil {
		return Observation{}, fmt.Errorf("encode redactions: %w", err)
	}
	row, err := t.q.InsertObservation(t.ctx, tenantdb.InsertObservationParams{
		ID:          store.NewID(),
		AccountID:   t.accountID,
		Source:      string(in.Source),
		SourceKey:   key,
		Payload:     payloadJSON,
		Redactions:  redactionsJSON,
		Fingerprint: in.Fingerprint,
		ReceivedAt:  in.ReceivedAt.UTC(),
	})
	if isUniqueViolation(err, "observations_account_id_source_source_key_key") {
		return Observation{}, ErrDuplicateObservation
	}
	if err != nil {
		return Observation{}, fmt.Errorf("insert observation: %w", err)
	}
	return toObservation(row)
}

var eventTypes = []EventType{EventPurchase, EventPurchaseUpdate, EventCancellation, EventRefund, EventDeclined, EventNotice, EventUnrelated, EventUnknown}

type InterpretationRecord struct {
	Interpretation
	ID            uuid.UUID
	ObservationID uuid.UUID
	Version       int
	CreatedAt     time.Time
}

func (s *Service) RecordInterpretation(ctx context.Context, accountID, observationID uuid.UUID, in Interpretation) (InterpretationRecord, error) {
	var record InterpretationRecord
	err := s.inTx(ctx, accountID, func(t *tx) error {
		if _, err := t.q.GetObservation(t.ctx, tenantdb.GetObservationParams{AccountID: t.accountID, ID: observationID}); err != nil {
			return fmt.Errorf("observation %s: %w", observationID, notFound(err))
		}
		var err error
		record, err = t.recordInterpretation(observationID, in)
		return err
	})
	return record, err
}

func (s *Service) Interpretations(ctx context.Context, accountID, observationID uuid.UUID) ([]InterpretationRecord, error) {
	var records []InterpretationRecord
	err := s.inTx(ctx, accountID, func(t *tx) error {
		rows, err := t.q.ListInterpretations(t.ctx, tenantdb.ListInterpretationsParams{AccountID: t.accountID, ObservationID: observationID})
		if err != nil {
			return fmt.Errorf("list interpretations: %w", err)
		}
		records = make([]InterpretationRecord, 0, len(rows))
		for _, row := range rows {
			record, err := t.toInterpretation(row)
			if err != nil {
				return err
			}
			records = append(records, record)
		}
		return nil
	})
	return records, err
}

func (t *tx) recordInterpretation(observationID uuid.UUID, in Interpretation) (InterpretationRecord, error) {
	if !slices.Contains(eventTypes, in.EventType) {
		return InterpretationRecord{}, &ValidationError{Field: "event_type", Message: fmt.Sprintf("must be one of %v", eventTypes)}
	}
	if in.Amount != nil {
		if err := validateAmount("amount", *in.Amount); err != nil {
			return InterpretationRecord{}, err
		}
	}
	if err := validateOccurrence(in.Occurred); err != nil {
		return InterpretationRecord{}, err
	}
	merchant, err := optionalText("merchant", in.Merchant, maxShortText)
	if err != nil {
		return InterpretationRecord{}, err
	}
	in.Merchant = merchant
	if !slices.Contains([]Author{AuthorAgent, AuthorUser, AuthorSystem}, in.Author) {
		return InterpretationRecord{}, &ValidationError{Field: "author", Message: "must be agent, user or system"}
	}
	var paymentMethodID *uuid.UUID
	if in.PaymentMethod != nil {
		method, err := t.ensurePaymentMethod(*in.PaymentMethod)
		if err != nil {
			return InterpretationRecord{}, err
		}
		paymentMethodID = &method.ID
	}
	details, _ := redaction.Values(nonNilPayload(in.Details))
	detailsJSON, err := json.Marshal(details)
	if err != nil {
		return InterpretationRecord{}, fmt.Errorf("encode interpretation details: %w", err)
	}
	params := tenantdb.InsertInterpretationParams{
		ID:              store.NewID(),
		AccountID:       t.accountID,
		ObservationID:   observationID,
		EventType:       string(in.EventType),
		Merchant:        in.Merchant,
		OccurredAt:      in.Occurred.At,
		OccurredOn:      dateTime(in.Occurred.On),
		DatePrecision:   string(in.Occurred.Precision),
		DateSource:      string(in.Occurred.Source),
		PaymentMethodID: paymentMethodID,
		Details:         detailsJSON,
		Author:          string(in.Author),
		TransactionID:   in.TransactionID,
		SubmissionHash:  in.submissionHash,
	}
	if in.Effect != nil {
		effect := string(*in.Effect)
		params.Effect = &effect
	}
	if in.Amount != nil {
		params.AmountMinor = &in.Amount.Minor
		params.Currency = &in.Amount.Currency
	}
	row, err := t.q.InsertInterpretation(t.ctx, params)
	if err != nil {
		return InterpretationRecord{}, fmt.Errorf("insert interpretation: %w", err)
	}
	return t.toInterpretation(row)
}

func (t *tx) toInterpretation(row tenantdb.ObservationInterpretation) (InterpretationRecord, error) {
	record := InterpretationRecord{
		ID:            row.ID,
		ObservationID: row.ObservationID,
		Version:       int(row.Version),
		CreatedAt:     row.CreatedAt,
		Interpretation: Interpretation{
			EventType:      EventType(row.EventType),
			Merchant:       row.Merchant,
			Occurred:       toOccurrence(row.OccurredAt, row.OccurredOn, row.DatePrecision, row.DateSource),
			Author:         Author(row.Author),
			TransactionID:  row.TransactionID,
			submissionHash: row.SubmissionHash,
		},
	}
	if row.Effect != nil {
		effect := InterpretationEffect(*row.Effect)
		record.Effect = &effect
	}
	if row.AmountMinor != nil && row.Currency != nil {
		record.Amount = &money.Money{Minor: *row.AmountMinor, Currency: *row.Currency}
	}
	if row.PaymentMethodID != nil {
		method, err := t.paymentMethod(*row.PaymentMethodID)
		if err != nil {
			return InterpretationRecord{}, err
		}
		record.PaymentMethod = &PaymentMethodRef{Kind: method.Kind, CardLast4: method.CardLast4}
	}
	if err := json.Unmarshal(row.Details, &record.Details); err != nil {
		return InterpretationRecord{}, fmt.Errorf("decode interpretation details: %w", err)
	}
	return record, nil
}

func nonNilPayload(payload map[string]any) map[string]any {
	if payload == nil {
		return map[string]any{}
	}
	return payload
}

func toObservation(row tenantdb.Observation) (Observation, error) {
	observation := Observation{
		ID:         row.ID,
		Source:     Source(row.Source),
		SourceKey:  row.SourceKey,
		ReceivedAt: row.ReceivedAt,
		CreatedAt:  row.CreatedAt,
	}
	if err := json.Unmarshal(row.Payload, &observation.Payload); err != nil {
		return Observation{}, fmt.Errorf("decode observation payload: %w", err)
	}
	if err := json.Unmarshal(row.Redactions, &observation.Redactions); err != nil {
		return Observation{}, fmt.Errorf("decode redactions: %w", err)
	}
	return observation, nil
}
