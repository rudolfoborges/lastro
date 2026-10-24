package ledger_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func installmentStatement(closing, due, periodStart string, items ...ledger.StatementItem) ledger.StatementExtraction {
	total := int64(0)
	for _, item := range items {
		total += item.Amount.Minor
	}
	return ledger.StatementExtraction{
		Issuer:      "Banco Exemplo",
		CardLast4:   "1234",
		ClosingDate: *day(closing),
		DueDate:     *day(due),
		PeriodStart: day(periodStart),
		PeriodEnd:   day(closing),
		Total:       brl(total),
		Reading:     ledger.ReadingComplete,
		PageCount:   ptr(1),
		Items:       items,
	}
}

func installmentLine(text string, number, count int, minor int64, date string) ledger.StatementItem {
	item := line(ledger.ItemInstallment, text, minor, date, 1)
	item.Installment = &ledger.InstallmentPosition{Number: number, Count: count}
	item.Category = ptr("home")
	return item
}

func (f *fixture) plan(transactionID uuid.UUID) ledger.InstallmentPlan {
	f.t.Helper()
	detail := f.detail(transactionID)
	if detail.Plan == nil {
		f.t.Fatalf("transaction %s has no installment plan", transactionID)
	}
	return *detail.Plan
}

func (f *fixture) exec(sql string, args ...any) {
	f.t.Helper()
	conn, err := pgx.Connect(f.t.Context(), f.databaseURL)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := conn.Exec(f.t.Context(), sql, args...); err != nil {
		f.t.Fatal(err)
	}
}

func countStates(plan ledger.InstallmentPlan) map[ledger.InstallmentState]int {
	states := map[ledger.InstallmentState]int{}
	for _, installment := range plan.Installments {
		states[installment.State]++
	}
	return states
}

func TestAcceptanceCriterion5AnInstallmentPurchaseCountsOncePerView(t *testing.T) {
	f := newFixture(t)
	report := supermarket("message-1")
	report.RawText, report.Description = "Comprei uma TV de 1.200 em 12x de 100", "TV"
	report.Amount, report.DateReference, report.OccurredOn = ptr(brl(120000)), nil, day("2026-09-10")
	report.PaymentMethod = credit("1234")
	report.Installments = &ledger.Installments{Count: 12, Amount: ptr(brl(10000))}
	report.Category = ptr("electronics")
	told := f.record(report).Transaction
	before := f.plan(told.ID)

	statement := installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installmentLine("LOJA DE TV 01/12", 1, 12, 10000, "2026-09-10"))
	offered := f.submitStatement(statement)
	statement.Items[0].RelatedTransactionID = &told.ID
	charged := f.submitStatement(statement)
	after := f.plan(told.ID)

	if before.Total == nil || *before.Total != brl(120000) || before.Remaining() != brl(120000) || countStates(before)[ledger.InstallmentKnownFuture] != 12 {
		t.Errorf("plan before the statement = %+v, want R$ 1.200,00 still to come in 12 known installments", before)
	}
	if offered.Result != ledger.StatementPossibleMatch || offered.Candidates[0].Candidates[0].Transaction.ID != told.ID {
		t.Fatalf("statement = %+v, want the TV offered for the first installment", offered)
	}
	if charged.Lines[0].Effect != ledger.EffectLinked || charged.Review.Composition.Installments.Amount != brl(10000) {
		t.Errorf("line = %+v, installments charged = %+v, want R$ 100,00 in the cycle", charged.Lines[0], charged.Review.Composition.Installments)
	}
	if after.Charged != brl(10000) || after.RemainingKnown != brl(110000) || !after.RemainingEstimated.IsZero() {
		t.Errorf("plan after the statement = %+v, want R$ 100,00 charged and R$ 1.100,00 known still to come", after)
	}
	purchase := f.detail(told.ID)
	if purchase.Amount != brl(120000) || purchase.AmountBasis != ledger.AmountTotal || purchase.Status != ledger.StatusConfirmed || f.count("transactions") != 1 {
		t.Errorf("purchase = %+v, want one confirmed purchase of R$ 1.200,00", purchase.Transaction)
	}
}

func TestTheNextStatementChainsTheInstallmentByItself(t *testing.T) {
	f := newFixture(t)
	first := f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installmentLine("LOJA DE MOVEIS 03/10", 3, 10, 10000, "2026-07-02")))
	purchase := first.Lines[0].Transaction

	second := f.submitStatement(installmentStatement("2026-11-03", "2026-11-10", "2026-10-04", installmentLine("LOJA DE MOVEIS 04/10", 4, 10, 10000, "2026-07-02")))
	plan := f.plan(purchase.ID)

	if second.Result != ledger.StatementRecorded || second.Lines[0].Effect != ledger.EffectLinked || second.Lines[0].Transaction.ID != purchase.ID {
		t.Fatalf("second statement = %s, line = %+v, want the installment chained to the same purchase", second.Result, second.Lines[0])
	}
	if plan.Charged != brl(20000) || plan.RemainingEstimated != brl(60000) || len(plan.Installments) != 8 || plan.Installments[0].Number != 3 {
		t.Errorf("plan = %+v, want installments 3 and 4 charged and 5 to 10 estimated", plan)
	}
	if f.count("transactions") != 1 {
		t.Errorf("transactions = %d, want one purchase", f.count("transactions"))
	}
}

func TestTheTotalIsNeverInventedFromOneInstallment(t *testing.T) {
	f := newFixture(t)

	out := f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installmentLine("LOJA DE MOVEIS 03/10", 3, 10, 10000, "2026-07-02")))
	purchase := out.Lines[0].Transaction
	plan := f.plan(purchase.ID)

	if purchase.AmountBasis != ledger.AmountInstallment || purchase.Amount != brl(10000) || purchase.Status != ledger.StatusConfirmed {
		t.Errorf("purchase = %+v, want only the installment amount known", purchase)
	}
	if plan.Total != nil || plan.InstallmentAmountKnown || plan.RemainingEstimated != brl(70000) || !plan.RemainingKnown.IsZero() {
		t.Errorf("plan = %+v, want no total and the 7 future installments estimated", plan)
	}
	if out.Lines[0].Category.Code != "home" {
		t.Errorf("category = %+v", out.Lines[0].Category)
	}
}

func TestTwoPlansThatCouldTakeTheNextInstallmentAreLeftToCase(t *testing.T) {
	f := newFixture(t)
	first := f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04",
		installmentLine("LOJA X 03/10", 3, 10, 10000, "2026-07-02"),
		installmentLine("LOJA X 03/10", 3, 10, 10000, "2026-07-02"),
	))
	next := installmentStatement("2026-11-03", "2026-11-10", "2026-10-04", installmentLine("LOJA X 04/10", 4, 10, 10000, "2026-07-02"))

	offered := f.submitStatement(next)
	next.Items[0].RelatedTransactionID = &first.Lines[1].Transaction.ID
	linked := f.submitStatement(next)

	if offered.Result != ledger.StatementPossibleMatch || len(offered.Candidates[0].Candidates) != 2 {
		t.Fatalf("statement = %+v, want both plans offered", offered)
	}
	if linked.Lines[0].Transaction.ID != first.Lines[1].Transaction.ID || f.plan(first.Lines[0].Transaction.ID).Charged != brl(10000) {
		t.Errorf("linked = %+v, want only the chosen plan charged", linked.Lines[0])
	}
}

func TestFutureInstallmentsPrintedOnTheStatementAreKnown(t *testing.T) {
	f := newFixture(t)
	item := installmentLine("LOJA DE MOVEIS 03/10", 3, 10, 10000, "2026-07-02")
	item.FutureInstallments = []ledger.FutureInstallment{{Number: 4, Amount: brl(10000)}, {Number: 5, Amount: brl(9950)}}

	out := f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", item))
	plan := f.plan(out.Lines[0].Transaction.ID)

	if plan.RemainingKnown != brl(19950) || plan.RemainingEstimated != brl(50000) {
		t.Errorf("plan = %+v, want installments 4 and 5 known and 6 to 10 estimated", plan)
	}
	invalid := installmentStatement("2026-11-03", "2026-11-10", "2026-10-04", installmentLine("OUTRA LOJA 02/03", 2, 3, 5000, "2026-09-02"))
	invalid.Items[0].FutureInstallments = []ledger.FutureInstallment{{Number: 2, Amount: brl(5000)}}
	_, err := f.ledger.SubmitStatement(t.Context(), f.account, invalid, statementReceivedAt, agent)
	assertValidation(t, err, "items[0].future_installments[0].number")
}

func TestInstallmentLinesWithoutAPurchaseGetOneWhenTheStatementIsSentAgain(t *testing.T) {
	f := newFixture(t)
	statement := installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installmentLine("LOJA DE MOVEIS 03/10", 3, 10, 10000, "2026-07-02"))
	first := f.submitStatement(statement)
	legacy := first.Lines[0].Transaction.ID
	f.exec("UPDATE transaction_evidence SET status = 'undone' WHERE transaction_id = $1", legacy)
	f.exec("UPDATE installment_plans SET canceled_at = now() WHERE transaction_id = $1", legacy)
	f.exec("UPDATE transactions SET status = 'voided' WHERE id = $1", legacy)

	again := f.submitStatement(statement)

	if again.Result != ledger.StatementAlreadyRecorded || again.Lines[0].Transaction == nil || again.Lines[0].Transaction.ID == legacy {
		t.Fatalf("resubmission = %s, line = %+v, want the line given a purchase", again.Result, again.Lines[0])
	}
	if plan := f.plan(again.Lines[0].Transaction.ID); plan.Charged != brl(10000) {
		t.Errorf("plan = %+v, want installment 3 charged", plan)
	}
}

func TestRudolfoInformingTheTotalMakesItTheTotalOfThePlan(t *testing.T) {
	f := newFixture(t)
	out := f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installmentLine("LOJA DE MOVEIS 03/10", 3, 10, 10000, "2026-07-02")))
	purchase := out.Lines[0].Transaction

	corrected, err := f.ledger.Correct(t.Context(), f.account, purchase.ID, ledger.Patch{Amount: ptr(brl(100000))}, "Rudolfo disse que o sofá custou R$ 1.000", ledger.Actor{Author: ledger.AuthorUser})
	if err != nil {
		t.Fatal(err)
	}
	plan := f.plan(purchase.ID)

	if corrected.Transaction.AmountBasis != ledger.AmountTotal || plan.Total == nil || *plan.Total != brl(100000) {
		t.Errorf("purchase = %+v, plan total = %v, want R$ 1.000,00 as the total", corrected.Transaction, plan.Total)
	}
}

func TestMergingAToldInstallmentPurchaseFillsTheStatementPlan(t *testing.T) {
	f := newFixture(t)
	report := supermarket("message-1")
	report.RawText, report.Description = "Comprei uma TV de 1.200 em 12x de 100", "TV"
	report.Amount, report.DateReference, report.OccurredOn = ptr(brl(120000)), nil, day("2026-09-10")
	report.PaymentMethod = credit("1234")
	report.Installments = &ledger.Installments{Count: 12, Amount: ptr(brl(10000))}
	told := f.record(report).Transaction
	statement := installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installmentLine("LOJA DE TV 01/12", 1, 12, 10000, "2026-09-10"))
	statement.Items[0].ConfirmDistinct = true
	charged := f.submitStatement(statement).Lines[0].Transaction
	conversation := confirmedSources(f.detail(told.ID))[ledger.SourceConversation]

	merged := f.confirmMatch(told.ID, charged.ID)
	plan := f.plan(charged.ID)
	f.undoMatch(charged.ID, conversation)
	restored := f.plan(charged.ID)

	if merged.Survivor.ID != charged.ID || merged.Survivor.Amount != brl(120000) || merged.Survivor.AmountBasis != ledger.AmountTotal {
		t.Errorf("survivor = %+v, want the statement purchase with the total Rudolfo told", merged.Survivor)
	}
	if plan.Total == nil || *plan.Total != brl(120000) || !plan.InstallmentAmountKnown || plan.RemainingKnown != brl(110000) {
		t.Errorf("plan after the merge = %+v, want the total and the known installments", plan)
	}
	if restored.Total != nil || restored.InstallmentAmountKnown || f.detail(charged.ID).AmountBasis != ledger.AmountInstallment {
		t.Errorf("plan after the undo = %+v, want it back as the statement left it", restored)
	}
	if told := f.plan(told.ID); told.Total == nil || *told.Total != brl(120000) {
		t.Errorf("told plan after the undo = %+v, want it active again", told)
	}
}
