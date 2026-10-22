package ledger_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

var rudolfo = ledger.Actor{Author: ledger.AuthorUser, Ref: "case-key"}

func TestEveryCorrectionIsRecordedWithReasonAuthorAndValues(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	correction, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{
		Merchant: ledger.Value("Pão de Açúcar"),
		Amount:   ptr(brl(32793)),
	}, "Rudolfo corrigiu o valor e o mercado", rudolfo)

	if err != nil {
		t.Fatal(err)
	}
	if len(correction.Changed) != 2 || *correction.Transaction.Merchant != "Pão de Açúcar" || correction.Transaction.Amount != brl(32793) {
		t.Fatalf("correction = %+v", correction)
	}
	history, err := f.ledger.History(t.Context(), f.account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 3 || history[0].Field != "created" || history[0].Reason != "Registrado a partir da conversa." || history[0].Author != ledger.AuthorAgent {
		t.Fatalf("history = %+v", history)
	}
	merchant, amount := history[1], history[2]
	if merchant.Field != "merchant" || string(merchant.OldValue) != `"Mercado"` || string(merchant.NewValue) != `"Pão de Açúcar"` {
		t.Errorf("merchant revision = %+v", merchant)
	}
	if amount.Field != "amount" || amount.Reason != "Rudolfo corrigiu o valor e o mercado" || amount.Author != ledger.AuthorUser || *amount.ActorRef != "case-key" {
		t.Errorf("amount revision = %+v", amount)
	}
	var before, after map[string]string
	_ = json.Unmarshal(amount.OldValue, &before)
	_ = json.Unmarshal(amount.NewValue, &after)
	if before["amount"] != "327.39" || after["amount"] != "327.93" || after["currency"] != "BRL" {
		t.Errorf("amount values = %s -> %s", amount.OldValue, amount.NewValue)
	}
	for i := 1; i < len(history); i++ {
		if history[i].CreatedAt.Before(history[i-1].CreatedAt) {
			t.Errorf("history is not chronological: %v before %v", history[i].CreatedAt, history[i-1].CreatedAt)
		}
	}
}

func TestCorrectingTheAmountKeepsWhatTheSourceSaid(t *testing.T) {
	f := newFixture(t)
	created, observation := f.transaction("message-1")

	if _, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Amount: ptr(brl(1000))}, "valor errado", rudolfo); err != nil {
		t.Fatal(err)
	}

	detail, err := f.ledger.GetTransaction(t.Context(), f.account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Amount != brl(1000) || detail.OriginalDescription != "Gastei R$ 327,39 no mercado" {
		t.Errorf("transaction = %+v", detail.Transaction)
	}
	if len(detail.Evidence) != 1 || detail.Evidence[0].ObservationID != observation.ID {
		t.Fatalf("evidence = %+v", detail.Evidence)
	}
	if observation.Payload["raw_text"] != "Gastei R$ 327,39 no mercado" {
		t.Errorf("observation payload = %+v", observation.Payload)
	}
}

func TestFieldsConfirmedByRudolfoAreNotOverwrittenByAutomaticChanges(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if _, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Merchant: ledger.Value("Feira")}, "era a feira", rudolfo); err != nil {
		t.Fatal(err)
	}
	statement := f.observation("statement-line-1", "MERCADO EXTRA 327,39")

	correction, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{
		Merchant:    ledger.Value("Mercado Extra"),
		Description: ptr("Compra no Mercado Extra"),
	}, "conferência da fatura", ledger.Actor{Author: ledger.AuthorSystem, ObservationID: &statement.ID})

	if err != nil {
		t.Fatal(err)
	}
	if *correction.Transaction.Merchant != "Feira" || correction.Transaction.Description != "Compra no Mercado Extra" {
		t.Errorf("transaction = %+v", correction.Transaction)
	}
	if len(correction.Conflicts) != 1 || correction.Conflicts[0].Field != ledger.FieldMerchant {
		t.Fatalf("conflicts = %+v", correction.Conflicts)
	}
	var current, proposed string
	_ = json.Unmarshal(correction.Conflicts[0].Current, &current)
	_ = json.Unmarshal(correction.Conflicts[0].Proposed, &proposed)
	if current != "Feira" || proposed != "Mercado Extra" {
		t.Errorf("conflict = %q vs %q", current, proposed)
	}
	if len(correction.Changed) != 1 || correction.Changed[0] != ledger.FieldDescription {
		t.Errorf("changed = %v, want only the unlocked description", correction.Changed)
	}

	again, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Merchant: ledger.Value("Feira livre")}, "nome completo", rudolfo)
	if err != nil || *again.Transaction.Merchant != "Feira livre" {
		t.Errorf("Rudolfo can still change his own field: %+v, %v", again.Transaction.Merchant, err)
	}
}

func TestChangingThePaymentMethodMovesTheStatusAlongTheRule(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if created.Status != ledger.StatusProvisional {
		t.Fatalf("credit card purchase status = %s, want provisional", created.Status)
	}

	pix, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{PaymentMethod: ledger.Value(ledger.PaymentMethodRef{Kind: ledger.PaymentPix})}, "foi Pix", rudolfo)
	if err != nil || pix.Transaction.Status != ledger.StatusConfirmed || pix.Transaction.PaymentMethod.Kind != ledger.PaymentPix {
		t.Fatalf("after Pix = %+v, %v", pix.Transaction, err)
	}
	unknown, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{PaymentMethod: ledger.Cleared[ledger.PaymentMethodRef]()}, "não sei o meio", rudolfo)
	if err != nil || unknown.Transaction.Status != ledger.StatusProvisional || unknown.Transaction.PaymentMethod != nil {
		t.Fatalf("after clearing = %+v, %v", unknown.Transaction, err)
	}

	history, err := f.ledger.History(t.Context(), f.account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	statusChanges := 0
	for _, revision := range history {
		if revision.Field == "status" {
			statusChanges++
			if revision.Author != ledger.AuthorSystem || revision.Reason != "Situação ajustada ao meio de pagamento." {
				t.Errorf("status revision = %+v", revision)
			}
		}
	}
	if statusChanges != 2 {
		t.Errorf("status revisions = %d, want 2", statusChanges)
	}
}

func TestVoidingKeepsTheTransactionAndClosesItForChanges(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	voided, err := f.ledger.Void(t.Context(), f.account, created.ID, "registrado por engano", rudolfo)
	if err != nil || voided.Status != ledger.StatusVoided {
		t.Fatalf("Void() = %+v, %v", voided, err)
	}
	if again, err := f.ledger.Void(t.Context(), f.account, created.ID, "de novo", rudolfo); err != nil || again.Status != ledger.StatusVoided {
		t.Errorf("second Void() = %+v, %v", again, err)
	}
	_, err = f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Description: ptr("outra")}, "tentativa", rudolfo)
	if !errors.Is(err, ledger.ErrTransactionClosed) {
		t.Errorf("Correct() after void error = %v, want ErrTransactionClosed", err)
	}
	history, err := f.ledger.History(t.Context(), f.account, created.ID)
	if err != nil || len(history) != 2 || history[1].Field != "status" || string(history[1].NewValue) != `"voided"` {
		t.Errorf("history = %+v, %v", history, err)
	}
}

func TestCorrectionsNeedAReasonAndValidData(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	_, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Description: ptr("x")}, "  ", rudolfo)
	assertValidation(t, err, "reason")
	_, err = f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Amount: ptr(brl(-5))}, "valor", rudolfo)
	assertValidation(t, err, "amount")
	_, err = f.ledger.Void(t.Context(), f.account, created.ID, "", rudolfo)
	assertValidation(t, err, "reason")

	history, err := f.ledger.History(t.Context(), f.account, created.ID)
	if err != nil || len(history) != 1 {
		t.Errorf("rejected corrections left revisions: %+v, %v", history, err)
	}
}

func TestClearingAFieldAndRepeatingAValueAreHandled(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	cleared, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Merchant: ledger.Cleared[string]()}, "sem loja", rudolfo)
	if err != nil || cleared.Transaction.Merchant != nil {
		t.Fatalf("clear = %+v, %v", cleared.Transaction.Merchant, err)
	}
	same, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Amount: ptr(brl(32739))}, "mesmo valor", rudolfo)
	if err != nil || len(same.Changed) != 0 {
		t.Errorf("repeating the amount = %+v, %v; want no change", same.Changed, err)
	}
	if _, err := f.ledger.History(t.Context(), f.account, f.newAccount("Bob")); !errors.Is(err, ledger.ErrNotFound) {
		t.Errorf("History(unknown) error = %v, want ErrNotFound", err)
	}
}
