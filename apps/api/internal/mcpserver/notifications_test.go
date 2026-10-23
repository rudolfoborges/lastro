package mcpserver_test

import (
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

const notificationKey = "0|com.google.android.apps.walletnfcrel|7|null|10123"

var notificationPostedAt = time.Date(2026, 10, 21, 18, 4, 5, 0, time.UTC)

func (e *env) notify(principal accounts.Principal, event ledger.NotificationEvent, text string, postedAt time.Time) {
	e.t.Helper()
	if _, err := e.ledger.SetTracking(e.t.Context(), principal.AccountID, true, nil, postedAt.Add(-24*time.Hour)); err != nil {
		e.t.Fatal(err)
	}
	_, err := e.ledger.IngestNotification(e.t.Context(), principal.AccountID, ledger.WalletNotification{
		DeviceID:        "galaxy-s24",
		Package:         "com.google.android.apps.walletnfcrel",
		NotificationKey: notificationKey,
		Event:           event,
		PostedAt:        postedAt,
		Title:           "Padaria Pão Quente",
		Text:            text,
		ReceivedAt:      postedAt.Add(30 * time.Second),
	})
	if err != nil {
		e.t.Fatal(err)
	}
}

func submit(t *testing.T, session *mcp.ClientSession, items ...map[string]any) result[mcpserver.SubmitNotificationInterpretationsOutput] {
	t.Helper()
	return callTool[mcpserver.SubmitNotificationInterpretationsOutput](t, session, "submit_notification_interpretations", map[string]any{"interpretations": items}, nil)
}

func pending(t *testing.T, session *mcp.ClientSession) mcpserver.ListPendingNotificationsOutput {
	t.Helper()
	got := callTool[mcpserver.ListPendingNotificationsOutput](t, session, "list_pending_notifications", map[string]any{}, nil)
	if got.isError {
		t.Fatalf("list_pending_notifications = %s", got.text)
	}
	return got.out
}

func TestAcceptanceCriterion2NotificationsThroughCase(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	for range 3 {
		e.notify(principal, ledger.NotificationPosted, "Compra de R$ 32,97 com Visa •••• 1234", notificationPostedAt)
	}

	inbox := pending(t, session)
	if inbox.Pending != 1 || len(inbox.Notifications) != 1 || inbox.Notifications[0].PostedAt != "2026-10-21T15:04:05-03:00" {
		t.Fatalf("inbox = %+v, want one notification with posted_at in São Paulo", inbox)
	}
	purchase := map[string]any{
		"observation_id": inbox.Notifications[0].ObservationID,
		"event_type":     "purchase",
		"amount":         "32.97",
		"merchant":       "Padaria Pão Quente",
		"payment_method": "credit_card",
		"card_last4":     "1234",
		"category":       "meals",
	}
	first := submit(t, session, purchase)
	resent := submit(t, session, purchase)

	if first.isError || first.out.Outcome != "completed" || first.out.Results[0].Effect != "created" {
		t.Fatalf("first submission = %+v %s", first.out, first.text)
	}
	created := first.out.Results[0].Transaction
	if created.Status != "provisional" || created.Date.Precision != "exact" || created.Category.Code != "meals" {
		t.Errorf("created = %+v", created)
	}
	if resent.out.Results[0].Result != "already_applied" || resent.out.Results[0].Transaction.ID != created.ID {
		t.Errorf("resent batch = %+v", resent.out)
	}

	e.notify(principal, ledger.NotificationUpdated, "Compra de R$ 32,97 aprovada", notificationPostedAt.Add(time.Minute))
	update := pending(t, session).Notifications[0]
	if update.Version != 2 || len(update.PreviousVersions) != 1 || *update.PreviousVersions[0].Interpretation.TransactionID != created.ID {
		t.Fatalf("update = %+v, want version 2 pointing at %s", update, created.ID)
	}
	updated := submit(t, session, map[string]any{
		"observation_id":         update.ObservationID,
		"event_type":             "purchase_update",
		"related_transaction_id": created.ID,
	})
	if updated.out.Results[0].Effect != "updated" || updated.out.Results[0].Transaction.ID != created.ID {
		t.Errorf("update = %+v", updated.out)
	}
	if left := pending(t, session); left.Pending != 0 || left.Summary != "Nenhuma notificação pendente." {
		t.Errorf("inbox after processing = %+v", left)
	}
	listed := callTool[mcpserver.ListTransactionsOutput](t, session, "list_transactions", map[string]any{}, nil)
	if len(listed.out.Transactions) != 1 {
		t.Errorf("transactions = %d, want 1", len(listed.out.Transactions))
	}
}

func TestEachInterpretationIsAppliedOnItsOwn(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	e.notify(principal, ledger.NotificationPosted, "Seu cartão foi adicionado à carteira", notificationPostedAt)
	observation := pending(t, session).Notifications[0].ObservationID

	got := submit(t, session,
		map[string]any{"observation_id": observation, "event_type": "notice"},
		map[string]any{"observation_id": "not-an-id", "event_type": "purchase", "amount": "10.00", "merchant": "X"},
		map[string]any{"observation_id": observation, "event_type": "purchase", "amount": "10,00", "merchant": "X"},
	)

	if got.isError || got.out.Outcome != "pending" || len(got.out.Results) != 3 {
		t.Fatalf("batch = %+v %s", got.out, got.text)
	}
	if got.out.Results[0].Outcome != "completed" || got.out.Results[0].Effect != "none" || got.out.Results[0].Transaction != nil {
		t.Errorf("notice = %+v", got.out.Results[0])
	}
	for _, failed := range got.out.Results[1:] {
		if failed.Outcome != "failed" || failed.Error == nil || !strings.HasPrefix(failed.Summary, "Nada foi aplicado") {
			t.Errorf("invalid item = %+v", failed)
		}
	}
	changed := submit(t, session, map[string]any{"observation_id": observation, "event_type": "purchase", "amount": "10.00", "merchant": "X"})
	if changed.out.Results[0].Error == nil || changed.out.Results[0].Error.Code != "already_interpreted" {
		t.Errorf("a different reading without reinterpret = %+v", changed.out.Results[0])
	}
}

func TestPossibleMatchesComeBackPendingWithReasons(t *testing.T) {
	e := newEnv(t)
	principal := e.principal("Rudolfo", "expenses:read", "expenses:write")
	session := e.session(principal)
	told := supermarket("message-1")
	told["amount"], told["raw_text"] = "32.97", "Gastei R$ 32,97 na padaria agora"
	recorded := callTool[mcpserver.RecordExpenseOutput](t, session, "record_expense", told, chatgpt)
	e.notify(principal, ledger.NotificationPosted, "Compra de R$ 32,97", clock.Add(time.Minute))
	observation := pending(t, session).Notifications[0].ObservationID

	got := submit(t, session, map[string]any{"observation_id": observation, "event_type": "purchase", "amount": "32.97", "merchant": "Padaria Pão Quente"})

	item := got.out.Results[0]
	if got.out.Outcome != "pending" || item.Outcome != "pending" || item.Result != "possible_match" || len(item.Candidates) != 1 {
		t.Fatalf("submission = %+v", got.out)
	}
	if item.Candidates[0].Transaction.ID != recorded.out.Transaction.ID || len(item.Candidates[0].Reasons) == 0 {
		t.Errorf("candidate = %+v", item.Candidates[0])
	}
	if pending(t, session).Pending != 1 {
		t.Error("a possible match left the notification interpreted")
	}
}

func TestNotificationToolsAreDescribedForCase(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		byName[tool.Name] = tool
	}
	list, submitTool := byName["list_pending_notifications"], byName["submit_notification_interpretations"]
	if list == nil || submitTool == nil || !list.Annotations.ReadOnlyHint || submitTool.Annotations.ReadOnlyHint {
		t.Fatalf("notification tools = %+v / %+v", list, submitTool)
	}
	enum, _ := lookup(submitTool.InputSchema, "properties", "interpretations", "items", "properties", "event_type", "enum").([]any)
	if len(enum) != 8 {
		t.Errorf("event_type enum = %v, want the 8 event types", enum)
	}
	instructions := callTool[mcpserver.GetInstructionsOutput](t, session, "get_instructions", map[string]any{"topic": "notifications"}, nil)
	if instructions.isError || !strings.Contains(instructions.out.Content, "purchase_update") {
		t.Errorf("notifications procedure = %+v", instructions)
	}
}

func lookup(value any, path ...string) any {
	for _, key := range path {
		object, ok := value.(map[string]any)
		if !ok {
			return nil
		}
		value = object[key]
	}
	return value
}
