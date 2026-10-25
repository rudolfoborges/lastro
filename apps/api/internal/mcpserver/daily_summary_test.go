package mcpserver_test

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

func turnOnDailySummary(t *testing.T, session *mcp.ClientSession) mcpserver.SetDailySummaryOutput {
	t.Helper()
	got := callTool[mcpserver.SetDailySummaryOutput](t, session, "set_daily_summary", map[string]any{"enabled": true, "time": "08:00"}, nil)
	if got.isError {
		t.Fatalf("set_daily_summary = %s", got.text)
	}
	return got.out
}

func TestTheDailySummaryIsOffUntilRudolfoTurnsItOn(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))

	off := callTool[mcpserver.DailySummaryOutput](t, session, "get_daily_summary", map[string]any{}, nil)
	on := turnOnDailySummary(t, session)
	invalidTime := callTool[mcpserver.SetDailySummaryOutput](t, session, "set_daily_summary", map[string]any{"enabled": true, "time": "8h"}, nil)

	if off.isError || off.out.Settings.Enabled || off.out.Settings.Time != "08:00" || len(off.out.Totals) != 0 || !strings.Contains(off.out.Summary, "desativado") {
		t.Errorf("summary before turning it on = %+v (%s)", off.out, off.text)
	}
	if !on.Settings.Enabled || on.Settings.Timezone != "America/Sao_Paulo" || !strings.Contains(on.Summary, "às 08:00") {
		t.Errorf("turned on = %+v", on)
	}
	if !invalidTime.isError || invalidTime.toolErr.Field != "time" {
		t.Errorf("time 8h = %+v %s, want a validation error", invalidTime.out, invalidTime.text)
	}
}

func TestTheDailySummaryShowsYesterdayWithItsCoverage(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	turnOnDailySummary(t, session)
	for _, expense := range []map[string]any{
		{"request_id": "m-1", "raw_text": "327,39 no mercado ontem, no débito", "amount": "327.39", "description": "Mercado", "date_reference": "yesterday", "payment_method": "debit_card", "category": "groceries"},
		{"request_id": "m-2", "raw_text": "200 de gasolina ontem no crédito", "amount": "200", "description": "Gasolina", "date_reference": "yesterday", "payment_method": "credit_card", "category": "fuel"},
	} {
		callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", expense, chatgpt)
	}

	yesterday := callTool[mcpserver.DailySummaryOutput](t, session, "get_daily_summary", map[string]any{}, nil)
	spending := callTool[mcpserver.QuerySpendingOutput](t, session, "query_spending", map[string]any{"from": "2026-10-20", "to": "2026-10-20"}, nil)
	empty := callTool[mcpserver.DailySummaryOutput](t, session, "get_daily_summary", map[string]any{"day": "2026-10-19"}, nil)
	today := callTool[mcpserver.DailySummaryOutput](t, session, "get_daily_summary", map[string]any{"day": "2026-10-21"}, nil)

	if yesterday.isError || yesterday.out.Day != "2026-10-20" || yesterday.out.Delivered || len(yesterday.out.Categories) != 2 {
		t.Fatalf("yesterday = %+v (%s)", yesterday.out, yesterday.text)
	}
	if total := yesterday.out.Totals[0]; total.Confirmed.Amount != spending.out.Totals[0].Confirmed.Amount || total.Provisional.Amount != spending.out.Totals[0].Provisional.Amount {
		t.Errorf("daily totals = %+v, want the same as query_spending %+v", yesterday.out.Totals, spending.out.Totals)
	}
	for _, want := range []string{"Resumo de 20/10/2026", "R$ 327,39 confirmados (1)", "R$ 200,00 provisórios (1)", "Por categoria:", "Atenção:"} {
		if !strings.Contains(yesterday.out.Summary, want) {
			t.Errorf("summary = %q, want %q", yesterday.out.Summary, want)
		}
	}
	if len(yesterday.out.Charts) != 1 || seriesTotals(yesterday.out.Charts[0])["Confirmados"] != "327.39" || seriesTotals(yesterday.out.Charts[0])["Provisórios"] != "200.00" {
		t.Errorf("charts = %+v, want the day's totals", yesterday.out.Charts)
	}
	if yesterday.out.Coverage == nil || yesterday.out.Coverage.Complete {
		t.Errorf("coverage = %+v, want the gap of a day without tracking", yesterday.out.Coverage)
	}
	if !strings.Contains(empty.out.Summary, "não significa que não houve gastos") {
		t.Errorf("a day without records = %q, want the limitation instead of no spending", empty.out.Summary)
	}
	if !today.isError || today.toolErr.Field != "day" {
		t.Errorf("today = %+v %s, want the day refused until it is over", today.out, today.text)
	}
}

func TestADeliveredDayIsFlaggedAndNeverSentTwice(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))
	turnOnDailySummary(t, session)

	first := callTool[mcpserver.MarkDailySummaryDeliveredOutput](t, session, "mark_daily_summary_delivered", map[string]any{"day": "2026-10-20"}, nil)
	again := callTool[mcpserver.MarkDailySummaryDeliveredOutput](t, session, "mark_daily_summary_delivered", map[string]any{"day": "2026-10-20"}, nil)
	got := callTool[mcpserver.DailySummaryOutput](t, session, "get_daily_summary", map[string]any{}, nil)

	if first.isError || first.out.Result != "marked" || again.out.Result != "already_delivered" || again.out.DeliveredAt != first.out.DeliveredAt {
		t.Errorf("marks = %+v then %+v", first.out, again.out)
	}
	if !got.out.Delivered || got.out.DeliveredAt != first.out.DeliveredAt {
		t.Errorf("summary of a delivered day = %+v, want it flagged", got.out)
	}
}
