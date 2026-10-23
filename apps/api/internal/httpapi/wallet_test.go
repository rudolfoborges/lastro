package httpapi_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
)

func walletNotification(sentAt time.Time) map[string]any {
	return map[string]any{
		"device_id":        "galaxy-s24",
		"package":          "com.google.android.apps.walletnfcrel",
		"notification_key": "0|com.google.android.apps.walletnfcrel|7|null|10123",
		"event":            "posted",
		"posted_at":        sentAt.Add(-time.Minute).Format(time.RFC3339Nano),
		"title":            "Padaria Pão Quente",
		"text":             "R$ 32,97 com Visa •••• 1234",
		"big_text":         "",
		"sent_at":          sentAt.Format(time.RFC3339Nano),
	}
}

func (a *testAPI) track(accountID string, active bool) {
	a.t.Helper()
	if _, err := a.ledger.SetTracking(a.t.Context(), uuid.MustParse(accountID), active, nil, time.Now()); err != nil {
		a.t.Fatal(err)
	}
}

func TestWalletNotificationsAreStoredOnceAndAcknowledged(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	accountID, admin := api.signUp("Rudolfo")
	api.track(accountID, true)
	_, android := api.createKey(admin, "android", "wallet:ingest")
	notification := walletNotification(time.Now())

	first := api.do(http.MethodPost, "/v1/wallet/notifications", android, notification)
	notification["sent_at"] = time.Now().Add(time.Second).Format(time.RFC3339Nano)
	retry := api.do(http.MethodPost, "/v1/wallet/notifications", android, notification)

	assertStatus(t, first, http.StatusCreated, "")
	assertStatus(t, retry, http.StatusOK, "")
	if field(first.body, "result") != "stored" || field(retry.body, "result") != "duplicate" {
		t.Errorf("results = %s then %s, want stored then duplicate", first.raw, retry.raw)
	}
	if id := field(first.body, "notification_id"); id == "" || field(retry.body, "notification_id") != id {
		t.Errorf("notification ids = %s and %s, want the same one", first.raw, retry.raw)
	}
}

func TestWalletNotificationsNeedAnIngestKey(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, reader := api.createKey(admin, "case", "expenses:read")

	assertStatus(t, api.do(http.MethodPost, "/v1/wallet/notifications", "", walletNotification(time.Now())),
		http.StatusUnauthorized, "unauthorized")
	assertStatus(t, api.do(http.MethodPost, "/v1/wallet/notifications", "lst_invalid", walletNotification(time.Now())),
		http.StatusUnauthorized, "unauthorized")
	assertStatus(t, api.do(http.MethodPost, "/v1/wallet/notifications", reader, walletNotification(time.Now())),
		http.StatusForbidden, "forbidden")
}

func TestWalletNotificationsOutsideTheSentAtWindowAreRejected(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, android := api.createKey(admin, "android", "wallet:ingest")
	missing := walletNotification(time.Now())
	delete(missing, "sent_at")

	for name, body := range map[string]map[string]any{
		"old":     walletNotification(time.Now().Add(-10 * time.Minute)),
		"future":  walletNotification(time.Now().Add(10 * time.Minute)),
		"missing": missing,
	} {
		t.Run(name, func(t *testing.T) {
			assertStatus(t, api.do(http.MethodPost, "/v1/wallet/notifications", android, body), http.StatusBadRequest, "stale_request")
		})
	}
}

func TestInvalidWalletNotificationsExplainTheField(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, android := api.createKey(admin, "android", "wallet:ingest")
	notification := walletNotification(time.Now())
	notification["event"] = "dismissed"

	res := api.do(http.MethodPost, "/v1/wallet/notifications", android, notification)

	assertStatus(t, res, http.StatusBadRequest, "invalid_request")
	if message := field(res.body, "message"); !strings.HasPrefix(message, "event ") {
		t.Errorf("message = %q, want it to name the event field", message)
	}
	notification["hmac"] = "abc"
	assertStatus(t, api.do(http.MethodPost, "/v1/wallet/notifications", android, notification), http.StatusBadRequest, "invalid_request")
}

func TestRemovingAWalletNotificationIsAcknowledged(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	accountID, admin := api.signUp("Rudolfo")
	api.track(accountID, true)
	_, android := api.createKey(admin, "android", "wallet:ingest")
	posted := api.do(http.MethodPost, "/v1/wallet/notifications", android, walletNotification(time.Now()))
	removal := map[string]any{
		"device_id":        "galaxy-s24",
		"package":          "com.google.android.apps.walletnfcrel",
		"notification_key": "0|com.google.android.apps.walletnfcrel|7|null|10123",
		"event":            "removed",
		"sent_at":          time.Now().Format(time.RFC3339Nano),
	}

	res := api.do(http.MethodPost, "/v1/wallet/notifications", android, removal)

	assertStatus(t, res, http.StatusOK, "")
	if field(res.body, "result") != "removed" || field(res.body, "notification_id") != field(posted.body, "notification_id") {
		t.Errorf("removal = %s, want removed for %s", res.raw, posted.raw)
	}
}

func heartbeat(sentAt time.Time) map[string]any {
	return map[string]any{
		"device_id":            "galaxy-s24",
		"client_version":       "0.1.0",
		"notification_access":  true,
		"battery_unrestricted": true,
		"queue_size":           0,
		"sent_at":              sentAt.Format(time.RFC3339Nano),
	}
}

func TestHeartbeatsReportTheTrackingState(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	accountID, admin := api.signUp("Rudolfo")
	_, android := api.createKey(admin, "android", "wallet:ingest")

	before := api.do(http.MethodPost, "/v1/wallet/heartbeat", android, heartbeat(time.Now()))
	api.track(accountID, true)
	active := api.do(http.MethodPost, "/v1/wallet/heartbeat", android, heartbeat(time.Now()))
	api.track(accountID, false)
	paused := api.do(http.MethodPost, "/v1/wallet/heartbeat", android, heartbeat(time.Now()))

	for res, want := range map[*response]string{&before: "never_enabled", &active: "active", &paused: "paused"} {
		assertStatus(t, *res, http.StatusOK, "")
		if field(res.body, "tracking") != want || field(res.body, "server_time") == "" {
			t.Errorf("heartbeat = %s, want tracking %s", res.raw, want)
		}
	}
	missing := heartbeat(time.Now())
	delete(missing, "notification_access")
	assertStatus(t, api.do(http.MethodPost, "/v1/wallet/heartbeat", android, missing), http.StatusBadRequest, "invalid_request")
	assertStatus(t, api.do(http.MethodPost, "/v1/wallet/heartbeat", android, heartbeat(time.Now().Add(-time.Hour))), http.StatusBadRequest, "stale_request")
}

func TestWalletNotificationsAreDroppedWhileTrackingIsPaused(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, android := api.createKey(admin, "android", "wallet:ingest")

	res := api.do(http.MethodPost, "/v1/wallet/notifications", android, walletNotification(time.Now()))

	assertStatus(t, res, http.StatusOK, "")
	if field(res.body, "result") != "tracking_paused" || field(res.body, "notification_id") != "" {
		t.Errorf("notification while paused = %s", res.raw)
	}
}
