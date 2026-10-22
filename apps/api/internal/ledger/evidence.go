package ledger

import (
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

type EvidenceLink struct {
	Status  EvidenceStatus
	Origin  Origin
	Score   *int
	Reasons []string
}

var evidenceTransitions = map[EvidenceStatus][]EvidenceStatus{
	EvidenceSuggested: {EvidenceConfirmed, EvidenceRejected, EvidenceUndone},
	EvidenceConfirmed: {EvidenceUndone},
}

func (s *Service) LinkEvidence(ctx context.Context, accountID, transactionID, observationID uuid.UUID, link EvidenceLink) (Evidence, error) {
	var evidence Evidence
	err := s.inTx(ctx, accountID, func(t *tx) error {
		if _, err := t.transaction(transactionID); err != nil {
			return err
		}
		if _, err := t.q.GetObservation(t.ctx, tenantdb.GetObservationParams{AccountID: t.accountID, ID: observationID}); err != nil {
			return fmt.Errorf("observation %s: %w", observationID, notFound(err))
		}
		var err error
		evidence, err = t.linkEvidence(transactionID, observationID, link)
		return err
	})
	return evidence, err
}

func (s *Service) SetEvidenceStatus(ctx context.Context, accountID, evidenceID uuid.UUID, status EvidenceStatus, origin Origin) (Evidence, error) {
	var evidence Evidence
	err := s.inTx(ctx, accountID, func(t *tx) error {
		current, err := t.q.GetEvidence(t.ctx, tenantdb.GetEvidenceParams{AccountID: t.accountID, ID: evidenceID})
		if err != nil {
			return fmt.Errorf("evidence %s: %w", evidenceID, notFound(err))
		}
		if !slices.Contains(evidenceTransitions[EvidenceStatus(current.Status)], status) {
			return fmt.Errorf("%w: cannot go from %s to %s", ErrEvidenceClosed, current.Status, status)
		}
		row, err := t.q.SetEvidenceStatus(t.ctx, tenantdb.SetEvidenceStatusParams{
			AccountID: t.accountID,
			ID:        evidenceID,
			Status:    string(status),
			Origin:    string(origin),
		})
		if isUniqueViolation(err, "transaction_evidence_confirmed_observation_idx") {
			return ErrObservationClaimed
		}
		if err != nil {
			return fmt.Errorf("update evidence: %w", err)
		}
		evidence, err = t.evidence(row.TransactionID, row.ID)
		return err
	})
	return evidence, err
}

func (s *Service) TransactionEvidence(ctx context.Context, accountID, transactionID uuid.UUID) ([]Evidence, error) {
	var evidence []Evidence
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		evidence, err = t.transactionEvidence(transactionID)
		return err
	})
	return evidence, err
}

func (t *tx) linkEvidence(transactionID, observationID uuid.UUID, link EvidenceLink) (Evidence, error) {
	if link.Status != EvidenceSuggested && link.Status != EvidenceConfirmed {
		return Evidence{}, &ValidationError{Field: "status", Message: "a new link must be suggested or confirmed"}
	}
	var score *int16
	if link.Score != nil {
		if *link.Score < 0 || *link.Score > 100 {
			return Evidence{}, &ValidationError{Field: "score", Message: "must be between 0 and 100"}
		}
		value := int16(*link.Score) //nolint:gosec // bounded above
		score = &value
	}
	reasons := link.Reasons
	if reasons == nil {
		reasons = []string{}
	}
	row, err := t.q.InsertEvidence(t.ctx, tenantdb.InsertEvidenceParams{
		ID:            store.NewID(),
		AccountID:     t.accountID,
		TransactionID: transactionID,
		ObservationID: observationID,
		Status:        string(link.Status),
		Origin:        string(link.Origin),
		Score:         score,
		Reasons:       reasons,
	})
	switch {
	case isUniqueViolation(err, "transaction_evidence_confirmed_observation_idx"):
		return Evidence{}, ErrObservationClaimed
	case isUniqueViolation(err, "transaction_evidence_open_link_idx"):
		return Evidence{}, &ValidationError{Field: "observation_id", Message: "is already linked to this transaction"}
	case err != nil:
		return Evidence{}, fmt.Errorf("insert evidence: %w", err)
	}
	return t.evidence(transactionID, row.ID)
}

func (t *tx) evidence(transactionID, evidenceID uuid.UUID) (Evidence, error) {
	all, err := t.transactionEvidence(transactionID)
	if err != nil {
		return Evidence{}, err
	}
	for _, evidence := range all {
		if evidence.ID == evidenceID {
			return evidence, nil
		}
	}
	return Evidence{}, fmt.Errorf("evidence %s: %w", evidenceID, ErrNotFound)
}

func (t *tx) transactionEvidence(transactionID uuid.UUID) ([]Evidence, error) {
	rows, err := t.q.ListTransactionEvidence(t.ctx, tenantdb.ListTransactionEvidenceParams{AccountID: t.accountID, TransactionID: transactionID})
	if err != nil {
		return nil, fmt.Errorf("list evidence: %w", err)
	}
	evidence := make([]Evidence, len(rows))
	for i, row := range rows {
		evidence[i] = Evidence{
			ID:            row.ID,
			TransactionID: row.TransactionID,
			ObservationID: row.ObservationID,
			Source:        Source(row.Source),
			ReceivedAt:    row.ReceivedAt,
			Status:        EvidenceStatus(row.Status),
			Origin:        Origin(row.Origin),
			Reasons:       row.Reasons,
			CreatedAt:     row.CreatedAt,
			UpdatedAt:     row.UpdatedAt,
		}
		if row.Score != nil {
			score := int(*row.Score)
			evidence[i].Score = &score
		}
	}
	return evidence, nil
}
