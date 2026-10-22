package ledger_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func (f *fixture) openClarifications(kinds ...ledger.ClarificationKind) []ledger.Clarification {
	f.t.Helper()
	clarifications, err := f.ledger.Clarifications(f.t.Context(), f.account, ledger.ClarificationFilter{Kinds: kinds})
	if err != nil {
		f.t.Fatal(err)
	}
	return clarifications
}

func (f *fixture) pix(key string, purpose *string) ledger.Transaction {
	f.t.Helper()
	observation := f.observation(key, "Fiz um Pix de R$ 50")
	in := groceries(brl(5000))
	in.Description = "Pix para Ana"
	in.Merchant = nil
	in.PaymentMethod = &ledger.PaymentMethodRef{Kind: ledger.PaymentPix}
	in.Status = ledger.StatusConfirmed
	in.Purpose = purpose
	created, err := f.ledger.CreateTransaction(f.t.Context(), f.account, observation.ID, in, agent)
	if err != nil {
		f.t.Fatal(err)
	}
	return created
}

func TestExpensesWithoutCategoryAreListedAsClarifications(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")

	open := f.openClarifications(ledger.ClarificationCategoryUnknown)

	if len(open) != 1 || *open[0].TransactionID != created.ID || open[0].Transaction == nil {
		t.Fatalf("clarifications = %+v", open)
	}
	if open[0].Question != `Qual a categoria de "Compra no mercado" (R$ 327,39, 21/10/2026)?` {
		t.Errorf("question = %q", open[0].Question)
	}

	if _, err := f.categorize(created, []ledger.Split{{Code: "groceries"}}, ledger.AllocationSuggested, agent); err != nil {
		t.Fatal(err)
	}
	if open := f.openClarifications(ledger.ClarificationCategoryUnknown); len(open) != 0 {
		t.Errorf("category clarification still open after classification: %+v", open)
	}
	resolved, err := f.ledger.Clarifications(t.Context(), f.account, ledger.ClarificationFilter{Status: ledger.ClarificationResolved})
	if err != nil || len(resolved) != 1 || *resolved[0].ResolvedBy != ledger.AuthorSystem {
		t.Errorf("resolved = %+v, %v", resolved, err)
	}
}

func TestAPixWithoutPurposeStaysOpenUntilRudolfoAnswers(t *testing.T) {
	f := newFixture(t)
	created := f.pix("message-1", nil)
	if _, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Description: ptr("Pix para Ana Souza")}, "nome completo", rudolfo); err != nil {
		t.Fatal(err)
	}

	open := f.openClarifications(ledger.ClarificationPurposeUnknown)
	if len(open) != 1 || !strings.Contains(open[0].Question, "Qual a finalidade do Pix") {
		t.Fatalf("purpose clarifications = %+v", open)
	}

	resolved, err := f.ledger.ResolveClarification(t.Context(), f.account, open[0].ID, ledger.Resolution{
		Action: ledger.ResolutionResolve, Note: "Presente de aniversário da Ana", Purpose: ptr("presente de aniversário"),
	}, rudolfo)

	if err != nil {
		t.Fatal(err)
	}
	if resolved.Clarification.Status != ledger.ClarificationResolved || *resolved.Clarification.ResolvedBy != ledger.AuthorUser {
		t.Errorf("clarification = %+v", resolved.Clarification)
	}
	if resolved.Correction == nil || *resolved.Correction.Transaction.Purpose != "presente de aniversário" {
		t.Errorf("correction = %+v", resolved.Correction)
	}
	if remaining := f.openClarifications(ledger.ClarificationPurposeUnknown); len(remaining) != 0 {
		t.Errorf("purpose clarifications after answer = %+v", remaining)
	}
	history, err := f.ledger.History(t.Context(), f.account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	fields := []string{}
	for _, revision := range history {
		fields = append(fields, revision.Field)
	}
	if got := strings.Join(fields, ","); got != "created,description,clarification,purpose" {
		t.Errorf("history fields = %s", got)
	}
}

func TestPixWithPurposeAndOtherMethodsDoNotAskForPurpose(t *testing.T) {
	f := newFixture(t)
	f.pix("message-1", ptr("aluguel"))
	f.transaction("message-2")

	if open := f.openClarifications(ledger.ClarificationPurposeUnknown); len(open) != 0 {
		t.Errorf("purpose clarifications = %+v", open)
	}
}

func TestResolvingAMissingCategoryNeedsTheCategory(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	open := f.openClarifications(ledger.ClarificationCategoryUnknown)

	_, err := f.ledger.ResolveClarification(t.Context(), f.account, open[0].ID, ledger.Resolution{Action: ledger.ResolutionResolve, Note: "é mercado"}, rudolfo)
	assertValidation(t, err, "categories")

	resolved, err := f.ledger.ResolveClarification(t.Context(), f.account, open[0].ID, ledger.Resolution{
		Action: ledger.ResolutionResolve, Note: "é mercado", Categories: []ledger.Split{{Code: "groceries"}},
	}, rudolfo)
	if err != nil {
		t.Fatal(err)
	}
	allocations, err := f.ledger.Allocations(t.Context(), f.account, created.ID)
	if err != nil || allocations[0].Code != "groceries" || allocations[0].Status != ledger.AllocationConfirmed {
		t.Errorf("allocations = %+v, %v", allocations, err)
	}
	if _, err := f.ledger.ResolveClarification(t.Context(), f.account, resolved.Clarification.ID, ledger.Resolution{Action: ledger.ResolutionDismiss, Note: "de novo"}, rudolfo); !errors.Is(err, ledger.ErrClarificationClosed) {
		t.Errorf("closing twice error = %v, want ErrClarificationClosed", err)
	}
}

func TestADismissedClarificationIsNotAskedAgainForTheSameCause(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	open := f.openClarifications(ledger.ClarificationCategoryUnknown)

	if _, err := f.ledger.ResolveClarification(t.Context(), f.account, open[0].ID, ledger.Resolution{Action: ledger.ResolutionDismiss, Note: "não lembro"}, rudolfo); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Description: ptr("Mercado do bairro")}, "nome", rudolfo); err != nil {
		t.Fatal(err)
	}

	if open := f.openClarifications(ledger.ClarificationCategoryUnknown); len(open) != 0 {
		t.Errorf("dismissed clarification came back: %+v", open)
	}
	allocations, _ := f.ledger.Allocations(t.Context(), f.account, created.ID)
	if allocations[0].Code != "unidentified" {
		t.Errorf("allocations = %+v, want it still counted as unidentified", allocations)
	}
}

func TestAnAutomaticChangeToALockedFieldOpensAFieldConflict(t *testing.T) {
	f := newFixture(t)
	created, _ := f.transaction("message-1")
	if _, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Merchant: ledger.Value("Feira")}, "era a feira", rudolfo); err != nil {
		t.Fatal(err)
	}
	statement := f.observation("statement-line-1", "MERCADO EXTRA")
	system := ledger.Actor{Author: ledger.AuthorSystem, ObservationID: &statement.ID}

	for range 2 {
		if _, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Merchant: ledger.Value("Mercado Extra")}, "fatura", system); err != nil {
			t.Fatal(err)
		}
	}

	conflicts := f.openClarifications(ledger.ClarificationFieldConflict)
	if len(conflicts) != 1 || conflicts[0].Subject != "merchant" || *conflicts[0].ObservationID != statement.ID {
		t.Fatalf("conflicts = %+v", conflicts)
	}
	if conflicts[0].Context["current"] != "Feira" || conflicts[0].Context["proposed"] != "Mercado Extra" {
		t.Errorf("context = %+v", conflicts[0].Context)
	}
	if !strings.Contains(conflicts[0].Question, "o estabelecimento") {
		t.Errorf("question = %q", conflicts[0].Question)
	}

	if _, err := f.ledger.Correct(t.Context(), f.account, created.ID, ledger.Patch{Merchant: ledger.Value("Mercado Extra")}, "a fatura está certa", rudolfo); err != nil {
		t.Fatal(err)
	}
	if open := f.openClarifications(ledger.ClarificationFieldConflict); len(open) != 0 {
		t.Errorf("conflict still open after Rudolfo decided: %+v", open)
	}
}

func TestVoidingDismissesTheOpenClarifications(t *testing.T) {
	f := newFixture(t)
	created := f.pix("message-1", nil)

	if _, err := f.ledger.Void(t.Context(), f.account, created.ID, "engano", rudolfo); err != nil {
		t.Fatal(err)
	}

	if open := f.openClarifications(); len(open) != 0 {
		t.Errorf("open clarifications after void = %+v", open)
	}
}

func TestClarificationsCanBeFilteredByPeriod(t *testing.T) {
	f := newFixture(t)
	f.transaction("message-1")
	observation := f.observation("message-2", "Gastei R$ 20")
	in := groceries(brl(2000))
	in.Occurred = calendar.OnDay(calendar.Date{Year: 2026, Month: time.September, Day: 3}, calendar.SourceStated)
	if _, err := f.ledger.CreateTransaction(t.Context(), f.account, observation.ID, in, agent); err != nil {
		t.Fatal(err)
	}
	from, to := calendar.Date{Year: 2026, Month: time.October, Day: 1}, calendar.Date{Year: 2026, Month: time.October, Day: 31}

	october, err := f.ledger.Clarifications(t.Context(), f.account, ledger.ClarificationFilter{From: &from, To: &to})

	if err != nil || len(october) != 1 || october[0].Transaction.Occurred.On.Month != time.October {
		t.Errorf("October clarifications = %+v, %v", october, err)
	}
	if all := f.openClarifications(); len(all) != 2 {
		t.Errorf("all clarifications = %d, want 2", len(all))
	}
	_, err = f.ledger.Clarifications(t.Context(), f.account, ledger.ClarificationFilter{From: &to, To: &from})
	assertValidation(t, err, "to")
}
