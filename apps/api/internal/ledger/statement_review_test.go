package ledger_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func reconciledStatement() ledger.StatementExtraction {
	in := syntheticStatement()
	in.Items = in.Items[:len(in.Items)-1]
	in.Doubts = nil
	in.Total = brl(94182)
	return in
}

func (f *fixture) review(ref ledger.StatementRef) ledger.StatementReview {
	f.t.Helper()
	review, err := f.ledger.StatementReview(f.t.Context(), f.account, ref)
	if err != nil {
		f.t.Fatalf("StatementReview() error = %v", err)
	}
	return review
}

func TestTheCompositionExplainsTheInformedTotal(t *testing.T) {
	f := newFixture(t)
	f.submitStatement(reconciledStatement())

	review := f.review(ledger.StatementRef{})

	c := review.Composition
	if review.Status != ledger.ReviewReconciled || !c.Difference.IsZero() || c.ComputedTotal != brl(94182) || len(review.Pending) != 0 {
		t.Fatalf("review = %+v", review)
	}
	groups := map[string]ledger.CompositionGroup{"purchases": c.Purchases, "installments": c.Installments, "charges": c.Charges, "payments": c.Payments, "credits": c.Credits}
	want := map[string]ledger.CompositionGroup{
		"purchases":    {Amount: brl(34297), Lines: 3},
		"installments": {Amount: brl(60000), Lines: 2},
		"charges":      {Amount: brl(2885), Lines: 2},
		"payments":     {Amount: brl(150000), Lines: 1},
		"credits":      {Amount: brl(3000), Lines: 1},
	}
	for name, group := range want {
		if groups[name] != group {
			t.Errorf("%s = %+v, want %+v", name, groups[name], group)
		}
	}
	if c.PreviousBalance == nil || *c.PreviousBalance != brl(150000) {
		t.Errorf("previous balance = %+v", c.PreviousBalance)
	}
}

func TestADifferenceIsShownAndNeverHiddenInAnAdjustment(t *testing.T) {
	f := newFixture(t)
	in := reconciledStatement()
	in.Total = brl(94183)
	f.submitStatement(in)

	review := f.review(ledger.StatementRef{})

	if review.Status != ledger.ReviewDivergent || review.Composition.Difference != brl(1) {
		t.Fatalf("review = %+v, want divergent by one cent", review)
	}
	if len(review.Pending) != 1 || review.Pending[0].Kind != ledger.PendingDifference {
		t.Errorf("pending = %+v", review.Pending)
	}
	if review.Composition.Adjustments.Lines != 0 || f.countWhere("transactions", "nature = 'adjustment'") != 0 {
		t.Error("the difference became an adjustment")
	}
}

func TestAnIncompleteReadingIsNeverReconciled(t *testing.T) {
	f := newFixture(t)
	partial := reconciledStatement()
	partial.Reading, partial.Missing = ledger.ReadingPartial, "Página 4 ilegível."
	f.submitStatement(partial)
	other := syntheticStatement()
	other.ClosingDate, other.DueDate = *day("2026-11-03"), *day("2026-11-10")
	other.Total = brl(92948)
	f.submitStatement(other)

	byPartial := f.review(ledger.StatementRef{ClosingDate: day("2026-10-03")})
	withDoubts := f.review(ledger.StatementRef{})

	if byPartial.Status != ledger.ReviewReadIncomplete || byPartial.Pending[0].Kind != ledger.PendingPartialReading {
		t.Errorf("partial reading = %+v", byPartial)
	}
	if withDoubts.Statement.ClosingDate.String() != "2026-11-03" || withDoubts.Status != ledger.ReviewReadIncomplete || !withDoubts.Composition.Difference.IsZero() {
		t.Errorf("unknown line and doubt = %+v, want read_incomplete even though the totals match", withDoubts)
	}
}

func TestADryRunReviewsTheReadingWithoutRecordingIt(t *testing.T) {
	f := newFixture(t)
	in := reconciledStatement()
	in.DryRun = true

	out := f.submitStatement(in)

	if out.Result != ledger.StatementPreviewed || out.Review == nil || out.Review.Status != ledger.ReviewReconciled || len(out.Lines) != 9 {
		t.Fatalf("dry run = %+v", out)
	}
	for _, table := range []string{"statements", "statement_versions", "statement_lines", "observations", "transactions", "clarifications"} {
		if n := f.count(table); n != 0 {
			t.Errorf("%s has %d rows after a dry run", table, n)
		}
	}
}

func TestEveryStatementLineCanBeFoundInThePDF(t *testing.T) {
	f := newFixture(t)
	out := f.submitStatement(reconciledStatement())
	purchase := out.Lines[1]

	byTransaction, err := f.ledger.TransactionLineSources(t.Context(), f.account, purchase.Transaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	byIndex, err := f.ledger.StatementLineSource(t.Context(), f.account, out.Statement.ID, 5)
	if err != nil {
		t.Fatal(err)
	}
	_, missing := f.ledger.StatementLineSource(t.Context(), f.account, out.Statement.ID, 99)

	if len(byTransaction) != 1 || byTransaction[0].Page != 2 || byTransaction[0].Excerpt != "2026-09-14 PADARIA EXEMPLO" {
		t.Errorf("by transaction = %+v", byTransaction)
	}
	if byIndex.Kind != ledger.ItemInstallment || byIndex.Page != 2 || byIndex.TransactionID != nil {
		t.Errorf("by index = %+v", byIndex)
	}
	var validation *ledger.ValidationError
	if !errors.As(missing, &validation) || validation.Field != "index" {
		t.Errorf("missing line = %v", missing)
	}
}

func TestClarificationsCanBeListedByStatement(t *testing.T) {
	f := newFixture(t)
	report := supermarket("message-1")
	report.Category = nil
	f.record(report)
	in := syntheticStatement()
	in.Items[1].Category = nil
	out := f.submitStatement(in)

	listed, err := f.ledger.Clarifications(t.Context(), f.account, ledger.ClarificationFilter{StatementID: &out.Statement.ID})
	if err != nil {
		t.Fatal(err)
	}

	kinds := map[ledger.ClarificationKind]int{}
	for _, c := range listed {
		kinds[c.Kind]++
	}
	if kinds[ledger.ClarificationReadingDoubt] != 2 || kinds[ledger.ClarificationCategoryUnknown] != 1 || len(listed) != 3 {
		t.Errorf("clarifications = %v, want the statement doubts and the uncategorized line, not the conversation record", kinds)
	}
}

func (f *fixture) countWhere(table, condition string) int {
	f.t.Helper()
	conn, err := pgx.Connect(f.t.Context(), f.databaseURL)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var count int
	if err := conn.QueryRow(f.t.Context(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()+" WHERE "+condition).Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}
