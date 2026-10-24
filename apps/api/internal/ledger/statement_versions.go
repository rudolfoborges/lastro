package ledger

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

const (
	subjectRemovedLine  = "removed_line"
	reasonVersionAmount = "Valor alterado em uma nova versão da fatura."
	resolutionReplaced  = "A linha mudou em uma nova versão da fatura."
	resolutionReread    = "A nova leitura da fatura não traz mais esta dúvida."
	resolutionReturned  = "A linha voltou em uma nova versão da fatura."
	maxDiffContextLines = 20
)

type VersionCause string

const (
	CauseReissued VersionCause = "reissued"
	CauseReread   VersionCause = "reread"
	CauseUnknown  VersionCause = "unknown"
)

type DiffLine struct {
	Position      int
	Kind          StatementItemKind
	Description   string
	Amount        money.Money
	Date          *calendar.Date
	TransactionID *uuid.UUID
}

type DiffChange struct {
	Before DiffLine
	After  DiffLine
	Fields []string
}

type MetadataChange struct {
	Field  string
	Before string
	After  string
}

type StatementDiff struct {
	FromVersion int
	ToVersion   int
	Cause       VersionCause
	Added       []DiffLine
	Removed     []DiffLine
	Changed     []DiffChange
	Metadata    []MetadataChange
}

type priorLine struct {
	DiffLine
	ObservationID uuid.UUID
	key           string
	loose         string
}

type itemPlan int

const (
	planAdded itemPlan = iota
	planKept
	planChanged
)

type versionPlan struct {
	items   []itemPlan
	prior   []*priorLine
	removed []priorLine
}

func (s *statementSubmitter) newVersion(statement tenantdb.Statement, previous tenantdb.StatementVersion, hash []byte) (StatementSubmission, error) {
	before, err := s.priorLines(previous)
	if err != nil {
		return StatementSubmission{}, err
	}
	plan := s.plan(statement.ID, before)
	candidates, err := s.candidates(func(i int) bool {
		return plan.items[i] == planAdded || (plan.items[i] == planChanged && plan.prior[i].TransactionID == nil)
	})
	if err != nil || len(candidates) > 0 {
		return StatementSubmission{Result: StatementPossibleMatch, Candidates: candidates}, err
	}
	version, err := s.insertVersion(statement.ID, previous.Version+1, hash)
	if err != nil {
		return StatementSubmission{}, err
	}
	diff := s.diff(previous, version, plan)
	lines := make([]StatementLine, len(s.in.Items))
	for i, item := range s.in.Items {
		if lines[i], err = s.recordVersionLine(statement, version, i, item, plan); err != nil {
			return StatementSubmission{}, prefixed(fmt.Sprintf("items[%d].", i), err)
		}
		if plan.items[i] == planAdded {
			diff.Added[addedIndex(diff.Added, i+1)].TransactionID = transactionIDOf(lines[i])
		}
	}
	if err := s.flagRemoved(statement, int(version.Version), plan.removed); err != nil {
		return StatementSubmission{}, err
	}
	if err := s.refreshStatementDoubts(statement.ID); err != nil {
		return StatementSubmission{}, err
	}
	if err := s.t.openClarification(versionDoubt(statement, diff)); err != nil {
		return StatementSubmission{}, err
	}
	out, err := s.submission(StatementNewVersion, statement, version, lines)
	out.Diff = &diff
	return out, err
}

func (s *statementSubmitter) priorLines(version tenantdb.StatementVersion) ([]priorLine, error) {
	rows, err := s.t.q.StatementVersionLines(s.t.ctx, tenantdb.StatementVersionLinesParams{AccountID: s.t.accountID, StatementVersionID: version.ID})
	if err != nil {
		return nil, fmt.Errorf("lines of the previous version: %w", err)
	}
	lines := make([]priorLine, len(rows))
	for i, row := range rows {
		var payload struct {
			OriginalDescription string `json:"original_description"`
			CardLast4           string `json:"card_last4"`
			Date                string `json:"date"`
			InstallmentNumber   int    `json:"installment_number"`
			InstallmentCount    int    `json:"installment_count"`
		}
		if err := json.Unmarshal(row.Payload, &payload); err != nil {
			return nil, fmt.Errorf("decode line payload: %w", err)
		}
		line := priorLine{
			DiffLine: DiffLine{
				Position:      int(row.Position),
				Kind:          StatementItemKind(row.Kind),
				Description:   payload.OriginalDescription,
				Amount:        money.Money{Minor: row.AmountMinor, Currency: version.Currency},
				TransactionID: row.TransactionID,
			},
			ObservationID: row.ObservationID,
			key:           row.SourceKey,
		}
		if payload.Date != "" {
			if date, err := calendar.ParseDate(payload.Date); err == nil {
				line.Date = &date
			}
		}
		var installment *InstallmentPosition
		if payload.InstallmentCount > 0 {
			installment = &InstallmentPosition{Number: payload.InstallmentNumber, Count: payload.InstallmentCount}
		}
		line.loose = looseKey(payload.OriginalDescription, payload.CardLast4, line.Date, installment)
		lines[i] = line
	}
	return lines, nil
}

func (s *statementSubmitter) plan(statementID uuid.UUID, before []priorLine) versionPlan {
	byKey := map[string]*priorLine{}
	for i := range before {
		byKey[before[i].key] = &before[i]
	}
	plan := versionPlan{items: make([]itemPlan, len(s.in.Items)), prior: make([]*priorLine, len(s.in.Items))}
	matched := map[string]bool{}
	for i := range s.in.Items {
		key := s.identities[i].sourceKey(statementID)
		if prior, ok := byKey[key]; ok {
			plan.items[i], plan.prior[i] = planKept, prior
			matched[key] = true
		}
	}
	for i, item := range s.in.Items {
		if plan.items[i] != planAdded {
			continue
		}
		loose := looseKey(item.OriginalDescription, s.lineCard(item), item.Date, item.Installment)
		for j := range before {
			if !matched[before[j].key] && before[j].loose == loose {
				plan.items[i], plan.prior[i] = planChanged, &before[j]
				matched[before[j].key] = true
				break
			}
		}
	}
	for _, prior := range before {
		if !matched[prior.key] {
			plan.removed = append(plan.removed, prior)
		}
	}
	return plan
}

func (s *statementSubmitter) recordVersionLine(statement tenantdb.Statement, version tenantdb.StatementVersion, index int, item StatementItem, plan versionPlan) (StatementLine, error) {
	prior := plan.prior[index]
	switch {
	case plan.items[index] == planKept && prior.TransactionID == nil && item.Kind.installment():
		line, err := s.installment(statement, prior.ObservationID, item, false)
		if err != nil {
			return StatementLine{}, err
		}
		return s.insertLine(version, index, prior.ObservationID, item, line)
	case plan.items[index] == planKept:
		line := StatementLine{Effect: EffectNone}
		if prior.TransactionID != nil {
			var err error
			if line, err = s.withTransaction(line, *prior.TransactionID); err != nil {
				return StatementLine{}, err
			}
		}
		return s.insertLine(version, index, prior.ObservationID, item, line)
	case plan.items[index] == planChanged && prior.TransactionID != nil:
		observation, err := s.t.recordObservation(NewObservation{
			Source:     SourceStatementLine,
			SourceKey:  s.identities[index].sourceKey(statement.ID),
			Payload:    s.linePayload(statement, item),
			ReceivedAt: s.receivedAt,
		})
		if err != nil {
			return StatementLine{}, err
		}
		if err := s.recordComponents(statement.ID, observation.ID, item); err != nil {
			return StatementLine{}, err
		}
		line, err := s.moveEvidence(*prior, observation.ID, item)
		if err != nil {
			return StatementLine{}, err
		}
		return s.insertLine(version, index, observation.ID, item, line)
	default:
		if prior != nil {
			if err := s.closeLineDoubt(statement.ID, prior.ObservationID); err != nil {
				return StatementLine{}, err
			}
		}
		return s.record(statement, version, index, item)
	}
}

func (s *statementSubmitter) returned(statement tenantdb.Statement, version tenantdb.StatementVersion, index int, item StatementItem, observationID uuid.UUID) (StatementLine, error) {
	transactionID, err := s.t.q.ObservationTransaction(s.t.ctx, tenantdb.ObservationTransactionParams{AccountID: s.t.accountID, ObservationID: observationID})
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		line, err := s.apply(statement, observationID, item)
		if err != nil {
			return StatementLine{}, err
		}
		return s.insertLine(version, index, observationID, item, line)
	case err != nil:
		return StatementLine{}, fmt.Errorf("transaction of an earlier line: %w", err)
	}
	removed := dedupKey(newClarification{kind: ClarificationStatementVersionDiff, transactionID: &transactionID, observationID: &observationID, statementID: &statement.ID, subject: subjectRemovedLine})
	row, err := s.t.q.GetOpenClarificationByDedupKey(s.t.ctx, tenantdb.GetOpenClarificationByDedupKeyParams{AccountID: s.t.accountID, DedupKey: removed})
	switch {
	case err == nil:
		if _, err := s.t.closeClarification(row, ClarificationResolved, resolutionReturned, AuthorSystem); err != nil {
			return StatementLine{}, err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return StatementLine{}, fmt.Errorf("find removed line question: %w", err)
	}
	line, err := s.withTransaction(StatementLine{Effect: EffectNone}, transactionID)
	if err != nil {
		return StatementLine{}, err
	}
	return s.insertLine(version, index, observationID, item, line)
}

func (s *statementSubmitter) moveEvidence(prior priorLine, observationID uuid.UUID, item StatementItem) (StatementLine, error) {
	transactionID := *prior.TransactionID
	evidence, err := s.t.q.LockConfirmedEvidence(s.t.ctx, tenantdb.LockConfirmedEvidenceParams{AccountID: s.t.accountID, TransactionID: transactionID, ObservationID: prior.ObservationID})
	if err != nil {
		return StatementLine{}, fmt.Errorf("evidence of the previous line: %w", err)
	}
	if _, err := s.t.q.SetEvidenceStatus(s.t.ctx, tenantdb.SetEvidenceStatusParams{AccountID: s.t.accountID, ID: evidence.ID, Status: string(EvidenceUndone), Origin: string(OriginSystem)}); err != nil {
		return StatementLine{}, fmt.Errorf("undo evidence of the previous line: %w", err)
	}
	if _, err := s.t.linkEvidence(transactionID, observationID, EvidenceLink{Status: EvidenceConfirmed, Origin: OriginSystem, Reasons: []string{"same line in a new version of the statement"}}); err != nil {
		return StatementLine{}, err
	}
	if item.Kind.installment() {
		err := s.t.q.MoveChargedInstallment(s.t.ctx, tenantdb.MoveChargedInstallmentParams{AccountID: s.t.accountID, FromObservationID: &prior.ObservationID, ToObservationID: &observationID, AmountMinor: item.Amount.Minor})
		if err != nil {
			return StatementLine{}, fmt.Errorf("move charged installment: %w", err)
		}
	}
	current, err := s.t.lockOpenTransaction(transactionID)
	if errors.Is(err, ErrTransactionClosed) {
		return s.withTransaction(StatementLine{Effect: EffectNone}, transactionID)
	}
	if err != nil {
		return StatementLine{}, err
	}
	var patch Patch
	if !item.Kind.installment() && current.Amount.Currency == item.Amount.Currency && current.Amount != item.Amount {
		patch.Amount = &item.Amount
	}
	actor := s.actor
	actor.ObservationID = &observationID
	correction, err := s.t.correct(transactionID, patch, reasonVersionAmount, actor)
	if err != nil {
		return StatementLine{}, err
	}
	return s.withTransaction(StatementLine{Effect: EffectUpdated, Changed: correction.Changed, Conflicts: correction.Conflicts}, transactionID)
}

func (s *statementSubmitter) flagRemoved(statement tenantdb.Statement, version int, removed []priorLine) error {
	for _, line := range removed {
		if err := s.closeLineDoubt(statement.ID, line.ObservationID); err != nil {
			return err
		}
		if line.TransactionID == nil {
			continue
		}
		transaction, err := s.t.transaction(*line.TransactionID)
		if err != nil {
			return err
		}
		if transaction.Status == StatusVoided || transaction.Status == StatusMerged {
			continue
		}
		err = s.t.openClarification(newClarification{
			kind:          ClarificationStatementVersionDiff,
			transactionID: line.TransactionID,
			observationID: &line.ObservationID,
			statementID:   &statement.ID,
			subject:       subjectRemovedLine,
			question:      fmt.Sprintf("A versão %d da fatura com fechamento em %s não traz mais %s. O lançamento continua valendo ou deve ser anulado?", version, calendar.DateFromTime(statement.ClosingDate).Format(), describe(transaction)),
			context:       map[string]any{"version": version, "previous_position": line.Position},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *statementSubmitter) closeLineDoubt(statementID, observationID uuid.UUID) error {
	key := dedupKey(newClarification{kind: ClarificationReadingDoubt, observationID: &observationID, statementID: &statementID, subject: subjectStatementLine})
	row, err := s.t.q.GetOpenClarificationByDedupKey(s.t.ctx, tenantdb.GetOpenClarificationByDedupKeyParams{AccountID: s.t.accountID, DedupKey: key})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("find line doubt: %w", err)
	}
	_, err = s.t.closeClarification(row, ClarificationResolved, resolutionReplaced, AuthorSystem)
	return err
}

func (s *statementSubmitter) refreshStatementDoubts(statementID uuid.UUID) error {
	current := map[string]bool{}
	for _, doubt := range s.in.Doubts {
		clarification := statementDoubt(statementID, doubt)
		current[clarification.subject] = true
		if err := s.t.openClarification(clarification); err != nil {
			return err
		}
	}
	rows, err := s.t.q.OpenStatementClarifications(s.t.ctx, tenantdb.OpenStatementClarificationsParams{AccountID: s.t.accountID, StatementID: &statementID})
	if err != nil {
		return fmt.Errorf("list statement clarifications: %w", err)
	}
	for _, row := range rows {
		if ClarificationKind(row.Kind) != ClarificationReadingDoubt || row.ObservationID != nil || current[row.Subject] {
			continue
		}
		if _, err := s.t.closeClarification(row, ClarificationResolved, resolutionReread, AuthorSystem); err != nil {
			return err
		}
	}
	return nil
}

func (s *statementSubmitter) diff(previous, version tenantdb.StatementVersion, plan versionPlan) StatementDiff {
	diff := StatementDiff{FromVersion: int(previous.Version), ToVersion: int(version.Version), Cause: versionCause(previous.FileSha256, version.FileSha256)}
	for i, item := range s.in.Items {
		after := DiffLine{Position: i + 1, Kind: item.Kind, Description: item.OriginalDescription, Amount: item.Amount, Date: item.Date}
		switch prior := plan.prior[i]; plan.items[i] {
		case planAdded:
			diff.Added = append(diff.Added, after)
		case planKept:
			if prior.Kind != item.Kind {
				after.TransactionID = prior.TransactionID
				diff.Changed = append(diff.Changed, DiffChange{Before: prior.DiffLine, After: after, Fields: []string{"kind"}})
			}
		case planChanged:
			after.TransactionID = prior.TransactionID
			fields := []string{"amount"}
			if prior.Kind != item.Kind {
				fields = append(fields, "kind")
			}
			diff.Changed = append(diff.Changed, DiffChange{Before: prior.DiffLine, After: after, Fields: fields})
		}
	}
	for _, line := range plan.removed {
		diff.Removed = append(diff.Removed, line.DiffLine)
	}
	diff.Metadata = metadataChanges(previous, version)
	return diff
}

func metadataChanges(previous, version tenantdb.StatementVersion) []MetadataChange {
	text := func(value *string) string {
		if value == nil {
			return ""
		}
		return *value
	}
	amount := func(minor *int64, currency string) string {
		if minor == nil {
			return ""
		}
		return money.Money{Minor: *minor, Currency: currency}.Format()
	}
	date := func(value *calendar.Date) string {
		if value == nil {
			return ""
		}
		return value.Format()
	}
	pairs := []MetadataChange{
		{Field: "total", Before: amount(&previous.InformedTotalMinor, previous.Currency), After: amount(&version.InformedTotalMinor, version.Currency)},
		{Field: "previous_balance", Before: amount(previous.PreviousBalanceMinor, previous.Currency), After: amount(version.PreviousBalanceMinor, version.Currency)},
		{Field: "due_date", Before: calendar.DateFromTime(previous.DueDate).Format(), After: calendar.DateFromTime(version.DueDate).Format()},
		{Field: "issue_date", Before: date(optionalDate(previous.IssueDate)), After: date(optionalDate(version.IssueDate))},
		{Field: "period_start", Before: date(optionalDate(previous.PeriodStart)), After: date(optionalDate(version.PeriodStart))},
		{Field: "period_end", Before: date(optionalDate(previous.PeriodEnd)), After: date(optionalDate(version.PeriodEnd))},
		{Field: "reading", Before: previous.Reading, After: version.Reading},
		{Field: "missing", Before: text(previous.Missing), After: text(version.Missing)},
	}
	var changes []MetadataChange
	for _, pair := range pairs {
		if pair.Before != pair.After {
			changes = append(changes, pair)
		}
	}
	return changes
}

func versionCause(before, after []byte) VersionCause {
	switch {
	case before == nil || after == nil:
		return CauseUnknown
	case bytes.Equal(before, after):
		return CauseReread
	default:
		return CauseReissued
	}
}

func versionDoubt(statement tenantdb.Statement, diff StatementDiff) newClarification {
	var parts []string
	if n := len(diff.Added); n > 0 {
		parts = append(parts, fmt.Sprintf("%d linha(s) nova(s)", n))
	}
	if n := len(diff.Removed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d removida(s)", n))
	}
	if n := len(diff.Changed); n > 0 {
		parts = append(parts, fmt.Sprintf("%d alterada(s)", n))
	}
	for _, change := range diff.Metadata {
		if change.Field == "total" {
			parts = append(parts, fmt.Sprintf("total de %s para %s", change.Before, change.After))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "dados da fatura alterados")
	}
	cause := map[VersionCause]string{CauseReissued: "o banco emitiu um arquivo novo", CauseReread: "a Case releu o mesmo arquivo", CauseUnknown: "não se sabe se o arquivo é outro"}[diff.Cause]
	return newClarification{
		kind:        ClarificationStatementVersionDiff,
		statementID: &statement.ID,
		subject:     fmt.Sprintf("version:%d", diff.ToVersion),
		question: truncateRunes(fmt.Sprintf("A fatura com fechamento em %s mudou da versão %d para a %d (%s): %s. Confira as diferenças.",
			calendar.DateFromTime(statement.ClosingDate).Format(), diff.FromVersion, diff.ToVersion, cause, strings.Join(parts, ", ")), maxQuestion),
		context: diffContext(diff),
	}
}

func diffContext(diff StatementDiff) map[string]any {
	line := func(l DiffLine) map[string]any {
		out := map[string]any{"index": l.Position - 1, "kind": l.Kind, "description": l.Description, "amount": l.Amount.Decimal()}
		if l.Date != nil {
			out["date"] = l.Date.String()
		}
		return out
	}
	limited := func(lines []DiffLine) []map[string]any {
		out := []map[string]any{}
		for i, l := range lines {
			if i == maxDiffContextLines {
				break
			}
			out = append(out, line(l))
		}
		return out
	}
	changed := []map[string]any{}
	for i, change := range diff.Changed {
		if i == maxDiffContextLines {
			break
		}
		changed = append(changed, map[string]any{"before": line(change.Before), "after": line(change.After), "fields": change.Fields})
	}
	metadata := []map[string]any{}
	for _, change := range diff.Metadata {
		metadata = append(metadata, map[string]any{"field": change.Field, "before": change.Before, "after": change.After})
	}
	return map[string]any{
		"from_version": diff.FromVersion, "to_version": diff.ToVersion, "cause": diff.Cause,
		"added": limited(diff.Added), "removed": limited(diff.Removed), "changed": changed, "metadata": metadata,
	}
}

func looseKey(description, card string, date *calendar.Date, installment *InstallmentPosition) string {
	parts := []string{normalizedText(description), card}
	if date != nil {
		parts = append(parts, date.String())
	}
	if installment != nil {
		parts = append(parts, fmt.Sprintf("%d/%d", installment.Number, installment.Count))
	}
	return strings.Join(parts, "\x1f")
}

func addedIndex(added []DiffLine, position int) int {
	for i, line := range added {
		if line.Position == position {
			return i
		}
	}
	return -1
}

func transactionIDOf(line StatementLine) *uuid.UUID {
	if line.Transaction == nil {
		return nil
	}
	return &line.Transaction.ID
}
