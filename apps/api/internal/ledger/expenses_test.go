package ledger_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/idempotency"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/redaction"
)

var lateNight = time.Date(2026, 10, 22, 2, 50, 0, 0, time.UTC)

func supermarket(requestID string) ledger.ExpenseReport {
	now := calendar.ReferenceNow
	return ledger.ExpenseReport{
		RequestID:     requestID,
		Conversation:  "conversation-1",
		ReceivedAt:    lateNight,
		RawText:       "Gastei R$ 327,39 no mercado agora",
		Amount:        ptr(brl(32739)),
		Description:   "Compra no mercado",
		DateReference: &now,
		Category:      ptr("groceries"),
	}
}

func (f *fixture) record(report ledger.ExpenseReport) ledger.ExpenseOutcome {
	f.t.Helper()
	outcome, err := f.ledger.RecordExpense(f.t.Context(), f.account, report, agent)
	if err != nil {
		f.t.Fatalf("RecordExpense() error = %v", err)
	}
	return outcome
}

func (f *fixture) count(table string) int {
	f.t.Helper()
	conn, err := pgx.Connect(f.t.Context(), f.databaseURL)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var count int
	if err := conn.QueryRow(f.t.Context(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
		f.t.Fatal(err)
	}
	return count
}

func TestRecordingAnExpenseFromTheConversation(t *testing.T) {
	f := newFixture(t)

	outcome := f.record(supermarket("message-1"))

	if outcome.Result != ledger.ExpenseRecorded {
		t.Fatalf("result = %s", outcome.Result)
	}
	tr := outcome.Transaction
	if tr.Amount != brl(32739) || tr.Description != "Compra no mercado" || tr.OriginalDescription != "Gastei R$ 327,39 no mercado agora" {
		t.Errorf("transaction = %+v", tr)
	}
	if len(outcome.Categories) != 1 || outcome.Categories[0].Code != "groceries" || outcome.Categories[0].Label != "Mercado" || outcome.Categories[0].Status != ledger.AllocationSuggested {
		t.Errorf("categories = %+v", outcome.Categories)
	}
	if tr.Occurred.On.String() != "2026-10-21" || tr.Occurred.Source != calendar.SourceInferred || tr.Occurred.Precision != calendar.PrecisionExact {
		t.Errorf("occurred = %+v, want 21/10 inferred from 23:50 local", tr.Occurred)
	}
	if tr.Status != ledger.StatusProvisional || !slices.Contains(outcome.Pending, "payment_method") {
		t.Errorf("status = %s, pending = %v; want provisional with the payment method pending", tr.Status, outcome.Pending)
	}
	if !slices.Contains(outcome.Inferred, "date") || !slices.Contains(outcome.Inferred, "message_at") {
		t.Errorf("inferred = %v", outcome.Inferred)
	}
}

func TestTheSameRequestIsNeverRecordedTwice(t *testing.T) {
	f := newFixture(t)
	first := f.record(supermarket("message-1"))

	again := f.record(supermarket("message-1"))

	if again.Result != ledger.ExpenseDuplicateOf || again.Transaction.ID != first.Transaction.ID {
		t.Errorf("retry = %s %s, want duplicate_of %s", again.Result, again.Transaction.ID, first.Transaction.ID)
	}
	if f.count("transactions") != 1 || f.count("observations") != 1 {
		t.Errorf("transactions = %d, observations = %d, want 1 and 1", f.count("transactions"), f.count("observations"))
	}
}

func TestTheSameTextInTheSameConversationAsksBeforeRecording(t *testing.T) {
	f := newFixture(t)
	first := f.record(supermarket("message-1"))
	repeated := supermarket("message-2")
	repeated.RawText = "  gastei R$ 327,39 no MERCADO agora "

	warning := f.record(repeated)

	if warning.Result != ledger.ExpensePossibleDuplicate || warning.Transaction.ID != first.Transaction.ID || warning.RepeatedAt == nil {
		t.Fatalf("repeated message = %+v", warning)
	}
	if f.count("transactions") != 1 || f.count("idempotency_keys") != 1 {
		t.Fatalf("a possible duplicate wrote something: %d transactions, %d keys", f.count("transactions"), f.count("idempotency_keys"))
	}

	repeated.ConfirmDistinct = true
	second := f.record(repeated)

	if second.Result != ledger.ExpenseRecorded || second.Transaction.ID == first.Transaction.ID {
		t.Errorf("confirmed second purchase = %s %v", second.Result, second.Transaction)
	}
	if f.count("transactions") != 2 {
		t.Errorf("transactions = %d, want 2", f.count("transactions"))
	}
}

func TestAnotherMessageWithTheSameAmountIsANewExpense(t *testing.T) {
	f := newFixture(t)
	f.record(supermarket("message-1"))
	other := supermarket("message-2")
	other.RawText = "Mais R$ 327,39 no mercado, comprei a carne"
	elsewhere := supermarket("message-3")
	elsewhere.Conversation = "conversation-2"

	if got := f.record(other); got.Result != ledger.ExpenseRecorded {
		t.Errorf("different text = %s, want recorded", got.Result)
	}
	if got := f.record(elsewhere); got.Result != ledger.ExpenseRecorded {
		t.Errorf("other conversation = %s, want recorded", got.Result)
	}
	if f.count("transactions") != 3 {
		t.Errorf("transactions = %d, want 3", f.count("transactions"))
	}
}

func TestAVoidedExpenseDoesNotBlockRecordingItAgain(t *testing.T) {
	f := newFixture(t)
	first := f.record(supermarket("message-1"))
	if _, err := f.ledger.Void(t.Context(), f.account, first.Transaction.ID, "engano", rudolfo); err != nil {
		t.Fatal(err)
	}

	if got := f.record(supermarket("message-2")); got.Result != ledger.ExpenseRecorded {
		t.Errorf("after void = %s, want recorded", got.Result)
	}
}

func TestAPixReportedInTheConversationIsConfirmedAndAsksForThePurpose(t *testing.T) {
	f := newFixture(t)
	report := supermarket("message-1")
	report.RawText, report.Description, report.Category = "Fiz um Pix de R$ 50 pra Ana", "Pix para Ana", nil
	report.Amount = ptr(brl(5000))
	report.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentPix}

	outcome := f.record(report)

	if outcome.Transaction.Status != ledger.StatusConfirmed {
		t.Errorf("status = %s, want confirmed", outcome.Transaction.Status)
	}
	if !slices.Contains(outcome.Pending, "purpose") || !slices.Contains(outcome.Pending, "category") || slices.Contains(outcome.Pending, "payment_method") {
		t.Errorf("pending = %v", outcome.Pending)
	}
	kinds := []ledger.ClarificationKind{}
	for _, c := range outcome.Clarifications {
		kinds = append(kinds, c.Kind)
	}
	if !slices.Contains(kinds, ledger.ClarificationPurposeUnknown) || !slices.Contains(kinds, ledger.ClarificationCategoryUnknown) {
		t.Errorf("clarifications = %v", kinds)
	}
}

func TestMissingAmountOrIdentityAsksInsteadOfRecording(t *testing.T) {
	f := newFixture(t)
	noAmount := supermarket("message-1")
	noAmount.Amount = nil
	noIdentity := supermarket("message-2")
	noIdentity.Description = ""

	for name, report := range map[string]ledger.ExpenseReport{"amount": noAmount, "identity": noIdentity} {
		got := f.record(report)
		if got.Result != ledger.ExpenseNeedsClarification || got.Question == "" || got.Transaction != nil {
			t.Errorf("%s missing = %+v", name, got)
		}
	}
	if f.count("transactions") != 0 || f.count("idempotency_keys") != 0 {
		t.Errorf("needs_clarification wrote something")
	}
}

func TestMerchantAloneIdentifiesTheExpense(t *testing.T) {
	f := newFixture(t)
	report := supermarket("message-1")
	report.Description, report.Merchant = "", ptr("Padaria Real")

	outcome := f.record(report)

	if outcome.Result != ledger.ExpenseRecorded || outcome.Transaction.Description != "Padaria Real" {
		t.Errorf("outcome = %s %+v", outcome.Result, outcome.Transaction)
	}
}

func TestStatedDatesAndConfirmedCategoriesAreKeptAsGiven(t *testing.T) {
	f := newFixture(t)
	report := supermarket("message-1")
	report.DateReference = nil
	report.OccurredOn = &calendar.Date{Year: 2026, Month: time.October, Day: 18}
	report.CategoryStatus = ledger.AllocationConfirmed
	report.Installments = &ledger.Installments{Count: 3}

	outcome := f.record(report)

	if outcome.Transaction.Occurred.Source != calendar.SourceStated || outcome.Transaction.Occurred.On.String() != "2026-10-18" || len(outcome.Inferred) != 0 {
		t.Errorf("occurred = %+v, inferred = %v", outcome.Transaction.Occurred, outcome.Inferred)
	}
	if outcome.Categories[0].Status != ledger.AllocationConfirmed || outcome.Categories[0].Origin != ledger.AuthorUser {
		t.Errorf("categories = %+v", outcome.Categories)
	}
	if outcome.Transaction.Installments == nil || outcome.Transaction.Installments.Count != 3 || outcome.Transaction.Installments.Amount != nil {
		t.Errorf("installments = %+v", outcome.Transaction.Installments)
	}
}

func TestSecretsInTheMessageNeverReachTheTransaction(t *testing.T) {
	f := newFixture(t)
	report := supermarket("message-1")
	report.RawText = "Gastei R$ 327,39 no cartão 4111 1111 1111 1111, o código era 482913"
	report.Description = "Mercado no cartão 4111111111111111"

	outcome := f.record(report)

	for _, text := range []string{outcome.Transaction.OriginalDescription, outcome.Transaction.Description} {
		if strings.Contains(text, "4111 1111 1111 1111") || strings.Contains(text, "4111111111111111") || strings.Contains(text, "482913") {
			t.Errorf("secret reached the transaction: %q", text)
		}
	}
	detail, err := f.ledger.GetTransaction(t.Context(), f.account, outcome.Transaction.ID)
	if err != nil {
		t.Fatal(err)
	}
	observations, err := f.ledger.Interpretations(t.Context(), f.account, detail.Evidence[0].ObservationID)
	if err != nil || len(observations) != 1 {
		t.Fatalf("interpretations = %+v, %v", observations, err)
	}
	if strings.Contains(observations[0].Details["description"].(string), "4111111111111111") {
		t.Errorf("interpretation keeps the card number: %+v", observations[0].Details)
	}
	conn, err := pgx.Connect(t.Context(), f.databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var findings []redaction.Finding
	if err := conn.QueryRow(t.Context(), "SELECT redactions FROM observations").Scan(&findings); err != nil {
		t.Fatal(err)
	}
	if len(findings) != 2 || findings[0].Kind != redaction.KindCardNumber || findings[0].Count != 2 {
		t.Errorf("redactions = %+v", findings)
	}
}

func TestRecordExpenseRejectsInconsistentReports(t *testing.T) {
	f := newFixture(t)
	today := calendar.ReferenceToday
	tests := map[string]struct {
		change func(*ledger.ExpenseReport)
		field  string
	}{
		"no request id": {func(r *ledger.ExpenseReport) { r.RequestID = " " }, "request_id"},
		"no raw text":   {func(r *ledger.ExpenseReport) { r.RawText = "" }, "raw_text"},
		"two dates": {func(r *ledger.ExpenseReport) {
			r.DateReference = &today
			r.OccurredOn = &calendar.Date{Year: 2026, Month: 10, Day: 1}
		}, "date_reference"},
		"future message":    {func(r *ledger.ExpenseReport) { r.MessageAt = ptr(lateNight.Add(3 * time.Hour)) }, "message_at"},
		"unknown category":  {func(r *ledger.ExpenseReport) { r.Category = ptr("pets") }, "categories"},
		"rule confirmation": {func(r *ledger.ExpenseReport) { r.CategoryStatus = "maybe" }, "category_status"},
		"bad input mode":    {func(r *ledger.ExpenseReport) { r.InputMode = "video" }, "input_mode"},
	}
	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			report := supermarket("message-" + name)
			tt.change(&report)
			_, err := f.ledger.RecordExpense(t.Context(), f.account, report, agent)
			assertValidation(t, err, tt.field)
		})
	}
	if f.count("transactions") != 0 {
		t.Errorf("rejected reports wrote %d transactions", f.count("transactions"))
	}
}

func TestReusingARequestIDForAnotherExpenseIsAConflict(t *testing.T) {
	f := newFixture(t)
	f.record(supermarket("message-1"))
	other := supermarket("message-1")
	other.Amount = ptr(brl(1000))

	_, err := f.ledger.RecordExpense(t.Context(), f.account, other, agent)

	if !errors.Is(err, idempotency.ErrConflict) {
		t.Errorf("error = %v, want idempotency.ErrConflict", err)
	}
}
