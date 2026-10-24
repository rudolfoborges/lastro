package ledger

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	subjectStatementLine = "statement_line"
	reasonStatementLink  = "Conferido com a fatura."
	reasonStatementCheck = "Confirmado pela fatura."
)

var errDryRun = errors.New("dry run: roll back")

type statementSubmitter struct {
	t          *tx
	in         StatementExtraction
	identities []itemIdentity
	loc        *time.Location
	actor      Actor
	receivedAt time.Time
}

func (s *Service) SubmitStatement(ctx context.Context, accountID uuid.UUID, in StatementExtraction, receivedAt time.Time, actor Actor) (StatementSubmission, error) {
	in, err := normalizeExtraction(in)
	if err != nil {
		return StatementSubmission{}, err
	}
	var out StatementSubmission
	err = s.inTx(ctx, accountID, func(t *tx) error {
		loc, err := t.location()
		if err != nil {
			return err
		}
		sub := &statementSubmitter{t: t, in: in, identities: itemIdentities(in), loc: loc, actor: actor, receivedAt: receivedAt}
		out, err = sub.run()
		out.Location = loc
		if err == nil && in.DryRun && (out.Result == StatementRecorded || out.Result == StatementNewVersion) {
			out.Result = StatementPreviewed
			return errDryRun
		}
		return err
	})
	if errors.Is(err, errDryRun) {
		return out, nil
	}
	return out, err
}

func (s *statementSubmitter) run() (StatementSubmission, error) {
	hash := contentHash(s.in, s.identities)
	existing, err := s.t.q.LockStatementByIdentity(s.t.ctx, s.identity())
	switch {
	case err == nil:
		return s.resubmitted(existing, hash)
	case !errors.Is(err, pgx.ErrNoRows):
		return StatementSubmission{}, fmt.Errorf("find statement: %w", err)
	}
	candidates, err := s.candidates(func(int) bool { return true })
	if err != nil || len(candidates) > 0 {
		return StatementSubmission{Result: StatementPossibleMatch, Candidates: candidates}, err
	}
	method, err := s.t.ensurePaymentMethod(PaymentMethodRef{Kind: PaymentCreditCard, CardLast4: &s.in.CardLast4})
	if err != nil {
		return StatementSubmission{}, err
	}
	identity := s.identity()
	row, err := s.t.q.InsertStatement(s.t.ctx, tenantdb.InsertStatementParams{
		ID:              store.NewID(),
		AccountID:       s.t.accountID,
		Issuer:          s.in.Issuer,
		IssuerKey:       identity.IssuerKey,
		CardLast4:       identity.CardLast4,
		ClosingDate:     identity.ClosingDate,
		PaymentMethodID: method.ID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		existing, err := s.t.q.LockStatementByIdentity(s.t.ctx, identity)
		if err != nil {
			return StatementSubmission{}, fmt.Errorf("find statement recorded concurrently: %w", err)
		}
		return s.resubmitted(existing, hash)
	}
	if err != nil {
		return StatementSubmission{}, fmt.Errorf("insert statement: %w", err)
	}
	version, err := s.insertVersion(row.ID, 1, hash)
	if err != nil {
		return StatementSubmission{}, err
	}
	lines := make([]StatementLine, len(s.in.Items))
	for i, item := range s.in.Items {
		if lines[i], err = s.record(row, version, i, item); err != nil {
			return StatementSubmission{}, prefixed(fmt.Sprintf("items[%d].", i), err)
		}
	}
	for _, doubt := range s.in.Doubts {
		if err := s.t.openClarification(statementDoubt(row.ID, doubt)); err != nil {
			return StatementSubmission{}, err
		}
	}
	return s.submission(StatementRecorded, row, version, lines)
}

func (s *statementSubmitter) identity() tenantdb.LockStatementByIdentityParams {
	return tenantdb.LockStatementByIdentityParams{
		AccountID:   s.t.accountID,
		IssuerKey:   issuerKey(s.in.Issuer),
		CardLast4:   s.in.CardLast4,
		ClosingDate: s.in.ClosingDate.Time(),
	}
}

func (s *statementSubmitter) resubmitted(statement tenantdb.Statement, hash []byte) (StatementSubmission, error) {
	version, err := s.t.q.LatestStatementVersion(s.t.ctx, tenantdb.LatestStatementVersionParams{AccountID: s.t.accountID, StatementID: statement.ID})
	if err != nil {
		return StatementSubmission{}, fmt.Errorf("latest statement version: %w", err)
	}
	if !bytes.Equal(version.ContentHash, hash) {
		return s.newVersion(statement, version, hash)
	}
	if err := s.planInstallments(statement); err != nil {
		return StatementSubmission{}, err
	}
	lines, err := s.t.statementLines(version.ID)
	if err != nil {
		return StatementSubmission{}, err
	}
	return s.submission(StatementAlreadyRecorded, statement, version, lines)
}

// planInstallments gives installment lines recorded before installment plans
// existed their purchase and plan when the same reading is sent again.
func (s *statementSubmitter) planInstallments(statement tenantdb.Statement) error {
	for i, item := range s.in.Items {
		if !item.Kind.installment() {
			continue
		}
		observation, err := s.t.q.GetObservationBySourceKey(s.t.ctx, tenantdb.GetObservationBySourceKeyParams{AccountID: s.t.accountID, Source: string(SourceStatementLine), SourceKey: s.identities[i].sourceKey(statement.ID)})
		if err != nil {
			return fmt.Errorf("find installment line: %w", err)
		}
		transactionID, err := s.t.q.ObservationTransaction(s.t.ctx, tenantdb.ObservationTransactionParams{AccountID: s.t.accountID, ObservationID: observation.ID})
		switch {
		case err == nil:
			plan, err := s.t.planRow(transactionID)
			if err != nil {
				return err
			}
			if plan == nil {
				if err := s.charge(statement, transactionID, observation.ID, item); err != nil {
					return prefixed(fmt.Sprintf("items[%d].", i), err)
				}
			}
			continue
		case !errors.Is(err, pgx.ErrNoRows):
			return fmt.Errorf("transaction of an installment line: %w", err)
		}
		if _, err := s.installment(statement, observation.ID, item, false); err != nil {
			return prefixed(fmt.Sprintf("items[%d].", i), err)
		}
	}
	return nil
}

func (s *statementSubmitter) insertVersion(statementID uuid.UUID, number int32, hash []byte) (tenantdb.StatementVersion, error) {
	params := tenantdb.InsertStatementVersionParams{
		ID:                   store.NewID(),
		AccountID:            s.t.accountID,
		StatementID:          statementID,
		StatementVersion:     number,
		IssueDate:            dateTime(s.in.IssueDate),
		DueDate:              s.in.DueDate.Time(),
		PeriodStart:          dateTime(s.in.PeriodStart),
		PeriodEnd:            dateTime(s.in.PeriodEnd),
		Currency:             s.in.Total.Currency,
		InformedTotalMinor:   s.in.Total.Minor,
		Reading:              string(s.in.Reading),
		Missing:              optionalString(s.in.Missing),
		ContentHash:          hash,
		FileSha256:           s.in.FileSHA256,
		PreviousBalanceMinor: nil,
	}
	if s.in.PreviousBalance != nil {
		params.PreviousBalanceMinor = &s.in.PreviousBalance.Minor
	}
	if s.in.PageCount != nil {
		count := int32(*s.in.PageCount) //nolint:gosec // bounded by validation
		params.PageCount = &count
	}
	version, err := s.t.q.InsertStatementVersion(s.t.ctx, params)
	if err != nil {
		return tenantdb.StatementVersion{}, fmt.Errorf("insert statement version: %w", err)
	}
	return version, nil
}

func (s *statementSubmitter) record(statement tenantdb.Statement, version tenantdb.StatementVersion, index int, item StatementItem) (StatementLine, error) {
	key := s.identities[index].sourceKey(statement.ID)
	existing, err := s.t.q.GetObservationBySourceKey(s.t.ctx, tenantdb.GetObservationBySourceKeyParams{AccountID: s.t.accountID, Source: string(SourceStatementLine), SourceKey: key})
	switch {
	case err == nil:
		return s.returned(statement, version, index, item, existing.ID)
	case !errors.Is(err, pgx.ErrNoRows):
		return StatementLine{}, fmt.Errorf("find line of an earlier version: %w", err)
	}
	observation, err := s.t.recordObservation(NewObservation{
		Source:     SourceStatementLine,
		SourceKey:  key,
		Payload:    s.linePayload(statement, item),
		ReceivedAt: s.receivedAt,
	})
	if err != nil {
		return StatementLine{}, err
	}
	line, err := s.apply(statement, observation.ID, item)
	if err != nil {
		return StatementLine{}, err
	}
	return s.insertLine(version, index, observation.ID, item, line)
}

func (s *statementSubmitter) insertLine(version tenantdb.StatementVersion, index int, observationID uuid.UUID, item StatementItem, line StatementLine) (StatementLine, error) {
	line.Position, line.ObservationID = index+1, observationID
	line.Kind, line.Direction, line.Amount, line.Page = item.Kind, item.Direction, item.Amount, item.Page
	line.Excerpt = item.Excerpt
	details, err := json.Marshal(map[string]any{"effect": line.Effect})
	if err != nil {
		return StatementLine{}, fmt.Errorf("encode line details: %w", err)
	}
	err = s.t.q.InsertStatementLine(s.t.ctx, tenantdb.InsertStatementLineParams{
		ID:                 store.NewID(),
		AccountID:          s.t.accountID,
		StatementVersionID: version.ID,
		ObservationID:      observationID,
		Position:           int32(line.Position), //nolint:gosec // bounded by maxStatementItems
		Kind:               string(item.Kind),
		Direction:          string(item.Direction),
		AmountMinor:        item.Amount.Minor,
		Page:               int32(item.Page), //nolint:gosec // bounded by validation
		Excerpt:            line.Excerpt,
		Details:            details,
	})
	if err != nil {
		return StatementLine{}, fmt.Errorf("insert statement line: %w", err)
	}
	return line, nil
}

func (s *statementSubmitter) linePayload(statement tenantdb.Statement, item StatementItem) map[string]any {
	payload := map[string]any{
		"statement_id":         statement.ID.String(),
		"kind":                 item.Kind,
		"direction":            item.Direction,
		"original_description": item.OriginalDescription,
		"amount":               item.Amount.Decimal(),
		"currency":             item.Amount.Currency,
		"card_last4":           s.lineCard(item),
		"page":                 item.Page,
		"excerpt":              item.Excerpt,
	}
	if item.Description != "" {
		payload["description"] = item.Description
	}
	if item.Merchant != nil {
		payload["merchant"] = *item.Merchant
	}
	if item.OriginalAmount != nil {
		payload["original_amount"], payload["original_currency"] = item.OriginalAmount.Decimal(), item.OriginalAmount.Currency
	}
	if item.Date != nil {
		payload["date"] = item.Date.String()
	}
	if item.Category != nil {
		payload["category"] = *item.Category
	}
	if item.Purpose != nil {
		payload["purpose"] = *item.Purpose
	}
	if item.Installment != nil {
		payload["installment_number"], payload["installment_count"] = item.Installment.Number, item.Installment.Count
	}
	if len(item.Components) > 0 {
		components := make([]map[string]any, len(item.Components))
		for i, c := range item.Components {
			components[i] = map[string]any{"kind": c.Kind, "amount": c.Amount.Decimal(), "included": c.Included}
		}
		payload["components"] = components
	}
	return payload
}

func (s *statementSubmitter) lineCard(item StatementItem) string {
	if item.CardLast4 != nil {
		return *item.CardLast4
	}
	return s.in.CardLast4
}

func (s *statementSubmitter) apply(statement tenantdb.Statement, observationID uuid.UUID, item StatementItem) (StatementLine, error) {
	switch {
	case item.Kind.installment():
		return s.installment(statement, observationID, item, true)
	case item.RelatedTransactionID != nil:
		return s.link(observationID, item)
	case item.Kind == ItemUnknown:
		return StatementLine{Effect: EffectNone}, s.t.openClarification(s.unknownItem(statement.ID, observationID, item))
	default:
		return s.create(observationID, item)
	}
}

func (s *statementSubmitter) create(observationID uuid.UUID, item StatementItem) (StatementLine, error) {
	var candidates []MatchCandidate
	if item.ConfirmDistinct || item.DeferMatch {
		var err error
		if candidates, err = s.itemCandidates(item, map[uuid.UUID]bool{}); err != nil {
			return StatementLine{}, err
		}
	}
	description := item.Description
	if description == "" {
		description = item.OriginalDescription
	}
	card := s.lineCard(item)
	transaction := NewTransaction{
		OriginalDescription: item.OriginalDescription,
		Description:         description,
		Merchant:            item.Merchant,
		Amount:              item.Amount,
		OriginalAmount:      item.OriginalAmount,
		Occurred:            itemOccurrence(item),
		PaymentMethod:       &PaymentMethodRef{Kind: PaymentCreditCard, CardLast4: &card},
		Nature:              itemNatures[item.Kind],
		Status:              StatusConfirmed,
		Purpose:             item.Purpose,
	}
	var original *Transaction
	if item.RefundOfID != nil {
		purchase, err := s.t.refundable(*item.RefundOfID, item.Amount, "refund_of_id")
		if err != nil {
			return StatementLine{}, err
		}
		original, transaction.RefundOfID = &purchase, &purchase.ID
	}
	created, err := s.t.createTransaction(observationID, transaction, s.actor)
	if err != nil {
		return StatementLine{}, err
	}
	category := item.Category
	if category == nil && original != nil {
		current, err := s.t.currentCategory(original.ID)
		if err != nil {
			return StatementLine{}, err
		}
		if current != nil {
			category = &current.Code
		}
	}
	if category != nil {
		if _, err := s.t.categorize(created, *category, AllocationSuggested, reasonClassified, s.actor); err != nil {
			return StatementLine{}, err
		}
	}
	if err := s.t.settleCandidates(created, candidates, item.DeferMatch, s.actor); err != nil {
		return StatementLine{}, err
	}
	if err := s.t.refreshClarifications(created.ID); err != nil {
		return StatementLine{}, err
	}
	return s.withTransaction(StatementLine{Effect: EffectCreated}, created.ID)
}

func (s *statementSubmitter) link(observationID uuid.UUID, item StatementItem) (StatementLine, error) {
	return s.linkWith(observationID, item, OriginAgent, "same transaction, identified by Case")
}

func (s *statementSubmitter) linkWith(observationID uuid.UUID, item StatementItem, origin Origin, reason string) (StatementLine, error) {
	transactionID := *item.RelatedTransactionID
	current, err := s.t.lockOpenTransaction(transactionID)
	switch {
	case errors.Is(err, ErrNotFound):
		return StatementLine{}, &ValidationError{Field: "related_transaction_id", Message: "is not a transaction of Rudolfo's account"}
	case errors.Is(err, ErrTransactionClosed):
		return StatementLine{}, &ValidationError{Field: "related_transaction_id", Message: "is voided or merged; link the line to the transaction that is still open"}
	case err != nil:
		return StatementLine{}, err
	}
	if nature := itemNatures[item.Kind]; current.Nature != nature {
		return StatementLine{}, &ValidationError{Field: "related_transaction_id", Message: fmt.Sprintf("must be a transaction of nature %s to match a %s line", nature, item.Kind)}
	}
	if !item.Kind.installment() {
		charged, err := s.t.q.HasConfirmedEvidenceFrom(s.t.ctx, tenantdb.HasConfirmedEvidenceFromParams{AccountID: s.t.accountID, TransactionID: transactionID, Source: string(SourceStatementLine)})
		if err != nil {
			return StatementLine{}, fmt.Errorf("check statement evidence: %w", err)
		}
		if charged {
			return StatementLine{}, &ValidationError{Field: "related_transaction_id", Message: "is already charged by another statement line; two statement lines are two charges"}
		}
	}
	if _, err := s.t.linkEvidence(transactionID, observationID, EvidenceLink{Status: EvidenceConfirmed, Origin: origin, Reasons: []string{reason}}); err != nil {
		return StatementLine{}, err
	}
	actor := s.actor
	actor.ObservationID = &observationID
	correction, err := s.t.correct(transactionID, s.gapPatch(current, item), reasonStatementLink, actor)
	if err != nil {
		return StatementLine{}, err
	}
	if item.Category != nil {
		existing, err := s.t.currentCategory(transactionID)
		if err != nil {
			return StatementLine{}, err
		}
		if existing == nil {
			if _, err := s.t.categorize(correction.Transaction, *item.Category, AllocationSuggested, reasonClassified, actor); err != nil {
				return StatementLine{}, err
			}
		}
	}
	if err := s.confirmCharged(correction.Transaction); err != nil {
		return StatementLine{}, err
	}
	if err := s.t.refreshClarifications(transactionID); err != nil {
		return StatementLine{}, err
	}
	return s.withTransaction(StatementLine{Effect: EffectLinked, Changed: correction.Changed, Conflicts: correction.Conflicts}, transactionID)
}

func (s *statementSubmitter) gapPatch(current Transaction, item StatementItem) Patch {
	var patch Patch
	if !item.Kind.installment() && current.Amount.Currency == item.Amount.Currency && current.Amount.Minor != item.Amount.Minor {
		patch.Amount = &item.Amount
	}
	if current.Merchant == nil && item.Merchant != nil {
		patch.Merchant = Value(*item.Merchant)
	}
	if item.Date != nil && (current.Occurred.Precision == calendar.PrecisionUnknown || current.Occurred.Precision == calendar.PrecisionMonth) {
		occurred := itemOccurrence(item)
		patch.Occurred = &occurred
	}
	if method := current.PaymentMethod; method == nil || (method.Kind == PaymentCreditCard && method.CardLast4 == nil) {
		card := s.lineCard(item)
		patch.PaymentMethod = Value(PaymentMethodRef{Kind: PaymentCreditCard, CardLast4: &card})
	}
	if current.Purpose == nil && item.Purpose != nil {
		patch.Purpose = Value(*item.Purpose)
	}
	if current.Installments == nil && item.Installment != nil && item.Kind.installment() {
		patch.Installments = Value(Installments{Count: item.Installment.Count, Amount: &item.Amount})
	}
	return patch
}

func (s *statementSubmitter) confirmCharged(transaction Transaction) error {
	if transaction.Status != StatusProvisional {
		return nil
	}
	next := transaction
	next.Status = StatusConfirmed
	if _, err := s.t.saveTransaction(next); err != nil {
		return err
	}
	return s.t.recordStatusRevision(transaction.ID, transaction.Status, next.Status, reasonStatementCheck)
}

func (s *statementSubmitter) withTransaction(line StatementLine, transactionID uuid.UUID) (StatementLine, error) {
	transaction, err := s.t.transaction(transactionID)
	if err != nil {
		return StatementLine{}, err
	}
	category, err := s.t.effectiveCategory(transactionID)
	if err != nil {
		return StatementLine{}, err
	}
	line.Transaction, line.Category = &transaction, category
	return line, nil
}

func (s *statementSubmitter) candidates(eligible func(index int) bool) ([]ItemCandidates, error) {
	linked := map[uuid.UUID]bool{}
	for _, item := range s.in.Items {
		if item.RelatedTransactionID != nil {
			linked[*item.RelatedTransactionID] = true
		}
	}
	var out []ItemCandidates
	for i, item := range s.in.Items {
		if !item.Kind.matchable() || item.RelatedTransactionID != nil || item.ConfirmDistinct || item.DeferMatch || !eligible(i) {
			continue
		}
		var found []MatchCandidate
		var err error
		if item.Kind.installment() {
			found, err = s.installmentCandidates(item)
		} else {
			found, err = s.itemCandidates(item, linked)
		}
		if err != nil {
			return nil, err
		}
		if len(found) > 0 {
			out = append(out, ItemCandidates{Position: i + 1, Candidates: found})
		}
	}
	return out, nil
}

func (s *statementSubmitter) itemCandidates(item StatementItem, linked map[uuid.UUID]bool) ([]MatchCandidate, error) {
	card := s.lineCard(item)
	probe := matchProbe{
		nature:          itemNatures[item.Kind],
		amount:          item.Amount,
		originalAmount:  item.OriginalAmount,
		merchant:        item.Merchant,
		occurred:        itemOccurrence(item),
		method:          &PaymentMethodRef{Kind: PaymentCreditCard, CardLast4: &card},
		excludedIDs:     make([]uuid.UUID, 0, len(linked)),
		excludedSources: []Source{SourceStatementLine},
		paymentKinds:    []PaymentKind{PaymentCreditCard},
		label:           "na fatura",
	}
	for id := range linked {
		probe.excludedIDs = append(probe.excludedIDs, id)
	}
	probe.from, probe.to = s.window(item)
	return s.t.findCandidates(probe, s.loc)
}

func (s *statementSubmitter) window(item StatementItem) (calendar.Date, calendar.Date) {
	if item.Date != nil {
		return item.Date.AddDays(-statementMatchDays), item.Date.AddDays(statementMatchDays)
	}
	from, to := s.in.ClosingDate.AddDays(-statementLookback), s.in.ClosingDate
	if s.in.PeriodStart != nil {
		from = *s.in.PeriodStart
	}
	if s.in.PeriodEnd != nil {
		to = *s.in.PeriodEnd
	}
	return from, to
}

func (s *statementSubmitter) unknownItem(statementID, observationID uuid.UUID, item StatementItem) newClarification {
	question := item.Question
	if question == "" {
		question = fmt.Sprintf("Não entendi este item da fatura (página %d): \"%s\", %s. O que é?", item.Page, truncateRunes(item.Excerpt, 200), item.Amount.Format())
	}
	return newClarification{
		kind:          ClarificationReadingDoubt,
		observationID: &observationID,
		statementID:   &statementID,
		subject:       subjectStatementLine,
		question:      truncateRunes(question, maxQuestion),
		context:       map[string]any{"page": item.Page, "excerpt": item.Excerpt, "amount": item.Amount.Decimal(), "currency": item.Amount.Currency},
	}
}

func statementDoubt(statementID uuid.UUID, doubt ReadingDoubt) newClarification {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%d\x1f%s\x1f%s", doubt.Page, normalizedText(doubt.Excerpt), normalizedText(doubt.Question))))
	return newClarification{
		kind:        ClarificationReadingDoubt,
		statementID: &statementID,
		subject:     "reading:" + hex.EncodeToString(sum[:6]),
		question:    doubt.Question,
		context:     map[string]any{"page": doubt.Page, "excerpt": doubt.Excerpt},
	}
}

func itemOccurrence(item StatementItem) calendar.Occurrence {
	if item.Date == nil {
		return calendar.Unknown()
	}
	return calendar.OnDay(*item.Date, calendar.SourceDocument)
}

func (s *statementSubmitter) submission(result StatementResult, row tenantdb.Statement, version tenantdb.StatementVersion, lines []StatementLine) (StatementSubmission, error) {
	review, err := s.t.review(row, version)
	if err != nil {
		return StatementSubmission{}, err
	}
	return StatementSubmission{Result: result, Statement: &review.Statement, Lines: lines, Review: &review}, nil
}

func (t *tx) statementLines(versionID uuid.UUID) ([]StatementLine, error) {
	rows, err := t.q.ListStatementLines(t.ctx, tenantdb.ListStatementLinesParams{AccountID: t.accountID, StatementVersionID: versionID})
	if err != nil {
		return nil, fmt.Errorf("list statement lines: %w", err)
	}
	version, err := t.q.GetStatementVersion(t.ctx, tenantdb.GetStatementVersionParams{AccountID: t.accountID, ID: versionID})
	if err != nil {
		return nil, fmt.Errorf("statement version %s: %w", versionID, notFound(err))
	}
	lines := make([]StatementLine, len(rows))
	for i, row := range rows {
		var details struct {
			Effect InterpretationEffect `json:"effect"`
		}
		if err := json.Unmarshal(row.Details, &details); err != nil {
			return nil, fmt.Errorf("decode line details: %w", err)
		}
		lines[i] = StatementLine{
			Position:      int(row.Position),
			ObservationID: row.ObservationID,
			Kind:          StatementItemKind(row.Kind),
			Direction:     Direction(row.Direction),
			Amount:        money.Money{Minor: row.AmountMinor, Currency: version.Currency},
			Page:          int(row.Page),
			Excerpt:       row.Excerpt,
			Effect:        details.Effect,
		}
		if row.TransactionID != nil {
			transaction, err := t.transaction(*row.TransactionID)
			if err != nil {
				return nil, err
			}
			category, err := t.effectiveCategory(transaction.ID)
			if err != nil {
				return nil, err
			}
			lines[i].Transaction, lines[i].Category = &transaction, category
		}
	}
	return lines, nil
}

func toStatement(row tenantdb.Statement, version tenantdb.StatementVersion) Statement {
	out := Statement{
		ID:          row.ID,
		Issuer:      row.Issuer,
		CardLast4:   row.CardLast4,
		ClosingDate: calendar.DateFromTime(row.ClosingDate),
		Version: StatementVersion{
			ID:        version.ID,
			Number:    int(version.Version),
			DueDate:   calendar.DateFromTime(version.DueDate),
			Total:     money.Money{Minor: version.InformedTotalMinor, Currency: version.Currency},
			Reading:   Reading(version.Reading),
			Missing:   version.Missing,
			CreatedAt: version.CreatedAt,
		},
	}
	out.Version.IssueDate = optionalDate(version.IssueDate)
	out.Version.PeriodStart = optionalDate(version.PeriodStart)
	out.Version.PeriodEnd = optionalDate(version.PeriodEnd)
	if version.PreviousBalanceMinor != nil {
		out.Version.PreviousBalance = &money.Money{Minor: *version.PreviousBalanceMinor, Currency: version.Currency}
	}
	if version.PageCount != nil {
		count := int(*version.PageCount)
		out.Version.PageCount = &count
	}
	return out
}

func optionalDate(value *time.Time) *calendar.Date {
	if value == nil {
		return nil
	}
	date := calendar.DateFromTime(*value)
	return &date
}

func optionalString(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}
