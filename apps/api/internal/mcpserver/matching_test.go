package mcpserver_test

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

func TestCaseLinksAConversationReportToAPurchaseGoogleWalletRecorded(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	e.notify(principal, ledger.NotificationPosted, "Compra de R$ 32,97 com Visa •••• 1234", clock.Add(-time.Hour))
	observation := pending(t, session).Notifications[0].ObservationID
	notified := submit(t, session, map[string]any{
		"observation_id": observation, "event_type": "purchase", "amount": "32.97", "merchant": "Padaria Pão Quente",
		"payment_method": "credit_card", "card_last4": "1234",
	}).out.Results[0].Transaction
	told := supermarket("message-1")
	told["amount"], told["raw_text"], told["purpose"] = "32.97", "Paguei 32,97 na padaria, café da equipe", "café da equipe"

	offered := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", told, chatgpt)
	told["related_transaction_id"] = notified.ID
	linked := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", told, chatgpt)
	search := callTool[mcpserver.FindMatchCandidatesOutput](t, session, "find_match_candidates", map[string]any{"transaction_id": notified.ID}, nil)

	if offered.out.Outcome != "pending" || offered.out.Result != "possible_match" || len(offered.out.Candidates) != 1 {
		t.Fatalf("first call = %+v", offered.out)
	}
	candidate := offered.out.Candidates[0]
	if candidate.Transaction.ID != notified.ID || !slices.Equal(candidate.Sources, []string{"wallet_notification"}) || len(candidate.Reasons) == 0 {
		t.Errorf("candidate = %+v", candidate)
	}
	if linked.isError || linked.out.Result != "linked" || linked.out.Transaction.ID != notified.ID || !slices.Contains(linked.out.Changed, "purpose") {
		t.Fatalf("second call = %+v (%s)", linked.out, linked.text)
	}
	if !strings.Contains(linked.out.Summary, "nada novo foi criado") {
		t.Errorf("summary = %q", linked.out.Summary)
	}
	if search.isError || !slices.Equal(search.out.Sources, []string{"conversation", "wallet_notification"}) || len(search.out.Candidates) != 0 {
		t.Errorf("find_match_candidates = %+v (%s)", search.out, search.text)
	}
	if got := e.count("transactions"); got != 1 {
		t.Errorf("transactions = %d, want 1", got)
	}
}

func TestFindingMatchesNeedsATransactionOrAnObservation(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read"))

	got := callTool[mcpserver.FindMatchCandidatesOutput](t, session, "find_match_candidates", map[string]any{}, nil)

	if !got.isError || got.toolErr.Field != "transaction_id" {
		t.Errorf("find_match_candidates without a target = %+v", got)
	}
}

func TestTheMatchingProcedureIsAvailableToCase(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read"))

	got := callTool[mcpserver.GetInstructionsOutput](t, session, "get_instructions", map[string]any{"topic": "matching"}, nil)

	if got.isError || !strings.Contains(got.out.Content, "find_match_candidates") || !strings.Contains(got.out.Content, "one record of each source") {
		t.Errorf("get_instructions(matching) = %+v", got)
	}
}
