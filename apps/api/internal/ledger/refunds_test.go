package ledger_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

func storeReport(requestID string, minor int64) ledger.ExpenseReport {
	report := supermarket(requestID)
	report.RawText, report.Description, report.Merchant = "Comprei uma luminária de 100 na Loja Exemplo", "Luminária", ptr("Loja Exemplo")
	report.Amount, report.Category = ptr(brl(minor)), ptr("home")
	report.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentDebitCard}
	return report
}

func TestAcceptanceCriterion10APartialRefundKeepsBothAndShowsTheNet(t *testing.T) {
	f := newFixture(t)
	purchase := f.record(storeReport("message-1", 10000)).Transaction
	refund := storeReport("message-2", 3000)
	refund.RawText, refund.Description, refund.Category = "A loja devolveu 30 da luminária", "", nil
	refund.Nature, refund.RefundOfID = ledger.NatureRefund, &purchase.ID
	refund.ReceivedAt = refund.ReceivedAt.Add(30 * 24 * time.Hour)

	refunded := f.record(refund)
	detail := f.detail(purchase.ID)

	if refunded.Result != ledger.ExpenseRecorded || refunded.Transaction.Nature != ledger.NatureRefund || refunded.Transaction.RefundOfID == nil || *refunded.Transaction.RefundOfID != purchase.ID {
		t.Fatalf("refund = %s %+v, want a refund of the purchase", refunded.Result, refunded.Transaction)
	}
	if refunded.Category.Code != "home" || refunded.Transaction.Description != "Estorno: Luminária" {
		t.Errorf("refund = %+v %+v, want the purchase's category and a description", refunded.Transaction, refunded.Category)
	}
	if detail.Amount != brl(10000) || detail.Refunded != brl(3000) || detail.Net() != brl(7000) || len(detail.Refunds) != 1 {
		t.Errorf("purchase = %s, refunded = %s, net = %s, want R$ 100,00 kept, R$ 30,00 refunded, R$ 70,00 net", detail.Amount, detail.Refunded, detail.Net())
	}
	if f.count("transactions") != 2 {
		t.Errorf("transactions = %d, want the purchase and the refund", f.count("transactions"))
	}
}

func TestARefundInALaterStatementPointsToThePurchaseOfAnEarlierOne(t *testing.T) {
	f := newFixture(t)
	purchase := line(ledger.ItemPurchase, "LOJA EXEMPLO", 10000, "2026-09-14", 1)
	purchase.Category = ptr("home")
	first := f.submitStatement(installmentStatement("2026-10-03", "2026-10-10", "2026-09-04", purchase))
	bought := first.Lines[0].Transaction
	refund := line(ledger.ItemRefund, "ESTORNO LOJA EXEMPLO", 3000, "2026-10-20", 1)
	refund.RefundOfID = &bought.ID
	second := installmentStatement("2026-11-03", "2026-11-10", "2026-10-04", refund)
	second.Total = brl(-3000)

	out := f.submitStatement(second)
	detail := f.detail(bought.ID)

	if out.Lines[0].Transaction.RefundOfID == nil || detail.Net() != brl(7000) {
		t.Errorf("refund = %+v, net = %s, want R$ 70,00 net across the two statements", out.Lines[0].Transaction, detail.Net())
	}
}

func TestARefundNeverExceedsThePurchaseNorChangesItsCurrency(t *testing.T) {
	f := newFixture(t)
	purchase := f.record(storeReport("message-1", 10000)).Transaction
	tooMuch := storeReport("message-2", 12000)
	tooMuch.Nature, tooMuch.RefundOfID = ledger.NatureRefund, &purchase.ID
	dollars := storeReport("message-3", 3000)
	dollars.Amount = &money.Money{Minor: 3000, Currency: "USD"}
	dollars.Nature, dollars.RefundOfID = ledger.NatureRefund, &purchase.ID

	_, tooMuchErr := f.ledger.RecordExpense(t.Context(), f.account, tooMuch, agent)
	_, dollarsErr := f.ledger.RecordExpense(t.Context(), f.account, dollars, agent)

	assertValidation(t, tooMuchErr, "amount")
	assertValidation(t, dollarsErr, "currency")
	if f.count("transactions") != 1 {
		t.Errorf("transactions = %d, want only the purchase", f.count("transactions"))
	}
}

func TestARefundRecordedAsAnExpenseCanBeLinkedLater(t *testing.T) {
	f := newFixture(t)
	purchase := f.record(storeReport("message-1", 10000)).Transaction
	mistaken := storeReport("message-2", 3000)
	mistaken.RawText, mistaken.Description = "Recebi 30 da Loja Exemplo", "Devolução"
	mistaken.ReceivedAt = mistaken.ReceivedAt.Add(time.Hour)
	recorded := f.record(mistaken).Transaction

	nature := ledger.NatureRefund
	corrected, err := f.ledger.Correct(t.Context(), f.account, recorded.ID, ledger.Patch{Nature: &nature, RefundOf: ledger.Value(purchase.ID)}, "Era o estorno da luminária", ledger.Actor{Author: ledger.AuthorUser})
	if err != nil {
		t.Fatal(err)
	}
	cleared, err := f.ledger.Correct(t.Context(), f.account, recorded.ID, ledger.Patch{RefundOf: ledger.Cleared[uuid.UUID]()}, "Não era dessa compra", ledger.Actor{Author: ledger.AuthorUser})
	if err != nil {
		t.Fatal(err)
	}

	if corrected.Transaction.Nature != ledger.NatureRefund || corrected.Transaction.RefundOfID == nil || *corrected.Transaction.RefundOfID != purchase.ID {
		t.Errorf("corrected = %+v, want a refund of the purchase", corrected.Transaction)
	}
	if cleared.Transaction.RefundOfID != nil || f.detail(purchase.ID).Net() != brl(10000) {
		t.Errorf("cleared = %+v, want the link undone and the purchase whole again", cleared.Transaction)
	}
}

func TestAnInternationalPurchaseKeepsItsOriginalAmountAndGainsTheChargedOne(t *testing.T) {
	f := newFixture(t)
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "US$ 20,00"))
	reading := purchaseReading(observation, money.Money{Minor: 2000, Currency: "USD"})
	reading.Merchant = ptr("Example Software")
	notified := f.interpret(reading).Transaction
	statement := bakeryStatement()
	statement.Items[0] = line(ledger.ItemPurchase, "EXAMPLE SOFTWARE", 11000, "2026-10-22", 1)
	statement.Items[0].OriginalAmount = &money.Money{Minor: 2000, Currency: "USD"}
	statement.Items[0].RelatedTransactionID = &notified.ID
	statement.Total = brl(11000)

	linked := f.submitStatement(statement).Lines[0].Transaction

	if linked.Amount != brl(11000) || linked.OriginalAmount == nil || *linked.OriginalAmount != (money.Money{Minor: 2000, Currency: "USD"}) {
		t.Errorf("linked = %+v, want R$ 110,00 charged and US$ 20,00 kept as the original", linked)
	}
}

func TestADeclinedPaymentNeverTouchesAPurchase(t *testing.T) {
	f := newFixture(t)
	told := f.record(bakeryReport("message-1")).Transaction
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "Pagamento recusado"))

	declined := f.interpret(ledger.NotificationInterpretation{ObservationID: observation, EventType: ledger.EventDeclined})
	_, related := f.ledger.InterpretNotification(t.Context(), f.account, ledger.NotificationInterpretation{ObservationID: observation, EventType: ledger.EventDeclined, RelatedTransactionID: &told.ID, Reinterpret: true, Reason: "Recusa"}, agent)

	assertInterpretation(t, declined, ledger.InterpretationApplied, ledger.EffectNone)
	assertValidation(t, related, "related_transaction_id")
	if f.count("transactions") != 1 || f.detail(told.ID).Status != ledger.StatusProvisional {
		t.Errorf("transactions = %d, want the told purchase untouched", f.count("transactions"))
	}
}
