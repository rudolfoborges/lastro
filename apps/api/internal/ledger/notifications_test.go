package ledger_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

const walletPackage = "com.google.android.apps.walletnfcrel"

var postedAt = time.Date(2026, 10, 22, 15, 4, 5, 0, time.UTC)

func walletPurchase(key, text string) ledger.WalletNotification {
	return ledger.WalletNotification{
		DeviceID:        "galaxy-s24",
		Package:         walletPackage,
		NotificationKey: key,
		Event:           ledger.NotificationPosted,
		PostedAt:        postedAt,
		Title:           "Padaria Pão Quente",
		Text:            text,
		ReceivedAt:      postedAt.Add(20 * time.Second),
	}
}

func (f *fixture) ingest(in ledger.WalletNotification) ledger.IngestReceipt {
	f.t.Helper()
	receipt, err := f.ledger.IngestNotification(f.t.Context(), f.account, in)
	if err != nil {
		f.t.Fatalf("IngestNotification() error = %v", err)
	}
	return receipt
}

func (f *fixture) queryRow(sql string, args []any, dest ...any) {
	f.t.Helper()
	conn, err := pgx.Connect(f.t.Context(), f.databaseURL)
	if err != nil {
		f.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if err := conn.QueryRow(f.t.Context(), sql, args...).Scan(dest...); err != nil {
		f.t.Fatal(err)
	}
}

func assertReceipt(t *testing.T, receipt ledger.IngestReceipt, result ledger.IngestResult, version int) {
	t.Helper()
	if receipt.Result != result || receipt.Version != version {
		t.Errorf("receipt = %s v%d, want %s v%d", receipt.Result, receipt.Version, result, version)
	}
}

func TestRepeatedNotificationsAreStoredOnce(t *testing.T) {
	f := newFixture(t)
	notification := walletPurchase("0|wallet|7|null|10123", "R$ 32,97 com Visa •••• 1234")

	first := f.ingest(notification)
	notification.ReceivedAt = notification.ReceivedAt.Add(time.Minute)
	again := f.ingest(notification)
	notification.Event = ledger.NotificationUpdated
	reposted := f.ingest(notification)

	assertReceipt(t, first, ledger.IngestStored, 1)
	assertReceipt(t, again, ledger.IngestDuplicate, 1)
	assertReceipt(t, reposted, ledger.IngestDuplicate, 1)
	if *again.NotificationID != *first.NotificationID || *again.ObservationID != *first.ObservationID {
		t.Errorf("duplicate points at %v/%v, want %v/%v", *again.NotificationID, *again.ObservationID, *first.NotificationID, *first.ObservationID)
	}
	if got := f.count("observations"); got != 1 {
		t.Errorf("observations = %d, want 1", got)
	}
}

func TestAnUpdatedNotificationIsANewVersionOfTheSameNotification(t *testing.T) {
	f := newFixture(t)
	key := "0|wallet|7|null|10123"
	first := f.ingest(walletPurchase(key, "Pagamento pendente de R$ 32,97"))
	update := walletPurchase(key, "R$ 32,97 com Visa •••• 1234")
	update.Event = ledger.NotificationUpdated
	update.PostedAt = postedAt.Add(time.Minute)

	second := f.ingest(update)

	assertReceipt(t, second, ledger.IngestStored, 2)
	if *second.NotificationID != *first.NotificationID || *second.ObservationID == *first.ObservationID {
		t.Errorf("update = %+v, want a new observation of notification %v", second, *first.NotificationID)
	}
	if notifications, observations := f.count("wallet_notifications"), f.count("observations"); notifications != 1 || observations != 2 {
		t.Errorf("notifications = %d, observations = %d, want 1 and 2", notifications, observations)
	}
}

func TestAnIdenticalPurchasePostedLaterUnderTheSameKeyIsKept(t *testing.T) {
	f := newFixture(t)
	notification := walletPurchase("0|wallet|7|null|10123", "R$ 32,97 com Visa •••• 1234")
	f.ingest(notification)
	notification.PostedAt = notification.PostedAt.Add(10 * time.Minute)
	notification.ReceivedAt = notification.ReceivedAt.Add(10 * time.Minute)

	assertReceipt(t, f.ingest(notification), ledger.IngestStored, 2)
}

func TestNotificationTextIsRedactedBeforeItIsStored(t *testing.T) {
	f := newFixture(t)
	notification := walletPurchase("0|wallet|8|null|10123", "Cartão 4111 1111 1111 1111 aprovado")
	notification.BigText = "Use o código 482913 para confirmar"

	receipt := f.ingest(notification)

	var payload, redactions []byte
	f.queryRow("SELECT payload, redactions FROM observations WHERE id = $1", []any{*receipt.ObservationID}, &payload, &redactions)
	for _, secret := range []string{"4111 1111 1111 1111", "482913"} {
		if strings.Contains(string(payload), secret) {
			t.Errorf("payload %s keeps %q", payload, secret)
		}
	}
	var findings []map[string]any
	if err := json.Unmarshal(redactions, &findings); err != nil || len(findings) != 2 {
		t.Errorf("redactions = %s, want card_number and secret", redactions)
	}
	var stored map[string]any
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatal(err)
	}
	if stored["device_id"] != "galaxy-s24" || stored["notification_key"] != notification.NotificationKey || stored["posted_at"] != "2026-10-22T15:04:05Z" {
		t.Errorf("payload = %s", payload)
	}
}

func TestNotificationsFromOtherAppsAreDiscardedAndOnlyCounted(t *testing.T) {
	f := newFixture(t)
	notification := walletPurchase("0|com.whatsapp|1|null|10123", "Oi, tudo bem?")
	notification.Package = "com.whatsapp"

	assertReceipt(t, f.ingest(notification), ledger.IngestDiscarded, 0)

	var discarded int
	f.queryRow("SELECT discarded_count FROM wallet_devices WHERE device_id = $1", []any{"galaxy-s24"}, &discarded)
	if discarded != 1 || f.count("observations") != 0 || f.count("wallet_notifications") != 0 {
		t.Errorf("discarded = %d, observations = %d, notifications = %d, want 1, 0, 0",
			discarded, f.count("observations"), f.count("wallet_notifications"))
	}
}

func TestRemovingANotificationDoesNotChangeItsTransaction(t *testing.T) {
	f := newFixture(t)
	notification := walletPurchase("0|wallet|9|null|10123", "R$ 327,39 com Visa •••• 1234")
	posted := f.ingest(notification)
	created, err := f.ledger.CreateTransaction(t.Context(), f.account, *posted.ObservationID, groceries(brl(32739)), agent)
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.ledger.History(t.Context(), f.account, created.ID)
	if err != nil {
		t.Fatal(err)
	}

	notification.Event = ledger.NotificationRemoved
	removed := f.ingest(notification)

	assertReceipt(t, removed, ledger.IngestRemoved, 0)
	detail, err := f.ledger.GetTransaction(t.Context(), f.account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.ledger.History(t.Context(), f.account, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Status != created.Status || detail.Amount != created.Amount || len(after) != len(before) {
		t.Errorf("transaction after removal = %+v with %d revisions, want it unchanged with %d", detail.Transaction, len(after), len(before))
	}
	var removedAt *time.Time
	f.queryRow("SELECT removed_at FROM wallet_notifications WHERE id = $1", []any{*posted.NotificationID}, &removedAt)
	if removedAt == nil {
		t.Error("removed_at was not recorded")
	}
}

func TestRemovingAnUnknownNotificationChangesNothing(t *testing.T) {
	f := newFixture(t)
	notification := walletPurchase("0|wallet|404|null|10123", "")
	notification.Event = ledger.NotificationRemoved
	notification.PostedAt = time.Time{}

	assertReceipt(t, f.ingest(notification), ledger.IngestUnknownNotification, 0)
	if got := f.count("wallet_notifications"); got != 0 {
		t.Errorf("notifications = %d, want 0", got)
	}
}

func TestTheSameNotificationKeyIsIndependentPerAccount(t *testing.T) {
	f := newFixture(t)
	notification := walletPurchase("0|wallet|7|null|10123", "R$ 32,97 com Visa •••• 1234")
	other := f.newAccount("Outra pessoa")

	mine := f.ingest(notification)
	theirs, err := f.ledger.IngestNotification(t.Context(), other, notification)
	if err != nil {
		t.Fatal(err)
	}

	assertReceipt(t, mine, ledger.IngestStored, 1)
	assertReceipt(t, theirs, ledger.IngestStored, 1)
	if *mine.NotificationID == *theirs.NotificationID {
		t.Error("both accounts share the same notification")
	}
}

func TestInvalidNotificationsAreRejected(t *testing.T) {
	f := newFixture(t)
	for field, change := range map[string]func(*ledger.WalletNotification){
		"device_id":        func(n *ledger.WalletNotification) { n.DeviceID = "Galaxy S24" },
		"package":          func(n *ledger.WalletNotification) { n.Package = "wallet" },
		"notification_key": func(n *ledger.WalletNotification) { n.NotificationKey = "" },
		"event":            func(n *ledger.WalletNotification) { n.Event = "dismissed" },
		"posted_at":        func(n *ledger.WalletNotification) { n.PostedAt = time.Time{} },
		"title":            func(n *ledger.WalletNotification) { n.Title = strings.Repeat("a", 5001) },
		"text":             func(n *ledger.WalletNotification) { n.Title, n.Text = " ", "" },
	} {
		t.Run(field, func(t *testing.T) {
			notification := walletPurchase("0|wallet|1|null|10123", "R$ 10,00")
			change(&notification)
			_, err := f.ledger.IngestNotification(t.Context(), f.account, notification)
			assertValidation(t, err, field)
		})
	}

	future := walletPurchase("0|wallet|1|null|10123", "R$ 10,00")
	future.PostedAt = future.ReceivedAt.Add(time.Hour)
	_, err := f.ledger.IngestNotification(t.Context(), f.account, future)
	assertValidation(t, err, "posted_at")
	if got := f.count("wallet_devices"); got != 0 {
		t.Errorf("devices = %d, want 0 after rejected requests", got)
	}
}
