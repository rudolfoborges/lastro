package ledger_test

import (
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

func (f *fixture) purchases(from, to string, filter ledger.ViewFilter, groupBy ledger.GroupBy) ledger.PurchasesView {
	f.t.Helper()
	view, err := f.ledger.Purchases(f.t.Context(), f.account, *day(from), *day(to), filter, groupBy)
	if err != nil {
		f.t.Fatalf("Purchases() error = %v", err)
	}
	return view
}

func totalIn(view ledger.PurchasesView, currency string) ledger.PurchaseTotals {
	for _, total := range view.Totals {
		if total.Currency == currency {
			return total
		}
	}
	return ledger.PurchaseTotals{Currency: currency, Confirmed: ledger.Bucket{Amount: money.Money{Currency: currency}}, RefundsConfirmed: money.Money{Currency: currency}}
}

func (f *fixture) tvInTwelveInstallments() ledger.Transaction {
	f.t.Helper()
	report := supermarket("message-tv")
	report.RawText, report.Description = "Comprei uma TV de 1.200 em 12x de 100", "TV"
	report.Amount, report.DateReference, report.OccurredOn = ptr(brl(120000)), nil, day("2026-09-10")
	report.PaymentMethod = credit("1234")
	report.Installments = &ledger.Installments{Count: 12, Amount: ptr(brl(10000))}
	report.Category = ptr("electronics")
	told := *f.record(report).Transaction
	statement := installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installmentLine("LOJA DE TV 01/12", 1, 12, 10000, "2026-09-10"))
	statement.Items[0].RelatedTransactionID, statement.Items[0].Category = &told.ID, nil
	f.submitStatement(statement)
	return told
}

var asOf = time.Date(2026, 10, 24, 15, 0, 0, 0, time.UTC)

func TestAcceptanceCriterion5EachViewCountsTheInstallmentPurchaseItsOwnWay(t *testing.T) {
	f := newFixture(t)
	tv := f.tvInTwelveInstallments()

	purchases := f.purchases("2026-09-01", "2026-09-30", ledger.ViewFilter{}, ledger.GroupNone)
	charges, err := f.ledger.StatementCharges(t.Context(), f.account, ledger.StatementRef{})
	if err != nil {
		t.Fatal(err)
	}
	commitments, err := f.ledger.FutureCommitments(t.Context(), f.account, ledger.ViewFilter{}, asOf)
	if err != nil {
		t.Fatal(err)
	}

	if got := totalIn(purchases, "BRL"); got.Confirmed.Amount != brl(120000) || got.Confirmed.Count != 1 || got.Confirmed.TransactionIDs[0] != tv.ID {
		t.Errorf("purchases = %+v, want the TV once at R$ 1.200,00", got)
	}
	if len(charges.Categories) != 1 || charges.Categories[0].Amount != brl(10000) || charges.Categories[0].Code != "electronics" {
		t.Errorf("statement categories = %+v, want R$ 100,00 of electronics in the cycle", charges.Categories)
	}
	if len(commitments.Totals) != 1 || commitments.Totals[0].Known != brl(110000) || !commitments.Totals[0].Estimated.IsZero() {
		t.Errorf("commitments = %+v, want R$ 1.100,00 known still to come", commitments.Totals)
	}
	if len(commitments.Months) != 11 || commitments.Months[0].Month != "2026-11" || commitments.Months[0].MonthEstimated {
		t.Errorf("months = %+v, want installments 2 to 12 from November on", commitments.Months)
	}
}

func TestAcceptanceCriterion6ThePixSubtotalShowsTheInterestInsideIt(t *testing.T) {
	f := newFixture(t)
	installment := pixInstallment(2,
		ledger.ChargeComponent{Kind: ledger.ComponentPrincipal, Amount: brl(45000), Included: true},
		ledger.ChargeComponent{Kind: ledger.ComponentInterest, Amount: brl(5000), Included: true},
	)
	installment.Purpose = ptr("reforma")
	f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installment))

	charges, err := f.ledger.StatementCharges(t.Context(), f.account, ledger.StatementRef{})
	if err != nil {
		t.Fatal(err)
	}

	if len(charges.Modalities) != 1 || charges.Modalities[0].Modality != ledger.ModalityPixInstallment || charges.Modalities[0].Amount != brl(50000) || charges.Modalities[0].IncludedCharges != brl(5000) {
		t.Errorf("modalities = %+v, want R$ 500,00 of Pix parcelado with R$ 50,00 of interest inside", charges.Modalities)
	}
	sum := int64(0)
	for _, category := range charges.Categories {
		sum += category.Amount.Minor
	}
	if sum != 50000 || charges.Review.Composition.ComputedTotal != brl(50000) {
		t.Errorf("categories add up to %d and the statement to %s, want R$ 500,00, never R$ 550,00", sum, charges.Review.Composition.ComputedTotal)
	}
}

func TestAcceptanceCriterion10TheRefundCountsInThePeriodItHappened(t *testing.T) {
	f := newFixture(t)
	report := storeReport("message-1", 10000)
	report.DateReference, report.OccurredOn = nil, day("2026-09-20")
	purchase := f.record(report).Transaction
	refund := storeReport("message-2", 3000)
	refund.Nature, refund.RefundOfID, refund.Category = ledger.NatureRefund, &purchase.ID, nil
	refund.DateReference, refund.OccurredOn = nil, day("2026-10-02")
	f.record(refund)

	september := totalIn(f.purchases("2026-09-01", "2026-09-30", ledger.ViewFilter{}, ledger.GroupNone), "BRL")
	october := totalIn(f.purchases("2026-10-01", "2026-10-31", ledger.ViewFilter{}, ledger.GroupNone), "BRL")
	both := totalIn(f.purchases("2026-09-01", "2026-10-31", ledger.ViewFilter{}, ledger.GroupNone), "BRL")

	if september.Confirmed.Amount != brl(10000) || september.NetConfirmed() != brl(10000) {
		t.Errorf("september = %+v, want the purchase of R$ 100,00", september)
	}
	if october.Refunds.Amount != brl(3000) || october.NetConfirmed() != brl(-3000) {
		t.Errorf("october = %+v, want the refund of R$ 30,00 in its own month", october)
	}
	if both.Confirmed.Amount != brl(10000) || both.Refunds.Amount != brl(3000) || both.NetConfirmed() != brl(7000) {
		t.Errorf("both months = %+v, want gross R$ 100,00, refunds R$ 30,00 and net R$ 70,00", both)
	}
}

func TestAnUnresolvedOverlapIsNeverPartOfTheConfirmedTotal(t *testing.T) {
	f := newFixture(t)
	f.notifiedBakery("0|wallet|7|null|10123")
	report := bakeryReport("message-1")
	report.ReceivedAt = postedAt.Add(time.Hour)
	report.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentDebitCard}
	report.DeferMatch = true
	f.record(report)

	got := totalIn(f.purchases("2026-10-01", "2026-10-31", ledger.ViewFilter{}, ledger.GroupNone), "BRL")

	if got.Pending.Amount != brl(3297) || got.Provisional.Amount != brl(3297) || !got.Confirmed.Amount.IsZero() {
		t.Errorf("totals = %+v, want the notified purchase provisional and the doubtful one pending, apart", got)
	}
}

func TestCurrenciesAreNeverAddedAndUnknownTotalsAreListedApart(t *testing.T) {
	f := newFixture(t)
	dollars := storeReport("message-1", 2000)
	dollars.Amount = &money.Money{Minor: 2000, Currency: "USD"}
	dollars.DateReference, dollars.OccurredOn = nil, day("2026-09-15")
	f.record(dollars)
	reais := storeReport("message-2", 5000)
	reais.DateReference, reais.OccurredOn = nil, day("2026-09-16")
	f.record(reais)
	f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", installmentLine("LOJA DE MOVEIS 03/10", 3, 10, 10000, "2026-09-02")))

	view := f.purchases("2026-09-01", "2026-09-30", ledger.ViewFilter{}, ledger.GroupNone)

	if len(view.Totals) != 2 || totalIn(view, "BRL").Confirmed.Amount != brl(5000) || totalIn(view, "USD").Confirmed.Amount != (money.Money{Minor: 2000, Currency: "USD"}) {
		t.Errorf("totals = %+v, want reais and dollars apart", view.Totals)
	}
	if len(view.UnknownTotal) != 1 || view.UnknownTotal[0].InstallmentAmount != brl(10000) || view.UnknownTotal[0].InstallmentCount != 10 {
		t.Errorf("unknown totals = %+v, want the statement-only installment purchase listed and not added", view.UnknownTotal)
	}
}

func TestPurchasesAreGroupedAndFiltered(t *testing.T) {
	f := newFixture(t)
	f.record(bakeryReport("message-1"))
	groceries := supermarket("message-2")
	groceries.Conversation = "conversation-2"
	f.record(groceries)

	byCategory := f.purchases("2026-10-01", "2026-10-31", ledger.ViewFilter{}, ledger.GroupCategory)
	onlyGroceries := f.purchases("2026-10-01", "2026-10-31", ledger.ViewFilter{Category: ptr("groceries")}, ledger.GroupNone)
	byMerchant := f.purchases("2026-10-01", "2026-10-31", ledger.ViewFilter{Merchant: ptr("padaria pao")}, ledger.GroupNone)
	fromWallet := f.purchases("2026-10-01", "2026-10-31", ledger.ViewFilter{Source: ptr(ledger.SourceWalletNotification)}, ledger.GroupNone)

	if len(byCategory.Groups) != 2 || byCategory.Groups[0].Key != "groceries" || byCategory.Groups[1].Key != "meals" {
		t.Errorf("groups = %+v, want groceries and meals", byCategory.Groups)
	}
	if totalIn(onlyGroceries, "BRL").Provisional.Amount != brl(32739) {
		t.Errorf("groceries = %+v", onlyGroceries.Totals)
	}
	if totalIn(byMerchant, "BRL").Provisional.Amount != brl(3297) || len(fromWallet.Totals) != 0 {
		t.Errorf("by merchant = %+v, from wallet = %+v", byMerchant.Totals, fromWallet.Totals)
	}
	_, err := f.ledger.Purchases(t.Context(), f.account, *day("2026-10-31"), *day("2026-10-01"), ledger.ViewFilter{}, ledger.GroupNone)
	assertValidation(t, err, "to")
}

func TestPaymentsExplainAStatementWithoutBeingSpending(t *testing.T) {
	f := newFixture(t)
	purchase := line(ledger.ItemPurchase, "LOJA EXEMPLO", 10000, "2026-09-14", 1)
	purchase.Category = ptr("home")
	f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", purchase))
	payment := line(ledger.ItemPayment, "PAGAMENTO EFETUADO", 10000, "2026-10-10", 1)
	next := installmentStatement("2026-11-03", "2026-11-10", "2026-10-04", payment)
	next.PreviousBalance, next.Total = ptr(brl(10000)), brl(0)
	f.submitStatement(next)

	view, err := f.ledger.Payments(t.Context(), f.account, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	spending := f.purchases("2026-09-01", "2026-11-30", ledger.ViewFilter{}, ledger.GroupNone)

	if len(view.Statements) != 2 || !view.Statements[0].Paid || view.Statements[0].PaidIn.ClosingDate != *day("2026-11-03") {
		t.Fatalf("payments = %+v, want October paid as the November statement shows", view.Statements)
	}
	if view.Statements[1].Paid || view.Statements[1].PreviousBalance == nil || *view.Statements[1].PreviousBalance != brl(10000) {
		t.Errorf("november = %+v, want the previous balance and no evidence of payment yet", view.Statements[1])
	}
	if got := totalIn(spending, "BRL"); got.Confirmed.Amount != brl(10000) {
		t.Errorf("purchases = %+v, want only the purchase: a payment is not spending", got)
	}
}
