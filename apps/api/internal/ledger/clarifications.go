package ledger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

type ClarificationKind string

const (
	ClarificationPurposeUnknown        ClarificationKind = "purpose_unknown"
	ClarificationCategoryUnknown       ClarificationKind = "category_unknown"
	ClarificationPossibleMatch         ClarificationKind = "possible_match"
	ClarificationReadingDoubt          ClarificationKind = "reading_doubt"
	ClarificationUnmatchedNotification ClarificationKind = "unmatched_notification"
	ClarificationStatementVersionDiff  ClarificationKind = "statement_version_diff"
	ClarificationStatementDivergence   ClarificationKind = "statement_divergence"
	ClarificationMissingBreakdown      ClarificationKind = "missing_breakdown"
	ClarificationFieldConflict         ClarificationKind = "field_conflict"
)

var ClarificationKinds = []ClarificationKind{
	ClarificationPurposeUnknown, ClarificationCategoryUnknown, ClarificationPossibleMatch, ClarificationReadingDoubt,
	ClarificationUnmatchedNotification, ClarificationStatementVersionDiff, ClarificationStatementDivergence,
	ClarificationMissingBreakdown, ClarificationFieldConflict,
}

type ClarificationStatus string

const (
	ClarificationOpen      ClarificationStatus = "open"
	ClarificationResolved  ClarificationStatus = "resolved"
	ClarificationDismissed ClarificationStatus = "dismissed"
)

const (
	maxClarifications     = 200
	defaultClarifications = 50
	fieldClarification    = "clarification"
	subjectTransaction    = "transaction"
)

var ErrClarificationClosed = errors.New("the clarification is already resolved or dismissed")

var fieldLabels = map[Field]string{
	FieldDescription:   "a descrição",
	FieldMerchant:      "o estabelecimento",
	FieldPayee:         "o beneficiário",
	FieldAmount:        "o valor",
	FieldOccurred:      "a data",
	FieldPaymentMethod: "o meio de pagamento",
	FieldNature:        "a natureza",
	FieldPurpose:       "a finalidade",
	FieldInstallments:  "o parcelamento",
	fieldCategory:      "a categoria",
}

type Clarification struct {
	ID            uuid.UUID
	Kind          ClarificationKind
	TransactionID *uuid.UUID
	ObservationID *uuid.UUID
	Subject       string
	Question      string
	Context       map[string]any
	Status        ClarificationStatus
	Resolution    *string
	ResolvedBy    *Author
	ResolvedAt    *time.Time
	CreatedAt     time.Time
	Transaction   *Transaction
}

type ClarificationFilter struct {
	Kinds  []ClarificationKind
	Status ClarificationStatus
	From   *calendar.Date
	To     *calendar.Date
	Limit  int
}

type ResolutionAction string

const (
	ResolutionResolve ResolutionAction = "resolve"
	ResolutionDismiss ResolutionAction = "dismiss"
)

type Resolution struct {
	Action     ResolutionAction
	Note       string
	Purpose    *string
	Categories []Split
}

type ResolvedClarification struct {
	Clarification Clarification
	Correction    *Correction
}

type newClarification struct {
	kind          ClarificationKind
	transactionID *uuid.UUID
	observationID *uuid.UUID
	subject       string
	question      string
	context       map[string]any
}

func (s *Service) Clarifications(ctx context.Context, accountID uuid.UUID, filter ClarificationFilter) ([]Clarification, error) {
	if filter.Status == "" {
		filter.Status = ClarificationOpen
	}
	if !slices.Contains([]ClarificationStatus{ClarificationOpen, ClarificationResolved, ClarificationDismissed}, filter.Status) {
		return nil, &ValidationError{Field: "status", Message: "must be open, resolved or dismissed"}
	}
	for _, kind := range filter.Kinds {
		if !slices.Contains(ClarificationKinds, kind) {
			return nil, &ValidationError{Field: "kind", Message: fmt.Sprintf("must be one of %v", ClarificationKinds)}
		}
	}
	if filter.From != nil && filter.To != nil && filter.To.Before(*filter.From) {
		return nil, &ValidationError{Field: "to", Message: "must not be before from"}
	}
	limit := filter.Limit
	if limit <= 0 {
		limit = defaultClarifications
	}
	limit = min(limit, maxClarifications)
	kinds := make([]string, len(filter.Kinds))
	for i, kind := range filter.Kinds {
		kinds[i] = string(kind)
	}

	var clarifications []Clarification
	err := s.inTx(ctx, accountID, func(t *tx) error {
		rows, err := t.q.ListClarifications(t.ctx, tenantdb.ListClarificationsParams{
			AccountID: t.accountID,
			Status:    string(filter.Status),
			Kinds:     kinds,
			FromDate:  dateTime(filter.From),
			ToDate:    dateTime(filter.To),
			MaxRows:   int32(limit), //nolint:gosec // bounded above
		})
		if err != nil {
			return fmt.Errorf("list clarifications: %w", err)
		}
		clarifications = make([]Clarification, 0, len(rows))
		for _, row := range rows {
			clarification, err := t.withTarget(row)
			if err != nil {
				return err
			}
			clarifications = append(clarifications, clarification)
		}
		return nil
	})
	return clarifications, err
}

func (s *Service) ResolveClarification(ctx context.Context, accountID, clarificationID uuid.UUID, resolution Resolution, actor Actor) (ResolvedClarification, error) {
	var resolved ResolvedClarification
	err := s.inTx(ctx, accountID, func(t *tx) error {
		var err error
		resolved, err = once(t, "resolve_clarification", actor, []any{clarificationID, resolution}, func() (ResolvedClarification, error) {
			return t.resolveClarification(clarificationID, resolution, actor)
		})
		return err
	})
	return resolved, err
}

func (t *tx) resolveClarification(id uuid.UUID, resolution Resolution, actor Actor) (ResolvedClarification, error) {
	note, err := requiredText("note", resolution.Note, maxReason)
	if err != nil {
		return ResolvedClarification{}, err
	}
	if resolution.Action != ResolutionResolve && resolution.Action != ResolutionDismiss {
		return ResolvedClarification{}, &ValidationError{Field: "action", Message: "must be resolve or dismiss"}
	}
	if resolution.Action == ResolutionDismiss && (resolution.Purpose != nil || resolution.Categories != nil) {
		return ResolvedClarification{}, &ValidationError{Field: "action", Message: "a dismissal does not change the transaction; use resolve to send a purpose or categories"}
	}
	row, err := t.q.LockClarification(t.ctx, tenantdb.LockClarificationParams{AccountID: t.accountID, ID: id})
	if err != nil {
		return ResolvedClarification{}, fmt.Errorf("clarification %s: %w", id, notFound(err))
	}
	if ClarificationStatus(row.Status) != ClarificationOpen {
		return ResolvedClarification{}, ErrClarificationClosed
	}
	kind := ClarificationKind(row.Kind)
	if resolution.Action == ResolutionResolve {
		if kind == ClarificationCategoryUnknown && resolution.Categories == nil {
			return ResolvedClarification{}, &ValidationError{Field: "categories", Message: "are needed to resolve a missing category; dismiss it if Rudolfo does not know"}
		}
		if kind == ClarificationPurposeUnknown && resolution.Purpose == nil {
			return ResolvedClarification{}, &ValidationError{Field: "purpose", Message: "is needed to resolve a missing purpose; dismiss it if Rudolfo does not know"}
		}
	}

	status := ClarificationResolved
	if resolution.Action == ResolutionDismiss {
		status = ClarificationDismissed
	}
	closed, err := t.closeClarification(row, status, note, actor.Author)
	if err != nil {
		return ResolvedClarification{}, err
	}

	var result ResolvedClarification
	if closed.TransactionID != nil {
		before := jsonValue(map[string]any{"kind": closed.Kind, "status": ClarificationOpen})
		after := jsonValue(map[string]any{"kind": closed.Kind, "status": closed.Status, "resolution": note})
		if err := t.recordRevision(*closed.TransactionID, fieldClarification, before, after, note, actor); err != nil {
			return ResolvedClarification{}, err
		}
		if resolution.Purpose != nil || resolution.Categories != nil {
			patch := Patch{Categories: resolution.Categories}
			if resolution.Purpose != nil {
				patch.Purpose = Value(*resolution.Purpose)
			}
			correction, err := t.correct(*closed.TransactionID, patch, note, actor)
			if err != nil {
				return ResolvedClarification{}, err
			}
			result.Correction = &correction
		}
	}
	result.Clarification, err = t.withTarget(closed)
	return result, err
}

func (t *tx) refreshClarifications(transactionID uuid.UUID) error {
	transaction, err := t.transaction(transactionID)
	if err != nil {
		return err
	}
	if transaction.Status == StatusVoided || transaction.Status == StatusMerged {
		return t.closeTransactionClarifications(transactionID, nil, ClarificationDismissed, "Lançamento anulado ou fundido.")
	}
	allocations, err := t.allocations(transactionID)
	if err != nil {
		return err
	}
	if len(allocations) == 0 {
		err = t.openClarification(newClarification{
			kind:          ClarificationCategoryUnknown,
			transactionID: &transactionID,
			subject:       subjectTransaction,
			question:      fmt.Sprintf("Qual a categoria de %s?", describe(transaction)),
		})
	} else {
		err = t.closeTransactionClarifications(transactionID, []ClarificationKind{ClarificationCategoryUnknown}, ClarificationResolved, "Categoria definida.")
	}
	if err != nil {
		return err
	}
	if transaction.PaymentMethod != nil && transaction.PaymentMethod.Kind == PaymentPix && transaction.Purpose == nil {
		return t.openClarification(newClarification{
			kind:          ClarificationPurposeUnknown,
			transactionID: &transactionID,
			subject:       subjectTransaction,
			question:      fmt.Sprintf("Qual a finalidade do Pix %s?", describe(transaction)),
		})
	}
	return t.closeTransactionClarifications(transactionID, []ClarificationKind{ClarificationPurposeUnknown}, ClarificationResolved, "Finalidade informada ou não se aplica mais.")
}

func (t *tx) openConflicts(transaction Transaction, conflicts []FieldConflict, actor Actor) error {
	for _, conflict := range conflicts {
		details := map[string]any{"field": conflict.Field, "current": conflict.Current, "proposed": conflict.Proposed, "proposed_by": actor.Author}
		err := t.openClarification(newClarification{
			kind:          ClarificationFieldConflict,
			transactionID: &transaction.ID,
			observationID: actor.ObservationID,
			subject:       string(conflict.Field),
			question:      fmt.Sprintf("Você confirmou %s de %s, mas uma nova conferência traz outro valor. Qual vale?", fieldLabels[conflict.Field], describe(transaction)),
			context:       details,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (t *tx) closeConflictsOn(transactionID uuid.UUID, fields []Field) error {
	rows, err := t.q.OpenTransactionClarifications(t.ctx, tenantdb.OpenTransactionClarificationsParams{AccountID: t.accountID, TransactionID: &transactionID})
	if err != nil {
		return fmt.Errorf("list open clarifications: %w", err)
	}
	for _, row := range rows {
		if ClarificationKind(row.Kind) == ClarificationFieldConflict && slices.Contains(fields, Field(row.Subject)) {
			if _, err := t.closeClarification(row, ClarificationResolved, "Rudolfo corrigiu o campo.", AuthorUser); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *tx) openClarification(in newClarification) error {
	key := dedupKey(in)
	dismissed, err := t.q.WasClarificationDismissed(t.ctx, tenantdb.WasClarificationDismissedParams{AccountID: t.accountID, DedupKey: key})
	if err != nil {
		return fmt.Errorf("check dismissed clarifications: %w", err)
	}
	if dismissed {
		return nil
	}
	details, err := json.Marshal(nonNilPayload(in.context))
	if err != nil {
		return fmt.Errorf("encode clarification context: %w", err)
	}
	err = t.q.InsertClarification(t.ctx, tenantdb.InsertClarificationParams{
		ID:            store.NewID(),
		AccountID:     t.accountID,
		Kind:          string(in.kind),
		TransactionID: in.transactionID,
		ObservationID: in.observationID,
		Subject:       in.subject,
		DedupKey:      key,
		Question:      in.question,
		Context:       details,
	})
	if err != nil {
		return fmt.Errorf("open clarification: %w", err)
	}
	return nil
}

func (t *tx) closeTransactionClarifications(transactionID uuid.UUID, kinds []ClarificationKind, status ClarificationStatus, resolution string) error {
	rows, err := t.q.OpenTransactionClarifications(t.ctx, tenantdb.OpenTransactionClarificationsParams{AccountID: t.accountID, TransactionID: &transactionID})
	if err != nil {
		return fmt.Errorf("list open clarifications: %w", err)
	}
	for _, row := range rows {
		if kinds != nil && !slices.Contains(kinds, ClarificationKind(row.Kind)) {
			continue
		}
		if _, err := t.closeClarification(row, status, resolution, AuthorSystem); err != nil {
			return err
		}
	}
	return nil
}

func (t *tx) closeClarification(row tenantdb.Clarification, status ClarificationStatus, resolution string, by Author) (tenantdb.Clarification, error) {
	resolvedBy := string(by)
	if by == AuthorRule {
		resolvedBy = string(AuthorAgent)
	}
	closed, err := t.q.CloseClarification(t.ctx, tenantdb.CloseClarificationParams{
		AccountID:  t.accountID,
		ID:         row.ID,
		Status:     string(status),
		Resolution: &resolution,
		ResolvedBy: &resolvedBy,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return tenantdb.Clarification{}, ErrClarificationClosed
	}
	if err != nil {
		return tenantdb.Clarification{}, fmt.Errorf("close clarification: %w", err)
	}
	return closed, nil
}

func (t *tx) withTarget(row tenantdb.Clarification) (Clarification, error) {
	clarification := Clarification{
		ID:            row.ID,
		Kind:          ClarificationKind(row.Kind),
		TransactionID: row.TransactionID,
		ObservationID: row.ObservationID,
		Subject:       row.Subject,
		Question:      row.Question,
		Status:        ClarificationStatus(row.Status),
		Resolution:    row.Resolution,
		ResolvedAt:    row.ResolvedAt,
		CreatedAt:     row.CreatedAt,
	}
	if row.ResolvedBy != nil {
		by := Author(*row.ResolvedBy)
		clarification.ResolvedBy = &by
	}
	if err := json.Unmarshal(row.Context, &clarification.Context); err != nil {
		return Clarification{}, fmt.Errorf("decode clarification context: %w", err)
	}
	if row.TransactionID != nil {
		transaction, err := t.transaction(*row.TransactionID)
		if err != nil {
			return Clarification{}, err
		}
		clarification.Transaction = &transaction
	}
	return clarification, nil
}

func dedupKey(in newClarification) string {
	parts := []string{string(in.kind), in.subject}
	if in.transactionID != nil {
		parts = append(parts, "transaction:"+in.transactionID.String())
	}
	if in.observationID != nil {
		parts = append(parts, "observation:"+in.observationID.String())
	}
	return strings.Join(parts, "|")
}

func describe(transaction Transaction) string {
	when := "data desconhecida"
	if transaction.Occurred.On != nil {
		when = transaction.Occurred.On.Format()
		if transaction.Occurred.Precision == calendar.PrecisionMonth {
			when = transaction.Occurred.On.Time().Format("01/2006")
		}
	}
	return fmt.Sprintf("\"%s\" (%s, %s)", transaction.Description, transaction.Amount.Format(), when)
}

func FieldLabel(field Field) string {
	if label, ok := fieldLabels[field]; ok {
		return label
	}
	return string(field)
}
