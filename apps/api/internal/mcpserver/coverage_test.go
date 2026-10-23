package mcpserver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

func (e *env) beat(principal accounts.Principal, from, to time.Time) {
	e.t.Helper()
	for at := from; !at.After(to); at = at.Add(15 * time.Minute) {
		_, err := e.ledger.RecordHeartbeat(e.t.Context(), principal.AccountID, ledger.Heartbeat{
			DeviceID: "galaxy-s24", SentAt: at, ClientVersion: "0.1.0", NotificationAccess: true, ReceivedAt: at,
		})
		if err != nil {
			e.t.Fatal(err)
		}
	}
}

func TestAcceptanceCriterion11CoverageThroughCase(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	start := clock.Add(-10 * time.Hour)
	if _, err := e.ledger.SetTracking(t.Context(), principal.AccountID, true, nil, start); err != nil {
		t.Fatal(err)
	}
	e.beat(principal, start, start.Add(2*time.Hour))
	e.beat(principal, start.Add(5*time.Hour), clock)

	got := callTool[mcpserver.GetCoverageStatusOutput](t, session, "get_coverage_status", map[string]any{"from": "2026-10-21", "to": "2026-10-21"}, nil)

	if got.isError || got.out.Complete || len(got.out.Gaps) != 1 || got.out.Gaps[0].Minutes != 180 {
		t.Fatalf("coverage = %+v %s, want one 3-hour gap", got.out, got.text)
	}
	if !strings.HasPrefix(got.out.Summary, "Cobertura incompleta") || !strings.Contains(got.out.Summary, "Sem sinal do celular de 21/10 15:50 a 21/10 18:50") {
		t.Errorf("summary = %q", got.out.Summary)
	}
	if got.out.Tracking.State != "active" || got.out.Devices[0].DeviceID != "galaxy-s24" {
		t.Errorf("tracking/devices = %+v / %+v", got.out.Tracking, got.out.Devices)
	}
}

func TestPausingTrackingKeepsRecordsQueryable(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", supermarket("message-1"), chatgpt)

	activated := callTool[mcpserver.SetTrackingOutput](t, session, "set_tracking", map[string]any{"state": "active", "inbox_interval_minutes": 60}, nil)
	paused := callTool[mcpserver.SetTrackingOutput](t, session, "set_tracking", map[string]any{"state": "paused"}, nil)
	listed := callTool[mcpserver.ListTransactionsOutput](t, session, "list_transactions", map[string]any{}, nil)
	coverage := callTool[mcpserver.GetCoverageStatusOutput](t, session, "get_coverage_status", map[string]any{}, nil)

	if activated.out.Tracking.State != "active" || activated.out.Tracking.InboxIntervalMinutes != 60 || !strings.Contains(activated.out.Summary, "120 min") {
		t.Errorf("activated = %+v", activated.out)
	}
	if paused.out.Tracking.State != "paused" || !strings.Contains(paused.out.Summary, "continua disponível") {
		t.Errorf("paused = %+v", paused.out)
	}
	if len(listed.out.Transactions) != 1 {
		t.Errorf("transactions while paused = %d, want 1", len(listed.out.Transactions))
	}
	if coverage.out.Tracking.State != "paused" || coverage.out.Complete || len(coverage.out.Inactive) == 0 {
		t.Errorf("coverage while paused = %+v", coverage.out)
	}
	invalid := callTool[mcpserver.SetTrackingOutput](t, session, "set_tracking", map[string]any{"state": "active", "inbox_interval_minutes": 5}, nil)
	if !invalid.isError || invalid.toolErr.Field != "inbox_interval_minutes" {
		t.Errorf("interval of 5 minutes = %+v", invalid)
	}
}
