package ledger_test

import (
	"slices"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func pixInstallment(number int, components ...ledger.ChargeComponent) ledger.StatementItem {
	item := line(ledger.ItemPixInstallment, "PIX PARCELADO JOAO", 50000, "2026-08-10", 1)
	item.Installment = &ledger.InstallmentPosition{Number: number, Count: 12}
	item.Category = ptr("home")
	item.Components = components
	return item
}

func (f *fixture) openKinds(transactionID ...ledger.Transaction) map[ledger.ClarificationKind]int {
	f.t.Helper()
	open, err := f.ledger.Clarifications(f.t.Context(), f.account, ledger.ClarificationFilter{})
	if err != nil {
		f.t.Fatal(err)
	}
	kinds := map[ledger.ClarificationKind]int{}
	for _, c := range open {
		if c.TransactionID == nil || slices.ContainsFunc(transactionID, func(tr ledger.Transaction) bool { return tr.ID == *c.TransactionID }) {
			kinds[c.Kind]++
		}
	}
	return kinds
}

func TestAcceptanceCriterion6InterestInsideAnInstallmentIsNeverAddedAgain(t *testing.T) {
	f := newFixture(t)
	installment := pixInstallment(2,
		ledger.ChargeComponent{Kind: ledger.ComponentPrincipal, Amount: brl(45000), Included: true},
		ledger.ChargeComponent{Kind: ledger.ComponentInterest, Amount: brl(5000), Included: true},
		ledger.ChargeComponent{Kind: ledger.ComponentIOF, Amount: brl(300), Included: false},
	)
	installment.Purpose = ptr("reforma da cozinha")
	iof := line(ledger.ItemTax, "IOF PIX PARCELADO", 300, "2026-09-10", 1)
	iof.Category = ptr("financial_charges")

	out := f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installment, iof))
	plan := f.plan(out.Lines[0].Transaction.ID)

	composition := out.Review.Composition
	if out.Review.Status != ledger.ReviewReconciled || composition.ComputedTotal != brl(50300) {
		t.Errorf("review = %s, computed = %s, want R$ 503,00: the installment once and the IOF line once", out.Review.Status, composition.ComputedTotal)
	}
	if composition.Installments.Amount != brl(50000) || composition.IncludedCharges.Amount != brl(5000) || composition.Charges.Amount != brl(300) {
		t.Errorf("composition = %+v, want R$ 500,00 of installment, R$ 50,00 of interest inside it and R$ 3,00 of IOF apart", composition)
	}
	breakdown := map[ledger.ComponentKind]int64{}
	for _, component := range plan.Breakdown {
		breakdown[component.Kind] = component.Amount.Minor
	}
	if plan.Charged != brl(50000) || breakdown[ledger.ComponentPrincipal] != 45000 || breakdown[ledger.ComponentInterest] != 5000 || breakdown[ledger.ComponentIOF] != 0 {
		t.Errorf("plan = %+v, want principal R$ 450,00 + interest R$ 50,00 = R$ 500,00, never R$ 550,00", plan)
	}
	if kinds := f.openKinds(*out.Lines[0].Transaction); kinds[ledger.ClarificationMissingBreakdown] != 0 || kinds[ledger.ClarificationPurposeUnknown] != 0 {
		t.Errorf("open questions = %v, want none for a detailed Pix installment with its purpose", kinds)
	}
}

func TestAcceptanceCriterion7APixWithoutPurposeIsChargedAndAskedAbout(t *testing.T) {
	f := newFixture(t)
	out := f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", pixInstallment(2)))
	purchase := *out.Lines[0].Transaction
	open, err := f.ledger.Clarifications(t.Context(), f.account, ledger.ClarificationFilter{Kinds: []ledger.ClarificationKind{ledger.ClarificationPurposeUnknown}})
	if err != nil {
		t.Fatal(err)
	}

	if out.Review.Composition.ComputedTotal != brl(50000) || len(open) != 1 || *open[0].TransactionID != purchase.ID {
		t.Fatalf("computed = %s, purpose questions = %+v, want the Pix charged and asked about", out.Review.Composition.ComputedTotal, open)
	}
	if _, err := f.ledger.ResolveClarification(t.Context(), f.account, open[0].ID, ledger.Resolution{Action: ledger.ResolutionResolve, Note: "Pagamento do pedreiro", Purpose: ptr("pagamento do pedreiro")}, ledger.Actor{Author: ledger.AuthorUser}); err != nil {
		t.Fatal(err)
	}
	review := f.review(ledger.StatementRef{})

	if review.Composition.ComputedTotal != brl(50000) || review.Composition.Installments.Amount != brl(50000) {
		t.Errorf("composition after the answer = %+v, want the same total", review.Composition)
	}
	if kinds := f.openKinds(purchase); kinds[ledger.ClarificationPurposeUnknown] != 0 {
		t.Errorf("open questions = %v, want the purpose answered", kinds)
	}
}

func TestAMissingBreakdownIsAskedOncePerPlan(t *testing.T) {
	f := newFixture(t)
	first := f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", pixInstallment(2)))
	purchase := *first.Lines[0].Transaction
	afterFirst := f.openKinds(purchase)[ledger.ClarificationMissingBreakdown]
	f.submitStatement(installmentStatement("2026-11-03", "2026-11-10", "2026-10-04", pixInstallment(3)))
	afterSecond := f.openKinds(purchase)[ledger.ClarificationMissingBreakdown]

	f.submitStatement(installmentStatement("2026-12-03", "2026-12-10", "2026-11-04", pixInstallment(4,
		ledger.ChargeComponent{Kind: ledger.ComponentPrincipal, Amount: brl(45000), Included: true},
		ledger.ChargeComponent{Kind: ledger.ComponentInterest, Amount: brl(5000), Included: true},
	)))
	plan := f.plan(purchase.ID)

	if afterFirst != 1 || afterSecond != 1 {
		t.Errorf("missing breakdown questions = %d then %d, want one for the plan", afterFirst, afterSecond)
	}
	if got := f.openKinds(purchase)[ledger.ClarificationMissingBreakdown]; got != 0 || plan.ChargedWithoutDetail != 2 {
		t.Errorf("open = %d, without detail = %d, want the question closed once a statement detailed it", got, plan.ChargedWithoutDetail)
	}
}
