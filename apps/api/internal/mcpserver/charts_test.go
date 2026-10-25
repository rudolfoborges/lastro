package mcpserver_test

import (
	"slices"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

func sumOf(t *testing.T, amounts ...mcpserver.MoneyOutput) string {
	t.Helper()
	total := money.Money{Currency: "BRL"}
	for _, amount := range amounts {
		value, err := money.Parse(amount.Amount, amount.Currency)
		if err != nil {
			t.Fatal(err)
		}
		if total, err = total.Add(value); err != nil {
			t.Fatal(err)
		}
	}
	return total.Decimal()
}

func seriesTotals(chart mcpserver.ChartOutput) map[string]string {
	totals := map[string]string{}
	for _, series := range chart.Series {
		totals[series.Name] = series.Total.Amount
	}
	return totals
}

func TestChartsCarryTheSameTotalsAsTheirAnswer(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", supermarket("message-1"), chatgpt)
	pix := map[string]any{"request_id": "message-2", "raw_text": "Pix de 50 no almoço dia 20", "amount": "50.00", "description": "Almoço", "occurred_on": "2026-10-20", "payment_method": "pix", "category": "meals", "purpose": "almoço"}
	callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", pix, chatgpt)
	e.octoberStatement(principal)

	spending := callTool[mcpserver.QuerySpendingOutput](t, session, "query_spending", map[string]any{"group_by": "category"}, nil)
	statement := callTool[mcpserver.StatementSummaryOutput](t, session, "get_statement_summary", map[string]any{}, nil)
	commitments := callTool[mcpserver.FutureCommitmentsOutput](t, session, "get_future_commitments", map[string]any{}, nil)

	if len(spending.out.Charts) != 1 {
		t.Fatalf("spending charts = %+v (%s)", spending.out.Charts, spending.text)
	}
	chart, total := spending.out.Charts[0], spending.out.Totals[0]
	want := map[string]string{"Confirmados": total.Confirmed.Amount.Amount, "Provisórios": total.Provisional.Amount.Amount}
	if got := seriesTotals(chart); got["Confirmados"] != want["Confirmados"] || got["Provisórios"] != want["Provisórios"] || len(got) != 2 {
		t.Errorf("spending chart totals = %v, want %v", got, want)
	}
	if chart.Kind != "stacked_bar" || chart.Title != "Compras por categoria" || len(chart.Labels) != len(spending.out.Groups) || !slices.Contains(chart.Notes, spending.out.Premise) {
		t.Errorf("spending chart = %+v", chart)
	}
	for _, series := range chart.Series {
		values := make([]mcpserver.MoneyOutput, len(series.Points))
		for i, point := range series.Points {
			values[i] = point.Value
		}
		if sumOf(t, values...) != series.Total.Amount {
			t.Errorf("series %s points add up to %s, total says %s", series.Name, sumOf(t, values...), series.Total.Amount)
		}
	}

	categories := make([]mcpserver.MoneyOutput, len(statement.out.Categories))
	for i, category := range statement.out.Categories {
		categories[i] = category.Amount
	}
	if statement.out.Chart == nil || len(statement.out.Chart.Series) != 1 || statement.out.Chart.Series[0].Total.Amount != sumOf(t, categories...) || statement.out.Chart.Series[0].Total.Amount != "600.00" {
		t.Errorf("statement chart = %+v, want the categories' R$ 600,00", statement.out.Chart)
	}

	if len(commitments.out.Charts) != 1 {
		t.Fatalf("commitment charts = %+v", commitments.out.Charts)
	}
	known := seriesTotals(commitments.out.Charts[0])
	future := commitments.out.Totals[0]
	if known["Conhecidas"] != future.Known.Amount || (future.Estimated.Amount != "0.00" && known["Estimadas"] != future.Estimated.Amount) {
		t.Errorf("commitment chart totals = %v, want known %s and estimated %s", known, future.Known.Amount, future.Estimated.Amount)
	}
}
