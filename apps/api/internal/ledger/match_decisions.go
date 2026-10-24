package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	fieldMatch           = "match"
	fieldPlan            = "installment_plan"
	subjectMatch         = "match"
	reasonDistinct       = "Case confirmou que são compras diferentes."
	reasonMergedQuestion = "Mesma compra: os lançamentos foram fundidos."
	reasonRejected       = "Compras diferentes."
	reasonMatchSettled   = "Situação ajustada após a conferência entre fontes."
)

var sourceRanks = map[Source]int{SourceStatementLine: 3, SourceWalletNotification: 2}

type MatchDecision struct {
	TransactionID      uuid.UUID
	OtherTransactionID uuid.UUID
	Reason             string
}

type MergeOutcome struct {
	Survivor  Transaction
	Merged    Transaction
	Category  Allocation
	Changed   []string
	Conflicts []FieldConflict
}

type RejectOutcome struct {
	Transactions []Transaction
}

type fieldChange struct {
	Field  string          `json:"field"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}

type mergeRecord struct {
	SurvivorID uuid.UUID       `json:"survivor_id"`
	MergedID   uuid.UUID       `json:"merged_id"`
	Changed    []string        `json:"changed"`
	Conflicts  []FieldConflict `json:"conflicts"`
}

func (s *Service) ConfirmMatch(ctx context.Context, accountID uuid.UUID, decision MatchDecision, actor Actor) (MergeOutcome, error) {
	var outcome MergeOutcome
	err := s.inTx(ctx, accountID, func(t *tx) error {
		record, err := once(t, "confirm_match", actor, decision, func() (mergeRecord, error) {
			return t.merge(decision, actor)
		})
		if err != nil {
			return err
		}
		if outcome.Survivor, err = t.transaction(record.SurvivorID); err != nil {
			return err
		}
		if outcome.Merged, err = t.transaction(record.MergedID); err != nil {
			return err
		}
		outcome.Changed, outcome.Conflicts = record.Changed, record.Conflicts
		outcome.Category, err = t.effectiveCategory(record.SurvivorID)
		return err
	})
	return outcome, err
}

func (s *Service) RejectMatch(ctx context.Context, accountID uuid.UUID, decision MatchDecision, actor Actor) (RejectOutcome, error) {
	var outcome RejectOutcome
	err := s.inTx(ctx, accountID, func(t *tx) error {
		ids, err := once(t, "reject_match", actor, decision, func() ([]uuid.UUID, error) {
			return t.reject(decision, actor)
		})
		if err != nil {
			return err
		}
		for _, id := range ids {
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

func (t *tx) lockPair(decision MatchDecision) (Transaction, Transaction, string, error) {
	reason, err := requiredText("reason", decision.Reason, maxReason)
	if err != nil {
		return Transaction{}, Transaction{}, "", err
	}
	if decision.TransactionID == decision.OtherTransactionID {
		return Transaction{}, Transaction{}, "", &ValidationError{Field: "other_transaction_id", Message: "must be another transaction"}
	}
	ids := []uuid.UUID{decision.TransactionID, decision.OtherTransactionID}
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return strings.Compare(a.String(), b.String()) })
	locked := map[uuid.UUID]Transaction{}
	for _, id := range ids {
		field := "transaction_id"
		if id == decision.OtherTransactionID {
			field = "other_transaction_id"
		}
		transaction, err := t.lockOpenTransaction(id)
		switch {
		case errors.Is(err, ErrNotFound):
			return Transaction{}, Transaction{}, "", &ValidationError{Field: field, Message: "is not a transaction of Rudolfo's account"}
		case errors.Is(err, ErrTransactionClosed):
			return Transaction{}, Transaction{}, "", &ValidationError{Field: field, Message: "is voided or merged; use the transaction that is still open"}
		case err != nil:
			return Transaction{}, Transaction{}, "", err
		}
		locked[id] = transaction
	}
	return locked[decision.TransactionID], locked[decision.OtherTransactionID], reason, nil
}

func (t *tx) merge(decision MatchDecision, actor Actor) (mergeRecord, error) {
	a, b, reason, err := t.lockPair(decision)
	if err != nil {
		return mergeRecord{}, err
	}
	sourcesA, err := t.evidenceSources(a.ID)
	if err != nil {
		return mergeRecord{}, err
	}
	sourcesB, err := t.evidenceSources(b.ID)
	if err != nil {
		return mergeRecord{}, err
	}
	if err := mergeable(a, sourcesA, b, sourcesB); err != nil {
		return mergeRecord{}, err
	}
	survivor, merged := a, b
	if outranks(b, sourcesB, a, sourcesA) {
		survivor, merged = b, a
	}
	if err := t.closePossibleMatches([]uuid.UUID{survivor.ID, merged.ID}, func(c Clarification) (bool, error) {
		return slices.Contains(candidateIDs(c), survivor.ID) || slices.Contains(candidateIDs(c), merged.ID), nil
	}, reasonMergedQuestion, actor.Author); err != nil {
		return mergeRecord{}, err
	}
	observations, err := t.moveEvidence(merged.ID, survivor.ID, "moved here when the transactions were merged", actor)
	if err != nil {
		return mergeRecord{}, err
	}
	record := mergeRecord{SurvivorID: survivor.ID, MergedID: merged.ID}
	changes, err := t.absorb(survivor, merged, reason, actor, &record)
	if err != nil {
		return mergeRecord{}, err
	}
	refunds, err := t.moveRefunds(merged.ID, survivor.ID, nil)
	if err != nil {
		return mergeRecord{}, err
	}
	if err := t.assertRefundsFit(survivor.ID); err != nil {
		return mergeRecord{}, err
	}
	if _, err := t.q.MarkTransactionMerged(t.ctx, tenantdb.MarkTransactionMergedParams{AccountID: t.accountID, ID: merged.ID, MergedIntoID: &survivor.ID}); err != nil {
		return mergeRecord{}, fmt.Errorf("mark merged: %w", err)
	}
	if err := t.recordRevision(merged.ID, fieldStatus, jsonValue(merged.Status), jsonValue(StatusMerged), reason, actor); err != nil {
		return mergeRecord{}, err
	}
	if err := t.recordRevision(survivor.ID, fieldMatch, jsonValue(nil), jsonValue(map[string]any{"merged": merged.ID}), reason, actor); err != nil {
		return mergeRecord{}, err
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return mergeRecord{}, fmt.Errorf("encode merge changes: %w", err)
	}
	_, err = t.q.InsertTransactionMerge(t.ctx, tenantdb.InsertTransactionMergeParams{
		ID:             store.NewID(),
		AccountID:      t.accountID,
		SurvivorID:     survivor.ID,
		MergedID:       merged.ID,
		MergedStatus:   string(merged.Status),
		ObservationIds: observations,
		RefundIds:      refunds,
		Changes:        encoded,
		Reason:         reason,
		Author:         string(decisionAuthor(actor)),
	})
	if err != nil {
		return mergeRecord{}, fmt.Errorf("record merge: %w", err)
	}
	if err := t.refreshClarifications(merged.ID); err != nil {
		return mergeRecord{}, err
	}
	if err := t.settleStatus(survivor.ID); err != nil {
		return mergeRecord{}, err
	}
	return record, t.refreshClarifications(survivor.ID)
}

func mergeable(a Transaction, sourcesA []Source, b Transaction, sourcesB []Source) error {
	switch {
	case a.Nature != b.Nature:
		return &ValidationError{Field: "other_transaction_id", Message: fmt.Sprintf("is a %s and the transaction is a %s; only records of the same nature can be the same purchase", b.Nature, a.Nature)}
	case !sameMoney(a, b):
		return &ValidationError{Field: "other_transaction_id", Message: "must be in the same currency, or have the same original amount for an international purchase"}
	case (a.RefundOfID != nil && *a.RefundOfID == b.ID) || (b.RefundOfID != nil && *b.RefundOfID == a.ID):
		return &ValidationError{Field: "other_transaction_id", Message: "is the refund of the other; a purchase and its refund are two transactions"}
	}
	for _, source := range sourcesA {
		if slices.Contains(sourcesB, source) {
			return &ValidationError{Field: "other_transaction_id", Message: fmt.Sprintf("also has a record from %s; two records of the same source are two purchases and are never merged", source)}
		}
	}
	return nil
}

func sameMoney(a, b Transaction) bool {
	if a.Amount.Currency == b.Amount.Currency {
		return true
	}
	matches := func(x *money.Money, y money.Money) bool { return x != nil && *x == y }
	return matches(a.OriginalAmount, b.Amount) || matches(b.OriginalAmount, a.Amount) ||
		(a.OriginalAmount != nil && b.OriginalAmount != nil && *a.OriginalAmount == *b.OriginalAmount)
}

func outranks(a Transaction, sourcesA []Source, b Transaction, sourcesB []Source) bool {
	rank := func(sources []Source) int {
		best := 1
		for _, source := range sources {
			best = max(best, sourceRanks[source])
		}
		return best
	}
	if ra, rb := rank(sourcesA), rank(sourcesB); ra != rb {
		return ra > rb
	}
	if !a.CreatedAt.Equal(b.CreatedAt) {
		return a.CreatedAt.Before(b.CreatedAt)
	}
	return a.ID.String() < b.ID.String()
}

func decisionAuthor(actor Actor) Author {
	if actor.Author == AuthorRule {
		return AuthorAgent
	}
	return actor.Author
}

func evidenceOrigin(actor Actor) Origin {
	switch actor.Author {
	case AuthorUser:
		return OriginUser
	case AuthorSystem:
		return OriginSystem
	default:
		return OriginAgent
	}
}

func (t *tx) moveEvidence(fromID, toID uuid.UUID, reason string, actor Actor) ([]uuid.UUID, error) {
	rows, err := t.q.ConfirmedEvidenceOf(t.ctx, tenantdb.ConfirmedEvidenceOfParams{AccountID: t.accountID, TransactionID: fromID})
	if err != nil {
		return nil, fmt.Errorf("list evidence: %w", err)
	}
	moved := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		if _, err := t.q.SetEvidenceStatus(t.ctx, tenantdb.SetEvidenceStatusParams{AccountID: t.accountID, ID: row.ID, Status: string(EvidenceUndone), Origin: string(evidenceOrigin(actor))}); err != nil {
			return nil, fmt.Errorf("undo evidence: %w", err)
		}
		if _, err := t.linkEvidence(toID, row.ObservationID, EvidenceLink{Status: EvidenceConfirmed, Origin: evidenceOrigin(actor), Reasons: []string{reason}}); err != nil {
			return nil, err
		}
		moved = append(moved, row.ObservationID)
	}
	return moved, nil
}

func (t *tx) absorb(survivor, merged Transaction, reason string, actor Actor, record *mergeRecord) ([]fieldChange, error) {
	correction, err := t.correct(survivor.ID, mergeGapPatch(survivor, merged), reason, actor)
	if err != nil {
		return nil, err
	}
	changes := make([]fieldChange, 0, len(correction.Changed)+1)
	for _, field := range correction.Changed {
		changes = append(changes, fieldChange{Field: string(field), Before: fieldValue(survivor, field), After: fieldValue(correction.Transaction, field)})
		record.Changed = append(record.Changed, string(field))
	}
	mergedSources, err := t.fieldSources(merged.ID)
	if err != nil {
		return nil, err
	}
	var conflicts []FieldConflict
	for _, source := range mergedSources {
		if !source.ConfirmedByUser {
			continue
		}
		if slices.Contains(correction.Changed, source.Field) {
			if err := t.setFieldSource(survivor.ID, source); err != nil {
				return nil, err
			}
			continue
		}
		current, proposed := fieldValue(correction.Transaction, source.Field), fieldValue(merged, source.Field)
		if !sameJSON(current, proposed) {
			conflicts = append(conflicts, FieldConflict{Field: source.Field, Current: current, Proposed: proposed})
		}
	}
	planChange, err := t.absorbPlan(correction.Transaction, merged)
	if err != nil {
		return nil, err
	}
	if planChange != nil {
		changes = append(changes, *planChange)
		record.Changed = append(record.Changed, fieldPlan)
	}
	categoryChange, categoryConflict, err := t.absorbCategory(correction.Transaction, merged, reason)
	if err != nil {
		return nil, err
	}
	if categoryChange != nil {
		changes = append(changes, *categoryChange)
		record.Changed = append(record.Changed, fieldCategory)
	}
	if categoryConflict != nil {
		conflicts = append(conflicts, *categoryConflict)
	}
	record.Conflicts = append(correction.Conflicts, conflicts...)
	return changes, t.openConflicts(correction.Transaction, conflicts, actor)
}

func mergeGapPatch(survivor, merged Transaction) Patch {
	var patch Patch
	if survivor.Merchant == nil && merged.Merchant != nil {
		patch.Merchant = Value(*merged.Merchant)
	}
	if survivor.Payee == nil && merged.Payee != nil {
		patch.Payee = Value(*merged.Payee)
	}
	if survivor.Purpose == nil && merged.Purpose != nil {
		patch.Purpose = Value(*merged.Purpose)
	}
	if theirs := merged.PaymentMethod; theirs != nil {
		mine := survivor.PaymentMethod
		if mine == nil || (mine.Kind == theirs.Kind && mine.CardLast4 == nil && theirs.CardLast4 != nil) {
			patch.PaymentMethod = Value(PaymentMethodRef{Kind: theirs.Kind, CardLast4: theirs.CardLast4})
		}
	}
	if merged.Occurred.Known() && !survivor.Occurred.Known() {
		occurred := merged.Occurred
		patch.Occurred = &occurred
	}
	if survivor.Installments == nil && merged.Installments != nil && merged.Amount.Currency == survivor.Amount.Currency {
		patch.Installments = Value(*merged.Installments)
	}
	if survivor.AmountBasis == AmountInstallment && merged.AmountBasis == AmountTotal && merged.Installments != nil && merged.Amount.Currency == survivor.Amount.Currency {
		total := merged.Amount
		patch.Amount = &total
	}
	return patch
}

func (t *tx) absorbPlan(survivor, merged Transaction) (*fieldChange, error) {
	theirs, err := t.planRow(merged.ID)
	if err != nil || theirs == nil {
		return nil, err
	}
	mine, err := t.planRow(survivor.ID)
	if err != nil {
		return nil, err
	}
	if mine == nil {
		params := planUpdate(*theirs)
		params.TransactionID = survivor.ID
		if _, err := t.q.UpdateInstallmentPlan(t.ctx, params); err != nil {
			return nil, fmt.Errorf("move installment plan: %w", err)
		}
		return &fieldChange{
			Field:  fieldPlan,
			Before: jsonValue(map[string]any{"plan_id": theirs.ID, "transaction_id": merged.ID}),
			After:  jsonValue(map[string]any{"plan_id": theirs.ID, "transaction_id": survivor.ID}),
		}, nil
	}
	if mine.InstallmentCount != theirs.InstallmentCount {
		return nil, &ValidationError{Field: "other_transaction_id", Message: fmt.Sprintf("is in %d installments and the transaction in %d; correct the count before merging", theirs.InstallmentCount, mine.InstallmentCount)}
	}
	if err := t.assertNoCharges(theirs.ID, "other_transaction_id", "has installments charged by a statement too; two charged plans are two purchases"); err != nil {
		return nil, err
	}
	params := planUpdate(*mine)
	if params.TotalMinor == nil {
		params.TotalMinor = theirs.TotalMinor
	}
	if !params.InstallmentAmountKnown && theirs.InstallmentAmountKnown {
		params.InstallmentAmountMinor, params.InstallmentAmountKnown = theirs.InstallmentAmountMinor, true
	}
	updated, err := t.q.UpdateInstallmentPlan(t.ctx, params)
	if err != nil {
		return nil, fmt.Errorf("update installment plan: %w", err)
	}
	if err := t.cancelPlan(*theirs); err != nil {
		return nil, err
	}
	before := planSnapshot(*mine)
	before["canceled"] = theirs.ID
	return &fieldChange{Field: fieldPlan, Before: jsonValue(before), After: jsonValue(planSnapshot(updated))}, nil
}

func planSnapshot(row tenantdb.InstallmentPlan) map[string]any {
	return map[string]any{
		"plan_id":                  row.ID,
		"transaction_id":           row.TransactionID,
		"total_minor":              row.TotalMinor,
		"installment_amount_minor": row.InstallmentAmountMinor,
		"installment_amount_known": row.InstallmentAmountKnown,
	}
}

func (t *tx) absorbCategory(survivor, merged Transaction, reason string) (*fieldChange, *FieldConflict, error) {
	theirs, err := t.currentCategory(merged.ID)
	if err != nil || theirs == nil {
		return nil, nil, err
	}
	mine, err := t.currentCategory(survivor.ID)
	if err != nil {
		return nil, nil, err
	}
	switch {
	case mine != nil && mine.Status == AllocationConfirmed && theirs.Status == AllocationConfirmed && mine.Code != theirs.Code:
		return nil, &FieldConflict{Field: fieldCategory, Current: allocationJSON(mine), Proposed: allocationJSON(theirs)}, nil
	case mine == nil || (mine.Status == AllocationSuggested && theirs.Status == AllocationConfirmed):
		change := fieldChange{Field: fieldCategory, Before: allocationSnapshot(mine), After: allocationSnapshot(theirs)}
		return &change, nil, t.restoreCategory(survivor, theirs, reason)
	default:
		return nil, nil, nil
	}
}

func allocationSnapshot(allocation *Allocation) json.RawMessage {
	if allocation == nil {
		return jsonValue(nil)
	}
	return jsonValue(map[string]any{"category": allocation.Code, "status": allocation.Status, "origin": allocation.Origin})
}

func (t *tx) restoreCategory(transaction Transaction, allocation *Allocation, reason string) error {
	before, err := t.currentCategory(transaction.ID)
	if err != nil {
		return err
	}
	if allocation == nil {
		if err := t.q.SupersedeAllocations(t.ctx, tenantdb.SupersedeAllocationsParams{AccountID: t.accountID, TransactionID: transaction.ID}); err != nil {
			return fmt.Errorf("supersede category: %w", err)
		}
	} else {
		category, err := t.category(allocation.Code)
		if err != nil {
			return err
		}
		restored := Allocation{Code: category.Code, Label: category.Label, Amount: transaction.Amount, Status: allocation.Status, Origin: allocation.Origin}
		if err := t.replaceCategory(transaction.ID, category, restored); err != nil {
			return err
		}
	}
	return t.recordRevision(transaction.ID, fieldCategory, allocationJSON(before), allocationJSON(allocation), reason, Actor{Author: AuthorSystem})
}

func (t *tx) moveRefunds(fromID, toID uuid.UUID, only []uuid.UUID) ([]uuid.UUID, error) {
	ids := only
	if ids == nil {
		var err error
		if ids, err = t.q.RefundIDsOf(t.ctx, tenantdb.RefundIDsOfParams{AccountID: t.accountID, RefundOfID: &fromID}); err != nil {
			return nil, fmt.Errorf("list refunds: %w", err)
		}
	}
	if len(ids) == 0 {
		return []uuid.UUID{}, nil
	}
	moved, err := t.q.MoveRefunds(t.ctx, tenantdb.MoveRefundsParams{AccountID: t.accountID, FromID: &fromID, ToID: &toID, Ids: ids})
	if err != nil {
		return nil, fmt.Errorf("move refunds: %w", err)
	}
	return moved, nil
}

func (t *tx) assertRefundsFit(purchaseID uuid.UUID) error {
	purchase, err := t.transaction(purchaseID)
	if err != nil {
		return err
	}
	refunded, err := t.q.SumRefunds(t.ctx, tenantdb.SumRefundsParams{AccountID: t.accountID, RefundOfID: &purchaseID})
	if err != nil {
		return fmt.Errorf("sum refunds: %w", err)
	}
	if refunded > purchase.Amount.Minor {
		return &ValidationError{Field: "other_transaction_id", Message: fmt.Sprintf("its refunds would exceed the purchase of %s", purchase.Amount.Format())}
	}
	return nil
}

func (t *tx) reject(decision MatchDecision, actor Actor) ([]uuid.UUID, error) {
	a, b, reason, err := t.lockPair(decision)
	if err != nil {
		return nil, err
	}
	if err := t.rejectPair(a.ID, b.ID, reason, actor); err != nil {
		return nil, err
	}
	for _, pair := range [][2]Transaction{{a, b}, {b, a}} {
		if err := t.recordRevision(pair[0].ID, fieldMatch, jsonValue(nil), jsonValue(map[string]any{"rejected": pair[1].ID}), reason, actor); err != nil {
			return nil, err
		}
	}
	if err := t.settlePossibleMatches([]uuid.UUID{a.ID, b.ID}, actor); err != nil {
		return nil, err
	}
	return []uuid.UUID{a.ID, b.ID}, nil
}

func (t *tx) rejectPair(a, b uuid.UUID, reason string, actor Actor) error {
	low, high := a, b
	if high.String() < low.String() {
		low, high = high, low
	}
	err := t.q.InsertMatchRejection(t.ctx, tenantdb.InsertMatchRejectionParams{AccountID: t.accountID, LowID: low, HighID: high, Reason: reason, Author: string(decisionAuthor(actor))})
	if err != nil {
		return fmt.Errorf("record rejected match: %w", err)
	}
	return nil
}

func (t *tx) rejected(a, b uuid.UUID) (bool, error) {
	low, high := a, b
	if high.String() < low.String() {
		low, high = high, low
	}
	rejected, err := t.q.IsMatchRejected(t.ctx, tenantdb.IsMatchRejectedParams{AccountID: t.accountID, LowID: low, HighID: high})
	if err != nil {
		return false, fmt.Errorf("check rejected match: %w", err)
	}
	return rejected, nil
}

func (t *tx) rejectCandidates(transactionID uuid.UUID, candidates []MatchCandidate, reason string, actor Actor) error {
	for _, candidate := range candidates {
		if err := t.rejectPair(transactionID, candidate.Transaction.ID, reason, actor); err != nil {
			return err
		}
	}
	return nil
}

func (t *tx) settleCandidates(transaction Transaction, candidates []MatchCandidate, deferred bool, actor Actor) error {
	switch {
	case len(candidates) == 0:
		return nil
	case deferred:
		return t.deferMatch(transaction, candidates)
	default:
		return t.rejectCandidates(transaction.ID, candidates, reasonDistinct, actor)
	}
}

func (t *tx) deferMatch(transaction Transaction, candidates []MatchCandidate) error {
	ids := make([]string, len(candidates))
	listed := make([]map[string]any, len(candidates))
	descriptions := make([]string, len(candidates))
	for i, candidate := range candidates {
		ids[i] = candidate.Transaction.ID.String()
		descriptions[i] = describe(candidate.Transaction)
		listed[i] = map[string]any{
			"transaction_id": ids[i],
			"description":    candidate.Transaction.Description,
			"amount":         candidate.Transaction.Amount.Decimal(),
			"currency":       candidate.Transaction.Amount.Currency,
			"sources":        candidate.Sources,
			"reasons":        candidate.Reasons,
			"differences":    candidate.Differences,
		}
	}
	question := fmt.Sprintf("%s pode ser a mesma compra que %s. É a mesma compra?", describe(transaction), strings.Join(descriptions, " ou "))
	err := t.openClarification(newClarification{
		kind:          ClarificationPossibleMatch,
		transactionID: &transaction.ID,
		subject:       subjectMatch,
		question:      truncateRunes(question, maxQuestion),
		context:       map[string]any{"candidate_ids": ids, "candidates": listed},
	})
	if err != nil {
		return err
	}
	return t.settleStatus(transaction.ID)
}

func candidateIDs(c Clarification) []uuid.UUID {
	raw, _ := c.Context["candidate_ids"].([]any)
	ids := make([]uuid.UUID, 0, len(raw))
	for _, value := range raw {
		if text, ok := value.(string); ok {
			if id, err := uuid.Parse(text); err == nil {
				ids = append(ids, id)
			}
		}
	}
	return ids
}

func (t *tx) openPossibleMatches(ids []uuid.UUID) ([]Clarification, error) {
	rows, err := t.q.OpenPossibleMatches(t.ctx, tenantdb.OpenPossibleMatchesParams{AccountID: t.accountID, TransactionIds: ids})
	if err != nil {
		return nil, fmt.Errorf("list possible matches: %w", err)
	}
	clarifications := make([]Clarification, len(rows))
	for i, row := range rows {
		if clarifications[i], err = t.withTarget(row); err != nil {
			return nil, err
		}
	}
	return clarifications, nil
}

func (t *tx) closePossibleMatches(ids []uuid.UUID, settled func(Clarification) (bool, error), resolution string, by Author) error {
	open, err := t.openPossibleMatches(ids)
	if err != nil {
		return err
	}
	for _, clarification := range open {
		done, err := settled(clarification)
		if err != nil {
			return err
		}
		if !done {
			continue
		}
		row, err := t.q.LockClarification(t.ctx, tenantdb.LockClarificationParams{AccountID: t.accountID, ID: clarification.ID})
		if err != nil {
			return fmt.Errorf("lock clarification: %w", err)
		}
		if _, err := t.closeClarification(row, ClarificationResolved, resolution, by); err != nil {
			return err
		}
		if err := t.settleStatus(*clarification.TransactionID); err != nil {
			return err
		}
	}
	return nil
}

func (t *tx) settlePossibleMatches(ids []uuid.UUID, actor Actor) error {
	return t.closePossibleMatches(ids, func(c Clarification) (bool, error) {
		for _, candidate := range candidateIDs(c) {
			open, err := t.stillCandidate(*c.TransactionID, candidate)
			if err != nil || open {
				return false, err
			}
		}
		return true, nil
	}, reasonRejected, actor.Author)
}

func (t *tx) stillCandidate(transactionID, candidateID uuid.UUID) (bool, error) {
	candidate, err := t.transaction(candidateID)
	if err != nil {
		return false, err
	}
	if candidate.Status == StatusMerged && candidate.MergedIntoID != nil {
		return t.stillCandidate(transactionID, *candidate.MergedIntoID)
	}
	if candidate.Status == StatusVoided || candidate.ID == transactionID {
		return false, nil
	}
	rejected, err := t.rejected(transactionID, candidateID)
	return !rejected, err
}

func (t *tx) dismissPossibleMatch(clarification tenantdb.Clarification, note string, actor Actor) error {
	if clarification.TransactionID == nil {
		return nil
	}
	closed, err := t.withTarget(clarification)
	if err != nil {
		return err
	}
	for _, candidate := range candidateIDs(closed) {
		if err := t.rejectPair(*clarification.TransactionID, candidate, note, actor); err != nil {
			return err
		}
	}
	return t.settleStatus(*clarification.TransactionID)
}

func (t *tx) settleStatus(transactionID uuid.UUID) error {
	current, err := t.transaction(transactionID)
	if err != nil {
		return err
	}
	if current.Status == StatusVoided || current.Status == StatusMerged {
		return nil
	}
	open, err := t.openPossibleMatches([]uuid.UUID{transactionID})
	if err != nil {
		return err
	}
	next := StatusPendingClarification
	if len(open) == 0 {
		probe := current
		probe.Status = StatusProvisional
		if next, err = t.statusFollowingPayment(probe); err != nil {
			return err
		}
	}
	if next == current.Status {
		return nil
	}
	if _, err := t.q.SetTransactionStatus(t.ctx, tenantdb.SetTransactionStatusParams{AccountID: t.accountID, ID: transactionID, Status: string(next)}); err != nil {
		return fmt.Errorf("set status: %w", err)
	}
	return t.recordStatusRevision(transactionID, current.Status, next, reasonMatchSettled)
}
