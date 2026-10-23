package ledger_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

func credit(last4 string) *ledger.PaymentMethodRef {
	return &ledger.PaymentMethodRef{Kind: ledger.PaymentCreditCard, CardLast4: ptr(last4)}
}

func (f *fixture) walletObservation(notification ledger.WalletNotification) uuid.UUID {
	f.t.Helper()
	receipt := f.ingest(notification)
	if receipt.ObservationID == nil {
		f.t.Fatalf("ingest = %+v, want a stored notification", receipt)
	}
	return *receipt.ObservationID
}

func purchaseReading(observationID uuid.UUID, amount money.Money) ledger.NotificationInterpretation {
	return ledger.NotificationInterpretation{
		ObservationID: observationID,
		EventType:     ledger.EventPurchase,
		Amount:        &amount,
		Merchant:      ptr("Padaria Pão Quente"),
		PaymentMethod: credit("1234"),
		Category:      ptr("meals"),
	}
}

func (f *fixture) interpret(in ledger.NotificationInterpretation) ledger.InterpretationOutcome {
	f.t.Helper()
	outcome, err := f.ledger.InterpretNotification(f.t.Context(), f.account, in, agent)
	if err != nil {
		f.t.Fatalf("InterpretNotification(%s) error = %v", in.EventType, err)
	}
	return outcome
}

func assertInterpretation(t *testing.T, outcome ledger.InterpretationOutcome, result ledger.InterpretationResult, effect ledger.InterpretationEffect) {
	t.Helper()
	if outcome.Result != result || outcome.Effect != effect {
		t.Errorf("outcome = %s/%s, want %s/%s", outcome.Result, outcome.Effect, result, effect)
	}
}

func TestAcceptanceCriterion2RepeatedNotificationsMakeOneTransaction(t *testing.T) {
	f := newFixture(t)
	key := "0|wallet|7|null|10123"
	notification := walletPurchase(key, "Compra de R$ 32,97 pendente")
	first := f.walletObservation(notification)
	f.ingest(notification)
	f.ingest(notification)

	created := f.interpret(purchaseReading(first, brl(3297)))
	again := f.interpret(purchaseReading(first, brl(3297)))

	update := walletPurchase(key, "R$ 32,98 em Padaria Pão Quente com Visa •••• 1234")
	update.Event, update.PostedAt, update.ReceivedAt = ledger.NotificationUpdated, postedAt.Add(time.Minute), postedAt.Add(2*time.Minute)
	second := f.walletObservation(update)
	f.ingest(update)
	misread := f.interpret(purchaseReading(second, brl(3298)))
	updateReading := ledger.NotificationInterpretation{
		ObservationID:        second,
		EventType:            ledger.EventPurchaseUpdate,
		Amount:               ptr(brl(3298)),
		RelatedTransactionID: &created.Transaction.ID,
	}
	updated := f.interpret(updateReading)
	resent := f.interpret(updateReading)

	assertInterpretation(t, created, ledger.InterpretationApplied, ledger.EffectCreated)
	assertInterpretation(t, again, ledger.InterpretationAlreadyApplied, ledger.EffectCreated)
	if misread.Result != ledger.InterpretationPossibleMatch || len(misread.Candidates) != 1 || misread.Candidates[0].Transaction.ID != created.Transaction.ID {
		t.Errorf("a purchase reading of the update = %+v, want the earlier transaction as the only candidate", misread)
	}
	assertInterpretation(t, updated, ledger.InterpretationApplied, ledger.EffectUpdated)
	assertInterpretation(t, resent, ledger.InterpretationAlreadyApplied, ledger.EffectUpdated)
	if got := f.count("transactions"); got != 1 {
		t.Fatalf("transactions = %d, want 1", got)
	}
	if updated.Transaction.ID != created.Transaction.ID || updated.Transaction.Amount != brl(3298) {
		t.Errorf("updated transaction = %+v, want %s with R$ 32,98", updated.Transaction, created.Transaction.ID)
	}
	evidence, err := f.ledger.TransactionEvidence(t.Context(), f.account, created.Transaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(evidence) != 2 {
		t.Errorf("evidence = %d links, want both notification versions", len(evidence))
	}
}

func TestNoticesAndUncertainReadingsNeverBecomeExpenses(t *testing.T) {
	f := newFixture(t)
	for i, event := range []ledger.EventType{ledger.EventDeclined, ledger.EventNotice, ledger.EventUnrelated, ledger.EventUnknown} {
		observation := f.walletObservation(walletPurchase("0|wallet|"+string(rune('a'+i))+"|null|10123", "Mensagem "+string(event)))
		reading := ledger.NotificationInterpretation{ObservationID: observation, EventType: event, Amount: ptr(brl(5000))}

		outcome := f.interpret(reading)

		assertInterpretation(t, outcome, ledger.InterpretationApplied, ledger.EffectNone)
		if outcome.Transaction != nil {
			t.Errorf("%s created transaction %+v", event, outcome.Transaction)
		}
		if event == ledger.EventUnknown && (len(outcome.Clarifications) != 1 || outcome.Clarifications[0].Kind != ledger.ClarificationReadingDoubt) {
			t.Errorf("unknown clarifications = %+v, want one reading doubt", outcome.Clarifications)
		}
	}
	if got := f.count("transactions"); got != 0 {
		t.Errorf("transactions = %d, want 0", got)
	}
}

func TestADifferentReadingNeedsAnExplicitReinterpretation(t *testing.T) {
	f := newFixture(t)
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "Alerta de segurança: R$ 32,97"))
	created := f.interpret(purchaseReading(observation, brl(3297)))

	_, err := f.ledger.InterpretNotification(t.Context(), f.account, ledger.NotificationInterpretation{ObservationID: observation, EventType: ledger.EventNotice}, agent)
	if !errors.Is(err, ledger.ErrAlreadyInterpreted) {
		t.Fatalf("error = %v, want ErrAlreadyInterpreted", err)
	}
	notice := f.interpret(ledger.NotificationInterpretation{ObservationID: observation, EventType: ledger.EventNotice, Reinterpret: true, Reason: "Era só um alerta de segurança."})

	assertInterpretation(t, notice, ledger.InterpretationApplied, ledger.EffectUndone)
	if notice.Transaction == nil || notice.Transaction.ID != created.Transaction.ID || notice.Transaction.Status != ledger.StatusVoided {
		t.Errorf("reinterpreted transaction = %+v, want %s voided", notice.Transaction, created.Transaction.ID)
	}
	interpretations, err := f.ledger.Interpretations(t.Context(), f.account, observation)
	if err != nil {
		t.Fatal(err)
	}
	if len(interpretations) != 2 || interpretations[1].EventType != ledger.EventNotice {
		t.Errorf("interpretations = %+v, want the purchase then the notice", interpretations)
	}
}

func TestReinterpretingAPurchaseCorrectsTheSameTransaction(t *testing.T) {
	f := newFixture(t)
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "R$ 32,97 com Visa •••• 1234"))
	created := f.interpret(purchaseReading(observation, brl(3279)))
	fixed := purchaseReading(observation, brl(3297))
	fixed.Reinterpret, fixed.Reason = true, "Valor lido errado."

	outcome := f.interpret(fixed)

	assertInterpretation(t, outcome, ledger.InterpretationApplied, ledger.EffectUpdated)
	if outcome.Transaction.ID != created.Transaction.ID || outcome.Transaction.Amount != brl(3297) || f.count("transactions") != 1 {
		t.Errorf("transaction = %+v, want %s corrected to R$ 32,97", outcome.Transaction, created.Transaction.ID)
	}
}

func TestStatusOfANotificationPurchaseFollowsThePaymentMethod(t *testing.T) {
	f := newFixture(t)
	for key, want := range map[string]ledger.Status{"credit": ledger.StatusProvisional, "debit": ledger.StatusConfirmed, "unknown": ledger.StatusProvisional} {
		observation := f.walletObservation(walletPurchase("0|wallet|"+key+"|null|10123", "Compra "+key))
		reading := purchaseReading(observation, brl(1000))
		reading.ConfirmDistinct = true
		switch key {
		case "debit":
			reading.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentDebitCard, CardLast4: ptr("9876")}
		case "unknown":
			reading.PaymentMethod = nil
		}

		if got := f.interpret(reading).Transaction.Status; got != want {
			t.Errorf("%s purchase status = %s, want %s", key, got, want)
		}
	}
}

func TestAPurchaseLikeAConversationRecordIsOfferedForMatching(t *testing.T) {
	f := newFixture(t)
	report := supermarket("req-1")
	report.ReceivedAt, report.Amount = postedAt.Add(-time.Hour), ptr(brl(3297))
	told := f.record(report)
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "R$ 32,97 com Visa •••• 1234"))

	offered := f.interpret(purchaseReading(observation, brl(3297)))
	linkReading := purchaseReading(observation, brl(3297))
	linkReading.RelatedTransactionID = &told.Transaction.ID
	linked := f.interpret(linkReading)

	if offered.Result != ledger.InterpretationPossibleMatch || len(offered.Candidates) != 1 || offered.Candidates[0].Transaction.ID != told.Transaction.ID {
		t.Fatalf("offered = %+v, want the conversation record as candidate", offered)
	}
	if len(offered.Candidates[0].Reasons) < 2 {
		t.Errorf("reasons = %v, want the amount and the date explained", offered.Candidates[0].Reasons)
	}
	assertInterpretation(t, linked, ledger.InterpretationApplied, ledger.EffectLinked)
	if f.count("transactions") != 1 || linked.Transaction.ID != told.Transaction.ID {
		t.Errorf("linked = %+v, want the conversation record and no new transaction", linked.Transaction)
	}
	if linked.Transaction.PaymentMethod == nil || *linked.Transaction.PaymentMethod.CardLast4 != "1234" || linked.Transaction.Occurred.Precision != calendar.PrecisionExact {
		t.Errorf("linked transaction = %+v, want the card and the exact time filled from the notification", linked.Transaction)
	}
	if linked.Transaction.Description != told.Transaction.Description {
		t.Errorf("description = %q, want Rudolfo's %q kept", linked.Transaction.Description, told.Transaction.Description)
	}
}

func TestADistinctPurchaseIsCreatedWhenCaseConfirmsIt(t *testing.T) {
	f := newFixture(t)
	report := supermarket("req-1")
	report.ReceivedAt, report.Amount = postedAt.Add(-time.Hour), ptr(brl(3297))
	f.record(report)
	observation := f.walletObservation(walletPurchase("0|wallet|7|null|10123", "R$ 32,97 com Visa •••• 1234"))
	reading := purchaseReading(observation, brl(3297))
	reading.ConfirmDistinct = true

	assertInterpretation(t, f.interpret(reading), ledger.InterpretationApplied, ledger.EffectCreated)
	if got := f.count("transactions"); got != 2 {
		t.Errorf("transactions = %d, want 2 legitimate purchases", got)
	}
}

func TestATransactionConfirmedByOneNotificationCannotTakeAnother(t *testing.T) {
	f := newFixture(t)
	first := f.walletObservation(walletPurchase("0|wallet|1|null|10123", "R$ 32,97"))
	created := f.interpret(purchaseReading(first, brl(3297)))
	second := f.walletObservation(walletPurchase("0|wallet|2|null|10123", "R$ 32,97"))
	reading := purchaseReading(second, brl(3297))
	reading.RelatedTransactionID = &created.Transaction.ID

	_, err := f.ledger.InterpretNotification(t.Context(), f.account, reading, agent)

	assertValidation(t, err, "related_transaction_id")
}

func TestACancellationVoidsThePurchase(t *testing.T) {
	f := newFixture(t)
	purchase := f.interpret(purchaseReading(f.walletObservation(walletPurchase("0|wallet|1|null|10123", "R$ 32,97")), brl(3297)))
	cancellation := f.walletObservation(walletPurchase("0|wallet|2|null|10123", "Compra de R$ 32,97 cancelada"))

	outcome := f.interpret(ledger.NotificationInterpretation{ObservationID: cancellation, EventType: ledger.EventCancellation, RelatedTransactionID: &purchase.Transaction.ID})

	assertInterpretation(t, outcome, ledger.InterpretationApplied, ledger.EffectVoided)
	if outcome.Transaction.Status != ledger.StatusVoided {
		t.Errorf("status = %s, want voided", outcome.Transaction.Status)
	}
}

func TestARefundIsKeptApartFromThePurchase(t *testing.T) {
	f := newFixture(t)
	purchase := f.interpret(purchaseReading(f.walletObservation(walletPurchase("0|wallet|1|null|10123", "R$ 100,00")), brl(10000)))
	refundObservation := f.walletObservation(walletPurchase("0|wallet|2|null|10123", "Estorno de R$ 30,00"))
	tooMuch := f.walletObservation(walletPurchase("0|wallet|3|null|10123", "Estorno de R$ 80,00"))

	refund := f.interpret(ledger.NotificationInterpretation{ObservationID: refundObservation, EventType: ledger.EventRefund, Amount: ptr(brl(3000)), RelatedTransactionID: &purchase.Transaction.ID})
	_, err := f.ledger.InterpretNotification(t.Context(), f.account, ledger.NotificationInterpretation{ObservationID: tooMuch, EventType: ledger.EventRefund, Amount: ptr(brl(8000)), RelatedTransactionID: &purchase.Transaction.ID}, agent)

	assertInterpretation(t, refund, ledger.InterpretationApplied, ledger.EffectRefundCreated)
	got := refund.Transaction
	if got.Nature != ledger.NatureRefund || got.RefundOfID == nil || *got.RefundOfID != purchase.Transaction.ID || got.Amount != brl(3000) {
		t.Errorf("refund = %+v, want R$ 30,00 refunding %s", got, purchase.Transaction.ID)
	}
	if got.Status != ledger.StatusProvisional || refund.Category.Code != "meals" {
		t.Errorf("refund status/category = %s/%s, want provisional and the purchase's category", got.Status, refund.Category.Code)
	}
	assertValidation(t, err, "amount")
	original, err := f.ledger.GetTransaction(t.Context(), f.account, purchase.Transaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	if original.Amount != brl(10000) || original.Status != purchase.Transaction.Status {
		t.Errorf("purchase after refund = %+v, want it preserved", original.Transaction)
	}
}

func TestPendingNotificationsAreListedInArrivalOrderUntilInterpreted(t *testing.T) {
	f := newFixture(t)
	var observations []uuid.UUID
	for i := range 3 {
		notification := walletPurchase("0|wallet|"+string(rune('a'+i))+"|null|10123", "Compra")
		notification.ReceivedAt = notification.ReceivedAt.Add(time.Duration(i) * time.Second)
		observations = append(observations, f.walletObservation(notification))
	}

	first, err := f.ledger.PendingNotifications(t.Context(), f.account, "", 2)
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.ledger.PendingNotifications(t.Context(), f.account, first.NextCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	f.interpret(ledger.NotificationInterpretation{ObservationID: observations[0], EventType: ledger.EventNotice})
	after, err := f.ledger.PendingNotifications(t.Context(), f.account, "", 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(first.Notifications) != 2 || first.NextCursor == "" || first.Pending != 3 || first.Notifications[0].ObservationID != observations[0] {
		t.Errorf("first page = %d items, cursor %q, pending %d", len(first.Notifications), first.NextCursor, first.Pending)
	}
	if len(second.Notifications) != 1 || second.NextCursor != "" || second.Notifications[0].ObservationID != observations[2] {
		t.Errorf("second page = %+v", second)
	}
	if after.Pending != 2 || after.Notifications[0].ObservationID != observations[1] {
		t.Errorf("after interpreting = pending %d, first %v", after.Pending, after.Notifications[0].ObservationID)
	}
	if _, err := f.ledger.PendingNotifications(t.Context(), f.account, "not-a-cursor", 0); err == nil {
		t.Error("an invalid cursor was accepted")
	}
}

func TestAPendingUpdateCarriesItsEarlierVersions(t *testing.T) {
	f := newFixture(t)
	key := "0|wallet|7|null|10123"
	first := f.walletObservation(walletPurchase(key, "Compra pendente"))
	created := f.interpret(purchaseReading(first, brl(3297)))
	update := walletPurchase(key, "Compra aprovada")
	update.Event, update.PostedAt, update.ReceivedAt = ledger.NotificationUpdated, postedAt.Add(time.Minute), postedAt.Add(2*time.Minute)
	f.ingest(update)

	page, err := f.ledger.PendingNotifications(t.Context(), f.account, "", 0)
	if err != nil {
		t.Fatal(err)
	}

	if len(page.Notifications) != 1 || page.Notifications[0].Version != 2 || len(page.Notifications[0].Previous) != 1 {
		t.Fatalf("pending = %+v, want version 2 with one earlier version", page.Notifications)
	}
	earlier := page.Notifications[0].Previous[0]
	if earlier.Text != "Compra pendente" || earlier.Interpretation == nil || *earlier.Interpretation.TransactionID != created.Transaction.ID {
		t.Errorf("earlier version = %+v, want its text and transaction", earlier)
	}
}
