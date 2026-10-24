package ledger

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/tenantdb"
)

var ErrStatementNotFound = errors.New("no statement matches; Rudolfo has not sent it yet or the closing date is different")

type ReviewStatus string

const (
	ReviewReadIncomplete ReviewStatus = "read_incomplete"
	ReviewDivergent      ReviewStatus = "divergent"
	ReviewReconciled     ReviewStatus = "reconciled"
)

type CompositionGroup struct {
	Amount money.Money
	Lines  int
}

type StatementComposition struct {
	PreviousBalance *money.Money
	Purchases       CompositionGroup
	Installments    CompositionGroup
	Charges         CompositionGroup
	Payments        CompositionGroup
	Credits         CompositionGroup
	Adjustments     CompositionGroup
	Unknown         CompositionGroup
	ComputedTotal   money.Money
	InformedTotal   money.Money
	Difference      money.Money
}

type PendingKind string

const (
	PendingPartialReading PendingKind = "partial_reading"
	PendingUnknownLines   PendingKind = "unknown_lines"
	PendingReadingDoubts  PendingKind = "reading_doubts"
	PendingDifference     PendingKind = "difference"
	PendingClarifications PendingKind = "clarifications"
)

type ReviewPending struct {
	Kind        PendingKind
	Description string
}

type StatementReview struct {
	Statement      Statement
	Composition    StatementComposition
	Status         ReviewStatus
	Pending        []ReviewPending
	Clarifications []Clarification
}

type StatementRef struct {
	StatementID *uuid.UUID
	ClosingDate *calendar.Date
	CardLast4   *string
}

type LineSource struct {
	Statement     Statement
	Position      int
	Kind          StatementItemKind
	Amount        money.Money
	Page          int
	Excerpt       string
	TransactionID *uuid.UUID
}

func (s *Service) StatementReview(ctx context.Context, accountID uuid.UUID, ref StatementRef) (StatementReview, error) {
	var review StatementReview
	err := s.inTx(ctx, accountID, func(t *tx) error {
		statement, err := t.findStatement(ref)
		if err != nil {
			return err
		}
		version, err := t.q.LatestStatementVersion(t.ctx, tenantdb.LatestStatementVersionParams{AccountID: t.accountID, StatementID: statement.ID})
		if err != nil {
			return fmt.Errorf("latest statement version: %w", err)
		}
		review, err = t.review(statement, version)
		return err
	})
	return review, err
}

func (s *Service) StatementLineSource(ctx context.Context, accountID, statementID uuid.UUID, position int) (LineSource, error) {
	var source LineSource
	err := s.inTx(ctx, accountID, func(t *tx) error {
		statement, err := t.findStatement(StatementRef{StatementID: &statementID})
		if err != nil {
			return err
		}
		version, err := t.q.LatestStatementVersion(t.ctx, tenantdb.LatestStatementVersionParams{AccountID: t.accountID, StatementID: statement.ID})
		if err != nil {
			return fmt.Errorf("latest statement version: %w", err)
		}
		row, err := t.q.StatementLineAt(t.ctx, tenantdb.StatementLineAtParams{AccountID: t.accountID, StatementVersionID: version.ID, Position: int32(position)}) //nolint:gosec // compared with stored positions only
		if errors.Is(err, pgx.ErrNoRows) {
			return &ValidationError{Field: "index", Message: "is not a line of this statement"}
		}
		if err != nil {
			return fmt.Errorf("statement line: %w", err)
		}
		source = LineSource{
			Statement:     toStatement(statement, version),
			Position:      int(row.Position),
			Kind:          StatementItemKind(row.Kind),
			Amount:        money.Money{Minor: row.AmountMinor, Currency: version.Currency},
			Page:          int(row.Page),
			Excerpt:       row.Excerpt,
			TransactionID: row.TransactionID,
		}
		return nil
	})
	return source, err
}

func (s *Service) TransactionLineSources(ctx context.Context, accountID, transactionID uuid.UUID) ([]LineSource, error) {
	var sources []LineSource
	err := s.inTx(ctx, accountID, func(t *tx) error {
		if _, err := t.transaction(transactionID); err != nil {
			return err
		}
		rows, err := t.q.TransactionStatementLines(t.ctx, tenantdb.TransactionStatementLinesParams{AccountID: t.accountID, TransactionID: transactionID})
		if err != nil {
			return fmt.Errorf("statement lines of transaction: %w", err)
		}
		for _, row := range rows {
			statement, err := t.q.GetStatement(t.ctx, tenantdb.GetStatementParams{AccountID: t.accountID, ID: row.StatementID})
			if err != nil {
				return fmt.Errorf("statement %s: %w", row.StatementID, err)
			}
			version, err := t.q.GetStatementVersion(t.ctx, tenantdb.GetStatementVersionParams{AccountID: t.accountID, ID: row.StatementVersionID})
			if err != nil {
				return fmt.Errorf("statement version %s: %w", row.StatementVersionID, err)
			}
			sources = append(sources, LineSource{
				Statement:     toStatement(statement, version),
				Position:      int(row.Position),
				Kind:          StatementItemKind(row.Kind),
				Amount:        money.Money{Minor: row.AmountMinor, Currency: version.Currency},
				Page:          int(row.Page),
				Excerpt:       row.Excerpt,
				TransactionID: &transactionID,
			})
		}
		return nil
	})
	return sources, err
}

func (t *tx) findStatement(ref StatementRef) (tenantdb.Statement, error) {
	switch {
	case ref.StatementID != nil:
		statement, err := t.q.GetStatement(t.ctx, tenantdb.GetStatementParams{AccountID: t.accountID, ID: *ref.StatementID})
		if errors.Is(err, pgx.ErrNoRows) {
			return tenantdb.Statement{}, ErrStatementNotFound
		}
		return statement, err
	case ref.ClosingDate != nil:
		rows, err := t.q.FindStatementsByClosingDate(t.ctx, tenantdb.FindStatementsByClosingDateParams{AccountID: t.accountID, ClosingDate: ref.ClosingDate.Time(), CardLast4: ref.CardLast4})
		if err != nil {
			return tenantdb.Statement{}, fmt.Errorf("find statements: %w", err)
		}
		switch len(rows) {
		case 0:
			return tenantdb.Statement{}, ErrStatementNotFound
		case 1:
			return rows[0], nil
		}
		return tenantdb.Statement{}, &ValidationError{Field: "card_last4", Message: "is required: more than one card has a statement closing on this date"}
	default:
		statement, err := t.q.LatestStatement(t.ctx, t.accountID)
		if errors.Is(err, pgx.ErrNoRows) {
			return tenantdb.Statement{}, ErrStatementNotFound
		}
		return statement, err
	}
}

func (t *tx) review(statement tenantdb.Statement, version tenantdb.StatementVersion) (StatementReview, error) {
	composition, err := t.composition(version)
	if err != nil {
		return StatementReview{}, err
	}
	rows, err := t.q.OpenStatementReviewClarifications(t.ctx, tenantdb.OpenStatementReviewClarificationsParams{AccountID: t.accountID, StatementID: &statement.ID, StatementVersionID: version.ID})
	if err != nil {
		return StatementReview{}, fmt.Errorf("list statement clarifications: %w", err)
	}
	review := StatementReview{Statement: toStatement(statement, version), Composition: composition}
	doubts := 0
	for _, row := range rows {
		clarification, err := t.withTarget(row)
		if err != nil {
			return StatementReview{}, err
		}
		if clarification.Kind == ClarificationReadingDoubt && clarification.StatementID != nil {
			doubts++
		}
		review.Clarifications = append(review.Clarifications, clarification)
	}
	review.Pending = pendingOf(review, doubts)
	review.Status = statusOf(review, doubts)
	return review, nil
}

func (t *tx) composition(version tenantdb.StatementVersion) (StatementComposition, error) {
	currency := version.Currency
	zero := func() CompositionGroup { return CompositionGroup{Amount: money.Money{Currency: currency}} }
	out := StatementComposition{
		Purchases: zero(), Installments: zero(), Charges: zero(), Payments: zero(), Credits: zero(), Adjustments: zero(), Unknown: zero(),
		InformedTotal: money.Money{Minor: version.InformedTotalMinor, Currency: currency},
	}
	computed := int64(0)
	if version.PreviousBalanceMinor != nil {
		out.PreviousBalance = &money.Money{Minor: *version.PreviousBalanceMinor, Currency: currency}
		computed = *version.PreviousBalanceMinor
	}
	rows, err := t.q.StatementLineTotals(t.ctx, tenantdb.StatementLineTotalsParams{AccountID: t.accountID, StatementVersionID: version.ID})
	if err != nil {
		return StatementComposition{}, fmt.Errorf("statement line totals: %w", err)
	}
	for _, row := range rows {
		signed := row.AmountMinor
		if Direction(row.Direction) == DirectionCredit {
			signed = -signed
		}
		computed += signed
		group := out.group(StatementItemKind(row.Kind))
		group.Lines += int(row.Lines)
		if StatementItemKind(row.Kind) == ItemAdjustment || StatementItemKind(row.Kind) == ItemUnknown {
			group.Amount.Minor += signed
		} else {
			group.Amount.Minor += row.AmountMinor
		}
	}
	out.ComputedTotal = money.Money{Minor: computed, Currency: currency}
	out.Difference = money.Money{Minor: version.InformedTotalMinor - computed, Currency: currency}
	return out, nil
}

func (c *StatementComposition) group(kind StatementItemKind) *CompositionGroup {
	switch kind {
	case ItemPurchase:
		return &c.Purchases
	case ItemInstallment, ItemPixInstallment:
		return &c.Installments
	case ItemInterest, ItemFee, ItemTax:
		return &c.Charges
	case ItemPayment:
		return &c.Payments
	case ItemCredit, ItemRefund:
		return &c.Credits
	case ItemAdjustment:
		return &c.Adjustments
	default:
		return &c.Unknown
	}
}

func statusOf(review StatementReview, doubts int) ReviewStatus {
	switch {
	case review.Statement.Version.Reading == ReadingPartial || review.Composition.Unknown.Lines > 0 || doubts > 0:
		return ReviewReadIncomplete
	case !review.Composition.Difference.IsZero():
		return ReviewDivergent
	default:
		return ReviewReconciled
	}
}

func pendingOf(review StatementReview, doubts int) []ReviewPending {
	var pending []ReviewPending
	version, composition := review.Statement.Version, review.Composition
	if version.Reading == ReadingPartial {
		pending = append(pending, ReviewPending{Kind: PendingPartialReading, Description: "A leitura da fatura está incompleta: " + *version.Missing})
	}
	if composition.Unknown.Lines > 0 {
		pending = append(pending, ReviewPending{Kind: PendingUnknownLines, Description: fmt.Sprintf("%d item(ns) da fatura sem interpretação, somando %s.", composition.Unknown.Lines, composition.Unknown.Amount.Format())})
	}
	if doubts > 0 {
		pending = append(pending, ReviewPending{Kind: PendingReadingDoubts, Description: fmt.Sprintf("%d dúvida(s) de leitura em aberto.", doubts)})
	}
	if !composition.Difference.IsZero() {
		pending = append(pending, ReviewPending{Kind: PendingDifference, Description: fmt.Sprintf("O total calculado (%s) difere do total informado na fatura (%s) em %s.", composition.ComputedTotal.Format(), composition.InformedTotal.Format(), composition.Difference.Format())})
	}
	if others := len(review.Clarifications) - doubts; others > 0 {
		pending = append(pending, ReviewPending{Kind: PendingClarifications, Description: fmt.Sprintf("%d dúvida(s) em aberto nos lançamentos desta fatura (categoria, finalidade ou correspondência). Elas não mudam o total.", others)})
	}
	return pending
}
