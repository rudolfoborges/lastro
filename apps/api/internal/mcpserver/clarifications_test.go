package mcpserver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

func (e *env) expense(p accounts.Principal, key string, method ledger.PaymentKind) ledger.Transaction {
	e.t.Helper()
	observation, err := e.ledger.RecordObservation(e.t.Context(), p.AccountID, ledger.NewObservation{
		Source: ledger.SourceConversation, SourceKey: key, Payload: map[string]any{"raw_text": "Pix de R$ 50 para a Ana"}, ReceivedAt: clock,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	day := calendar.Date{Year: 2026, Month: time.October, Day: 21}
	created, err := e.ledger.CreateTransaction(e.t.Context(), p.AccountID, observation.ID, ledger.NewTransaction{
		OriginalDescription: "Pix de R$ 50 para a Ana",
		Description:         "Pix para Ana",
		Amount:              money.Money{Minor: 5000, Currency: "BRL"},
		Occurred:            calendar.OnDay(day, calendar.SourceInferred),
		PaymentMethod:       &ledger.PaymentMethodRef{Kind: method},
		Nature:              ledger.NatureExpense,
		Status:              ledger.InitialStatus(&ledger.PaymentMethodRef{Kind: method}),
	}, ledger.Actor{Author: ledger.AuthorAgent})
	if err != nil {
		e.t.Fatal(err)
	}
	return created
}

func TestListClarificationsAnswersWhichExpensesHaveNoCategory(t *testing.T) {
	e := newEnv(t)
	p := e.principal("Rudolfo", "expenses:read")
	created := e.expense(p, "message-1", ledger.PaymentCreditCard)
	session := e.session(p)

	got := callTool[mcpserver.ListClarificationsOutput](t, session, "list_clarifications", map[string]any{"kind": "category_unknown"}, nil)

	if got.isError || got.out.Count != 1 || got.out.Summary != "1 dúvida aberta." {
		t.Fatalf("list_clarifications = %+v", got)
	}
	clarification := got.out.Clarifications[0]
	if clarification.Transaction == nil || clarification.Transaction.ID != created.ID.String() || clarification.Transaction.Amount.Formatted != "R$ 50,00" || *clarification.Transaction.Date.On != "2026-10-21" {
		t.Errorf("clarification = %+v", clarification)
	}
	if !strings.Contains(clarification.Question, "Qual a categoria") {
		t.Errorf("question = %q", clarification.Question)
	}
}

func TestResolveClarificationAppliesRudolfosAnswerOnce(t *testing.T) {
	e := newEnv(t)
	p := e.principal("Rudolfo", "expenses:read", "expenses:write")
	e.expense(p, "message-1", ledger.PaymentPix)
	session := e.session(p)
	open := callTool[mcpserver.ListClarificationsOutput](t, session, "list_clarifications", map[string]any{"kind": "purpose_unknown"}, nil)
	if open.out.Count != 1 {
		t.Fatalf("purpose clarifications = %+v", open)
	}
	args := map[string]any{
		"clarification_id": open.out.Clarifications[0].ID,
		"request_id":       "answer-1",
		"action":           "resolve",
		"note":             "Foi o presente da Ana",
		"purpose":          "presente",
	}

	first := callTool[mcpserver.ResolveClarificationOutput](t, session, "resolve_clarification", args, nil)
	retry := callTool[mcpserver.ResolveClarificationOutput](t, session, "resolve_clarification", args, nil)

	if first.isError || first.out.Outcome != "completed" || first.out.Result != "resolved" || *first.out.Transaction.Purpose != "presente" {
		t.Fatalf("resolve = %+v", first)
	}
	if retry.isError || retry.out.Clarification.ID != first.out.Clarification.ID {
		t.Errorf("retry with the same request_id = %+v, want the first answer replayed", retry)
	}
	again := callTool[mcpserver.ResolveClarificationOutput](t, session, "resolve_clarification", map[string]any{
		"clarification_id": open.out.Clarifications[0].ID, "request_id": "answer-2", "action": "dismiss", "note": "outra",
	}, nil)
	if !again.isError || again.toolErr.Code != "clarification_closed" {
		t.Errorf("answering a closed clarification = %+v", again)
	}
}

func TestResolveClarificationValidatesItsArguments(t *testing.T) {
	e := newEnv(t)
	p := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(p)

	badAction := callTool[mcpserver.ResolveClarificationOutput](t, session, "resolve_clarification", map[string]any{
		"clarification_id": "x", "request_id": "r", "action": "ignore", "note": "n",
	}, nil)
	badID := callTool[mcpserver.ResolveClarificationOutput](t, session, "resolve_clarification", map[string]any{
		"clarification_id": "not-an-id", "request_id": "r", "action": "dismiss", "note": "n",
	}, nil)

	if !badAction.isError {
		t.Error("an unknown action was accepted")
	}
	if !badID.isError || badID.toolErr.Field != "clarification_id" {
		t.Errorf("bad id = %+v", badID)
	}
}
