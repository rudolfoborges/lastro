package ledger_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
	"github.com/rudolfoborges/lastro/apps/api/internal/redaction"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

type fixture struct {
	t       *testing.T
	store   *store.Store
	ledger  *ledger.Service
	account uuid.UUID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	s, err := store.Open(t.Context(), storetest.MigratedDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	f := &fixture{t: t, store: s, ledger: ledger.NewService(s)}
	f.account = f.newAccount("Rudolfo")
	return f
}

func (f *fixture) newAccount(name string) uuid.UUID {
	f.t.Helper()
	account, err := f.store.Platform().CreateAccount(f.t.Context(), platformdb.CreateAccountParams{ID: store.NewID(), Name: name})
	if err != nil {
		f.t.Fatal(err)
	}
	return account.ID
}

func (f *fixture) observation(key, text string) ledger.Observation {
	f.t.Helper()
	observation, err := f.ledger.RecordObservation(f.t.Context(), f.account, ledger.NewObservation{
		Source:     ledger.SourceConversation,
		SourceKey:  key,
		Payload:    map[string]any{"raw_text": text},
		ReceivedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		f.t.Fatalf("RecordObservation() error = %v", err)
	}
	return observation
}

var agent = ledger.Actor{Author: ledger.AuthorAgent, Ref: "test-key"}

func brl(minor int64) money.Money {
	return money.Money{Minor: minor, Currency: "BRL"}
}

func ptr[T any](v T) *T {
	return &v
}

func groceries(amount money.Money) ledger.NewTransaction {
	day := calendar.Date{Year: 2026, Month: time.October, Day: 21}
	return ledger.NewTransaction{
		OriginalDescription: "Gastei R$ 327,39 no mercado",
		Description:         "Compra no mercado",
		Merchant:            ptr("Mercado"),
		Amount:              amount,
		Occurred:            calendar.OnDay(day, calendar.SourceInferred),
		PaymentMethod:       &ledger.PaymentMethodRef{Kind: ledger.PaymentCreditCard, CardLast4: ptr("1234")},
		Nature:              ledger.NatureExpense,
		Status:              ledger.StatusProvisional,
	}
}

func (f *fixture) transaction(key string) (ledger.Transaction, ledger.Observation) {
	f.t.Helper()
	observation := f.observation(key, "Gastei R$ 327,39 no mercado")
	created, err := f.ledger.CreateTransaction(f.t.Context(), f.account, observation.ID, groceries(brl(32739)), agent)
	if err != nil {
		f.t.Fatalf("CreateTransaction() error = %v", err)
	}
	return created, observation
}

func assertValidation(t *testing.T, err error, field string) {
	t.Helper()
	var validation *ledger.ValidationError
	if !errors.As(err, &validation) || validation.Field != field {
		t.Errorf("error = %v, want validation error on %s", err, field)
	}
}

func TestObservationsWithTheSameSourceIdentityAreRejected(t *testing.T) {
	f := newFixture(t)
	f.observation("message-1", "Gastei R$ 10,00")

	_, err := f.ledger.RecordObservation(t.Context(), f.account, ledger.NewObservation{
		Source:     ledger.SourceConversation,
		SourceKey:  "message-1",
		Payload:    map[string]any{"raw_text": "outra coisa"},
		ReceivedAt: time.Now(),
	})

	if !errors.Is(err, ledger.ErrDuplicateObservation) {
		t.Errorf("RecordObservation(same key) error = %v, want ErrDuplicateObservation", err)
	}
}

func TestTheSameSourceKeyIsIndependentAcrossSourcesAndAccounts(t *testing.T) {
	f := newFixture(t)
	f.observation("key-1", "Gastei R$ 10,00")

	_, err := f.ledger.RecordObservation(t.Context(), f.account, ledger.NewObservation{
		Source: ledger.SourceWalletNotification, SourceKey: "key-1", ReceivedAt: time.Now(),
	})
	if err != nil {
		t.Errorf("same key from another source error = %v", err)
	}
	_, err = f.ledger.RecordObservation(t.Context(), f.newAccount("Bob"), ledger.NewObservation{
		Source: ledger.SourceConversation, SourceKey: "key-1", ReceivedAt: time.Now(),
	})
	if err != nil {
		t.Errorf("same key in another account error = %v", err)
	}
}

func TestObservationPayloadsAreRedactedBeforeTheyAreStored(t *testing.T) {
	f := newFixture(t)

	observation := f.observation("message-1", "paguei com o cartão 4111 1111 1111 1111, código 482913")

	if observation.Payload["raw_text"] != "paguei com o cartão **** **** **** 1111, código [redacted]" {
		t.Errorf("payload = %+v", observation.Payload)
	}
	want := []redaction.Finding{{Kind: redaction.KindCardNumber, Count: 1}, {Kind: redaction.KindSecret, Count: 1}}
	if len(observation.Redactions) != 2 || observation.Redactions[0] != want[0] || observation.Redactions[1] != want[1] {
		t.Errorf("redactions = %+v, want %+v", observation.Redactions, want)
	}
}

func TestCreatingATransactionFromAnObservationLinksItAsConfirmedEvidence(t *testing.T) {
	f := newFixture(t)

	created, observation := f.transaction("message-1")

	detail, err := f.ledger.GetTransaction(t.Context(), f.account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Amount != brl(32739) || detail.Status != ledger.StatusProvisional || detail.Nature != ledger.NatureExpense {
		t.Errorf("transaction = %+v", detail.Transaction)
	}
	if detail.Occurred.On.String() != "2026-10-21" || detail.Occurred.Precision != calendar.PrecisionDay || detail.Occurred.Source != calendar.SourceInferred {
		t.Errorf("occurred = %+v", detail.Occurred)
	}
	if detail.PaymentMethod == nil || detail.PaymentMethod.Kind != ledger.PaymentCreditCard || *detail.PaymentMethod.CardLast4 != "1234" {
		t.Errorf("payment method = %+v", detail.PaymentMethod)
	}
	if len(detail.Evidence) != 1 || detail.Evidence[0].ObservationID != observation.ID || detail.Evidence[0].Status != ledger.EvidenceConfirmed || detail.Evidence[0].Source != ledger.SourceConversation {
		t.Errorf("evidence = %+v", detail.Evidence)
	}
	fields := map[ledger.Field]bool{}
	for _, source := range detail.Fields {
		if source.ObservationID == nil || *source.ObservationID != observation.ID || source.ConfirmedByUser {
			t.Errorf("field source = %+v", source)
		}
		fields[source.Field] = true
	}
	for _, field := range []ledger.Field{ledger.FieldDescription, ledger.FieldAmount, ledger.FieldMerchant, ledger.FieldOccurred, ledger.FieldPaymentMethod, ledger.FieldNature} {
		if !fields[field] {
			t.Errorf("provenance misses %s: %+v", field, detail.Fields)
		}
	}
	if fields[ledger.FieldPurpose] || fields[ledger.FieldPayee] {
		t.Errorf("provenance lists fields that were not given: %+v", detail.Fields)
	}
}

func TestPaymentMethodsAreReusedByKindAndLastDigits(t *testing.T) {
	f := newFixture(t)
	first, _ := f.transaction("message-1")
	second, _ := f.transaction("message-2")

	if first.PaymentMethod.ID != second.PaymentMethod.ID {
		t.Errorf("same card got two payment methods: %s and %s", first.PaymentMethod.ID, second.PaymentMethod.ID)
	}

	observation := f.observation("message-3", "Pix de R$ 50")
	in := groceries(brl(5000))
	in.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentPix}
	pix, err := f.ledger.CreateTransaction(t.Context(), f.account, observation.ID, in, agent)
	if err != nil {
		t.Fatal(err)
	}
	if pix.PaymentMethod.ID == first.PaymentMethod.ID || pix.PaymentMethod.CardLast4 != nil {
		t.Errorf("pix payment method = %+v", pix.PaymentMethod)
	}
}

func TestEvidenceCanBeUnlinkedAndTheObservationLinkedElsewhere(t *testing.T) {
	f := newFixture(t)
	first, observation := f.transaction("message-1")
	second, _ := f.transaction("message-2")

	_, err := f.ledger.LinkEvidence(t.Context(), f.account, second.ID, observation.ID, ledger.EvidenceLink{Status: ledger.EvidenceConfirmed, Origin: ledger.OriginAgent})
	if !errors.Is(err, ledger.ErrObservationClaimed) {
		t.Fatalf("confirming a claimed observation error = %v, want ErrObservationClaimed", err)
	}

	original, err := f.ledger.TransactionEvidence(t.Context(), f.account, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	undone, err := f.ledger.SetEvidenceStatus(t.Context(), f.account, original[0].ID, ledger.EvidenceUndone, ledger.OriginUser)
	if err != nil || undone.Status != ledger.EvidenceUndone || undone.Origin != ledger.OriginUser {
		t.Fatalf("undo = %+v, %v", undone, err)
	}

	moved, err := f.ledger.LinkEvidence(t.Context(), f.account, second.ID, observation.ID, ledger.EvidenceLink{
		Status: ledger.EvidenceConfirmed, Origin: ledger.OriginAgent, Score: ptr(90), Reasons: []string{"same amount and day"},
	})
	if err != nil || moved.Status != ledger.EvidenceConfirmed || *moved.Score != 90 {
		t.Fatalf("relink = %+v, %v", moved, err)
	}

	history, err := f.ledger.TransactionEvidence(t.Context(), f.account, first.ID)
	if err != nil || len(history) != 1 || history[0].Status != ledger.EvidenceUndone {
		t.Errorf("first transaction keeps the undone link: %+v, %v", history, err)
	}
	if _, err := f.ledger.SetEvidenceStatus(t.Context(), f.account, original[0].ID, ledger.EvidenceConfirmed, ledger.OriginUser); !errors.Is(err, ledger.ErrEvidenceClosed) {
		t.Errorf("reopening an undone link error = %v, want ErrEvidenceClosed", err)
	}
}

func TestSuggestedEvidenceCanBeConfirmedOrRejected(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	notification := f.observation("notification-1", "Compra aprovada R$ 327,39")
	other := f.observation("notification-2", "Compra aprovada R$ 327,39")

	suggested, err := f.ledger.LinkEvidence(t.Context(), f.account, created.ID, notification.ID, ledger.EvidenceLink{Status: ledger.EvidenceSuggested, Origin: ledger.OriginSystem})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.ledger.SetEvidenceStatus(t.Context(), f.account, suggested.ID, ledger.EvidenceConfirmed, ledger.OriginAgent); err != nil {
		t.Errorf("confirm suggested error = %v", err)
	}
	rejected, err := f.ledger.LinkEvidence(t.Context(), f.account, created.ID, other.ID, ledger.EvidenceLink{Status: ledger.EvidenceSuggested, Origin: ledger.OriginSystem})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := f.ledger.SetEvidenceStatus(t.Context(), f.account, rejected.ID, ledger.EvidenceRejected, ledger.OriginUser); err != nil || got.Status != ledger.EvidenceRejected {
		t.Errorf("reject = %+v, %v", got, err)
	}
}

func TestAccountsCannotReachEachOthersTransactions(t *testing.T) {
	f := newFixture(t)
	created, observation := f.transaction("message-1")
	bob := f.newAccount("Bob")

	if _, err := f.ledger.GetTransaction(t.Context(), bob, created.ID); !errors.Is(err, ledger.ErrNotFound) {
		t.Errorf("GetTransaction as Bob error = %v, want ErrNotFound", err)
	}
	if _, err := f.ledger.CreateTransaction(t.Context(), bob, observation.ID, groceries(brl(100)), agent); !errors.Is(err, ledger.ErrNotFound) {
		t.Errorf("CreateTransaction from Alice's observation as Bob error = %v, want ErrNotFound", err)
	}
}

func TestCreateTransactionValidatesTheData(t *testing.T) {
	f := newFixture(t)
	observation := f.observation("message-1", "texto")
	day := calendar.Date{Year: 2026, Month: time.October, Day: 21}
	tests := map[string]struct {
		change func(*ledger.NewTransaction)
		field  string
	}{
		"zero amount":         {func(in *ledger.NewTransaction) { in.Amount = brl(0) }, "amount"},
		"negative amount":     {func(in *ledger.NewTransaction) { in.Amount = brl(-100) }, "amount"},
		"unknown currency":    {func(in *ledger.NewTransaction) { in.Amount = money.Money{Minor: 100, Currency: "XXX"} }, "amount"},
		"empty description":   {func(in *ledger.NewTransaction) { in.Description = "  " }, "description"},
		"blank merchant":      {func(in *ledger.NewTransaction) { in.Merchant = ptr("") }, "merchant"},
		"day with an instant": {func(in *ledger.NewTransaction) { in.Occurred.At = ptr(time.Now()) }, "date_precision"},
		"month not on day one": {func(in *ledger.NewTransaction) {
			in.Occurred = ledger.NewTransaction{}.Occurred
			in.Occurred.Precision = calendar.PrecisionMonth
			in.Occurred.On = &day
			in.Occurred.Source = calendar.SourceStated
		}, "date_precision"},
		"last digits on pix": {func(in *ledger.NewTransaction) {
			in.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentPix, CardLast4: ptr("1234")}
		}, "card_last4"},
		"short last digits": {func(in *ledger.NewTransaction) { in.PaymentMethod.CardLast4 = ptr("123") }, "card_last4"},
		"unknown nature":    {func(in *ledger.NewTransaction) { in.Nature = "gift" }, "nature"},
		"born voided":       {func(in *ledger.NewTransaction) { in.Status = ledger.StatusVoided }, "status"},
		"one installment":   {func(in *ledger.NewTransaction) { in.Installments = &ledger.Installments{Count: 1} }, "installment_count"},
		"installment in USD": {func(in *ledger.NewTransaction) {
			in.Installments = &ledger.Installments{Count: 3, Amount: &money.Money{Minor: 100, Currency: "USD"}}
		}, "installment_amount"},
		"same original currency": {func(in *ledger.NewTransaction) { in.OriginalAmount = ptr(brl(100)) }, "original_amount"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			in := groceries(brl(32739))
			tt.change(&in)
			_, err := f.ledger.CreateTransaction(t.Context(), f.account, observation.ID, in, agent)
			assertValidation(t, err, tt.field)
		})
	}
}

func TestInstallmentsAndForeignAmountsAreKept(t *testing.T) {
	f := newFixture(t)
	observation := f.observation("message-1", "Comprei uma TV de R$ 1.200 em 12x de R$ 100")
	in := groceries(brl(120000))
	in.Installments = &ledger.Installments{Count: 12, Amount: ptr(brl(10000))}
	in.OriginalAmount = &money.Money{Minor: 22000, Currency: "USD"}

	created, err := f.ledger.CreateTransaction(t.Context(), f.account, observation.ID, in, agent)

	if err != nil {
		t.Fatal(err)
	}
	if created.Installments == nil || created.Installments.Count != 12 || *created.Installments.Amount != brl(10000) {
		t.Errorf("installments = %+v", created.Installments)
	}
	if created.OriginalAmount == nil || *created.OriginalAmount != (money.Money{Minor: 22000, Currency: "USD"}) {
		t.Errorf("original amount = %+v", created.OriginalAmount)
	}
}

func TestEachReadingOfAnObservationIsANewInterpretationVersion(t *testing.T) {
	f := newFixture(t)
	observation := f.observation("notification-1", "Compra aprovada")
	first := ledger.Interpretation{
		EventType: ledger.EventPurchase,
		Amount:    ptr(brl(32739)),
		Merchant:  ptr("MERCADO"),
		Occurred:  calendar.Unknown(),
		Author:    ledger.AuthorAgent,
		Details:   map[string]any{"note": "cartão 4111111111111111"},
	}
	second := first
	second.EventType = ledger.EventCancellation
	second.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentCreditCard, CardLast4: ptr("1111")}

	for _, in := range []ledger.Interpretation{first, second} {
		if _, err := f.ledger.RecordInterpretation(t.Context(), f.account, observation.ID, in); err != nil {
			t.Fatal(err)
		}
	}

	records, err := f.ledger.Interpretations(t.Context(), f.account, observation.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 2 || records[0].Version != 1 || records[1].Version != 2 {
		t.Fatalf("interpretations = %+v", records)
	}
	if records[0].EventType != ledger.EventPurchase || records[1].EventType != ledger.EventCancellation || *records[1].PaymentMethod.CardLast4 != "1111" {
		t.Errorf("interpretations = %+v", records)
	}
	if records[0].Details["note"] != "cartão ************1111" {
		t.Errorf("details were not redacted: %+v", records[0].Details)
	}
	_, err = f.ledger.RecordInterpretation(t.Context(), f.account, observation.ID, ledger.Interpretation{EventType: "guess", Occurred: calendar.Unknown(), Author: ledger.AuthorAgent})
	assertValidation(t, err, "event_type")
}
