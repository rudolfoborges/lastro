package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

func brl(minor int64) money.Money {
	return money.Money{Minor: minor, Currency: "BRL"}
}

func (e *env) octoberStatement(principal accounts.Principal) {
	e.t.Helper()
	date := func(value string) *calendar.Date {
		d, err := calendar.ParseDate(value)
		if err != nil {
			e.t.Fatal(err)
		}
		return &d
	}
	tv := ledger.StatementItem{Kind: ledger.ItemInstallment, OriginalDescription: "LOJA DE TV 01/12", Amount: brl(10000), Date: date("2026-09-10"), Page: 1, Excerpt: "10/09 LOJA DE TV 01/12 100,00",
		Installment: &ledger.InstallmentPosition{Number: 1, Count: 12}}
	pix := ledger.StatementItem{Kind: ledger.ItemPixInstallment, OriginalDescription: "PIX PARCELADO JOAO 02/06", Amount: brl(50000), Date: date("2026-08-10"), Page: 1, Excerpt: "10/08 PIX PARCELADO JOAO 02/06 500,00",
		Installment: &ledger.InstallmentPosition{Number: 2, Count: 6}, Category: ptr("home"), Purpose: ptr("reforma"),
		Components: []ledger.ChargeComponent{{Kind: ledger.ComponentPrincipal, Amount: brl(45000), Included: true}, {Kind: ledger.ComponentInterest, Amount: brl(5000), Included: true}}}
	_, err := e.ledger.SubmitStatement(e.t.Context(), principal.AccountID, ledger.StatementExtraction{
		Issuer: "Banco Exemplo", CardLast4: "1234", ClosingDate: *date("2026-10-03"), DueDate: *date("2026-10-10"), PeriodStart: date("2026-09-04"), PeriodEnd: date("2026-10-03"),
		Total: brl(60000), Reading: ledger.ReadingComplete, PageCount: ptr(1), Items: []ledger.StatementItem{tv, pix},
	}, clock, ledger.Actor{Author: ledger.AuthorAgent})
	if err != nil {
		e.t.Fatal(err)
	}
}

func ptr[T any](v T) *T {
	return &v
}

func TestThePRDQuestionsAreAnsweredEndToEnd(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", supermarket("message-1"), chatgpt)
	pix := map[string]any{"request_id": "message-2", "raw_text": "Pix de 50 no almoço dia 20", "amount": "50.00", "description": "Almoço", "occurred_on": "2026-10-20", "payment_method": "pix", "category": "meals", "purpose": "almoço"}
	callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", pix, chatgpt)
	e.octoberStatement(principal)

	groceries := callTool[mcpserver.QuerySpendingOutput](t, session, "query_spending", map[string]any{"category": "groceries"}, nil)
	week := callTool[mcpserver.QuerySpendingOutput](t, session, "query_spending", map[string]any{"from": "2026-10-15", "to": "2026-10-21", "group_by": "day"}, nil)
	statement := callTool[mcpserver.StatementSummaryOutput](t, session, "get_statement_summary", map[string]any{}, nil)
	pixSummary := callTool[mcpserver.PixInstallmentSummaryOutput](t, session, "get_pix_installment_summary", map[string]any{}, nil)
	commitments := callTool[mcpserver.FutureCommitmentsOutput](t, session, "get_future_commitments", map[string]any{}, nil)
	uncategorized := callTool[mcpserver.ListClarificationsOutput](t, session, "list_clarifications", map[string]any{"kind": "category_unknown"}, nil)
	review := callTool[mcpserver.GetStatementReviewOutput](t, session, "get_statement_review", map[string]any{}, nil)

	if groceries.isError || groceries.out.Period.From != "2026-10-01" || len(groceries.out.Totals) != 1 || groceries.out.Totals[0].Provisional.Amount.Amount != "327.39" {
		t.Errorf("mercado até agora = %+v (%s)", groceries.out, groceries.text)
	}
	if !strings.Contains(groceries.out.Summary, "Compras de 01/10/2026 a 21/10/2026") || groceries.out.Premise == "" {
		t.Errorf("summary = %q, premise = %q", groceries.out.Summary, groceries.out.Premise)
	}
	if week.isError || len(week.out.Groups) != 2 || week.out.Totals[0].Confirmed.Amount.Amount != "50.00" {
		t.Errorf("gastos da semana = %+v (%s)", week.out, week.text)
	}
	if statement.isError || statement.out.Statement.Month != "2026-10" || len(statement.out.Modalities) != 2 || !strings.Contains(statement.out.Summary, "R$ 600,00 cobrados") {
		t.Errorf("fatura = %+v (%s)", statement.out, statement.text)
	}
	if pixSummary.isError || len(pixSummary.out.Totals) != 1 || pixSummary.out.Totals[0].Charged.Amount != "500.00" || !strings.Contains(pixSummary.out.Summary, "R$ 50,00 de juros") {
		t.Errorf("Pix parcelado = %+v (%s)", pixSummary.out, pixSummary.text)
	}
	if commitments.isError || len(commitments.out.Totals) != 1 || commitments.out.Totals[0].Total.Amount != "3100.00" {
		t.Errorf("parcelas futuras = %+v (%s)", commitments.out, commitments.text)
	}
	if uncategorized.isError || uncategorized.out.Count != 1 {
		t.Errorf("sem categoria = %+v (%s)", uncategorized.out, uncategorized.text)
	}
	if review.isError || review.out.Review.Status != "reconciled" {
		t.Errorf("o que falta conferir = %+v (%s)", review.out, review.text)
	}
}

func TestAcceptanceCriterion11AnEmptyPeriodWithoutCoverageIsNotNoSpending(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read"))

	got := callTool[mcpserver.QuerySpendingOutput](t, session, "query_spending", map[string]any{"from": "2026-09-01", "to": "2026-09-30"}, nil)

	if got.isError || got.out.Coverage.Complete || len(got.out.Coverage.Limitations) == 0 {
		t.Fatalf("query_spending = %+v (%s), want the coverage limitation", got.out, got.text)
	}
	if !strings.Contains(got.out.Summary, "não significa que não houve gastos") || strings.Contains(got.out.Summary, "Não há compras registradas") {
		t.Errorf("summary = %q, want the limitation instead of no spending", got.out.Summary)
	}
}
