package httpapi_test

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/httpapi"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

type testAPI struct {
	t        *testing.T
	handler  http.Handler
	accounts *accounts.Service
	logs     *syncBuffer
}

func newTestAPI(t *testing.T, limits config.RateLimits) *testAPI {
	t.Helper()
	db, err := store.Open(t.Context(), storetest.MigratedDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	svc := accounts.NewService(db)
	logs := &syncBuffer{}
	handler := httpapi.NewHandler(httpapi.Deps{
		Logger:     slog.New(slog.NewJSONHandler(logs, nil)),
		Database:   db,
		Accounts:   svc,
		RateLimits: limits,
	})
	return &testAPI{t: t, handler: handler, accounts: svc, logs: logs}
}

type response struct {
	status int
	header http.Header
	body   map[string]any
	raw    string
}

func (a *testAPI) do(method, path, token string, body any) response {
	a.t.Helper()
	var payload bytes.Buffer
	if body != nil {
		if raw, ok := body.(string); ok {
			payload.WriteString(raw)
		} else if err := json.NewEncoder(&payload).Encode(body); err != nil {
			a.t.Fatal(err)
		}
	}
	req := httptest.NewRequestWithContext(a.t.Context(), method, path, &payload)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, req)

	res := response{status: rec.Code, header: rec.Header(), raw: rec.Body.String()}
	if err := json.Unmarshal(rec.Body.Bytes(), &res.body); err != nil {
		a.t.Fatalf("%s %s returned non-JSON body %q", method, path, res.raw)
	}
	return res
}

func (a *testAPI) signUp(name string) (accountID, adminToken string) {
	a.t.Helper()
	invite, err := a.accounts.CreateInvite(a.t.Context(), time.Hour, "")
	if err != nil {
		a.t.Fatal(err)
	}
	res := a.do(http.MethodPost, "/v1/accounts", "", map[string]string{"invite_code": invite.Code, "name": name})
	if res.status != http.StatusCreated {
		a.t.Fatalf("sign-up status = %d, body = %s", res.status, res.raw)
	}
	return field(res.body, "account", "id"), field(res.body, "api_key", "token")
}

func (a *testAPI) createKey(adminToken, name string, scopes ...string) (id, token string) {
	a.t.Helper()
	res := a.do(http.MethodPost, "/v1/api-keys", adminToken, map[string]any{"name": name, "scopes": scopes})
	if res.status != http.StatusCreated {
		a.t.Fatalf("create key status = %d, body = %s", res.status, res.raw)
	}
	return field(res.body, "api_key", "id"), field(res.body, "api_key", "token")
}

func field(body map[string]any, path ...string) string {
	var current any = body
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return ""
		}
		current = object[key]
	}
	value, _ := current.(string)
	return value
}

func assertStatus(t *testing.T, res response, status int, code string) {
	t.Helper()
	if res.status != status || (code != "" && field(res.body, "error") != code) {
		t.Errorf("response = %d %s, want %d %s", res.status, res.raw, status, code)
	}
}

func TestSignUpThenWhoami(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})

	accountID, token := api.signUp("Rudolfo")
	if !strings.HasPrefix(token, "lst_") {
		t.Fatalf("token = %q, want an lst_ token", token)
	}
	res := api.do(http.MethodGet, "/v1/whoami", token, nil)

	assertStatus(t, res, http.StatusOK, "")
	if field(res.body, "account", "id") != accountID || field(res.body, "account", "name") != "Rudolfo" {
		t.Errorf("whoami account = %s", res.raw)
	}
	if field(res.body, "api_key", "name") != "admin" || strings.Contains(res.raw, "token") {
		t.Errorf("whoami key = %s, want the admin key without its token", res.raw)
	}
}

func TestSignUpRejectsInvalidInvitesAndBodies(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})

	assertStatus(t, api.do(http.MethodPost, "/v1/accounts", "", map[string]string{"invite_code": "inv_nope", "name": "X"}),
		http.StatusBadRequest, "invalid_invite")
	assertStatus(t, api.do(http.MethodPost, "/v1/accounts", "", `{"invite_code": "inv_x", "name": "X", "admin": true}`),
		http.StatusBadRequest, "invalid_request")
	assertStatus(t, api.do(http.MethodPost, "/v1/accounts", "", `{"invite_code": "inv_x"} {}`),
		http.StatusBadRequest, "invalid_request")
	assertStatus(t, api.do(http.MethodPost, "/v1/accounts", "", map[string]string{"invite_code": "inv_x", "name": ""}),
		http.StatusBadRequest, "invalid_request")
}

func TestRequestsWithoutAValidTokenAreUnauthorizedWithoutDetails(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, token := api.signUp("Rudolfo")

	for name, candidate := range map[string]string{
		"missing":   "",
		"malformed": "not-a-token",
		"tampered":  token[:len(token)-3] + "abc",
	} {
		t.Run(name, func(t *testing.T) {
			res := api.do(http.MethodGet, "/v1/whoami", candidate, nil)
			assertStatus(t, res, http.StatusUnauthorized, "unauthorized")
			if res.header.Get("WWW-Authenticate") == "" || field(res.body, "message") != "" {
				t.Errorf("401 response = %v %s", res.header, res.raw)
			}
		})
	}
}

func TestKeysWithoutTheRequiredScopeAreForbidden(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, reader := api.createKey(admin, "case", "expenses:read")

	assertStatus(t, api.do(http.MethodGet, "/v1/api-keys", reader, nil), http.StatusForbidden, "forbidden")
	assertStatus(t, api.do(http.MethodGet, "/v1/whoami", reader, nil), http.StatusOK, "")
}

func TestRevokedKeysAreRejectedOnTheNextRequest(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	androidID, android := api.createKey(admin, "android", "wallet:ingest")
	assertStatus(t, api.do(http.MethodGet, "/v1/whoami", android, nil), http.StatusOK, "")

	res := api.do(http.MethodPost, "/v1/api-keys/"+androidID+"/revoke", admin, nil)

	assertStatus(t, res, http.StatusOK, "")
	if field(res.body, "api_key", "revoked_at") == "" {
		t.Errorf("revoke response = %s, want revoked_at", res.raw)
	}
	assertStatus(t, api.do(http.MethodGet, "/v1/whoami", android, nil), http.StatusUnauthorized, "unauthorized")
}

func TestListingKeysNeverExposesTokens(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, caseToken := api.createKey(admin, "case", "expenses:read", "expenses:write")

	res := api.do(http.MethodGet, "/v1/api-keys", admin, nil)

	assertStatus(t, res, http.StatusOK, "")
	keys, _ := res.body["api_keys"].([]any)
	if len(keys) != 2 {
		t.Errorf("api_keys = %s, want two keys", res.raw)
	}
	if strings.Contains(res.raw, "lst_") || strings.Contains(res.raw, caseToken) || strings.Contains(res.raw, "token") {
		t.Errorf("listing leaks token data: %s", res.raw)
	}
}

func TestRevokeProtectsTheLastAdminAndOtherAccounts(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, alice := api.signUp("Alice")
	_, bob := api.signUp("Bob")
	bobKeyID, _ := api.createKey(bob, "case", "expenses:read")
	aliceAdminID := field(api.do(http.MethodGet, "/v1/whoami", alice, nil).body, "api_key", "id")

	assertStatus(t, api.do(http.MethodPost, "/v1/api-keys/"+aliceAdminID+"/revoke", alice, nil), http.StatusConflict, "last_admin_key")
	assertStatus(t, api.do(http.MethodPost, "/v1/api-keys/"+bobKeyID+"/revoke", alice, nil), http.StatusNotFound, "not_found")
	assertStatus(t, api.do(http.MethodPost, "/v1/api-keys/not-a-uuid/revoke", alice, nil), http.StatusNotFound, "not_found")
}

func TestCreateKeyValidatesScopes(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")

	res := api.do(http.MethodPost, "/v1/api-keys", admin, map[string]any{"name": "case", "scopes": []string{"everything"}})

	assertStatus(t, res, http.StatusBadRequest, "invalid_request")
	if !strings.Contains(field(res.body, "message"), "everything") {
		t.Errorf("message = %q, want the unknown scope", field(res.body, "message"))
	}
}

func TestEachKeyIsRateLimited(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{PerKeyPerMinute: 2})
	_, admin := api.signUp("Rudolfo")

	assertStatus(t, api.do(http.MethodGet, "/v1/whoami", admin, nil), http.StatusOK, "")
	assertStatus(t, api.do(http.MethodGet, "/v1/whoami", admin, nil), http.StatusOK, "")
	res := api.do(http.MethodGet, "/v1/whoami", admin, nil)

	assertStatus(t, res, http.StatusTooManyRequests, "rate_limited")
	if res.header.Get("Retry-After") == "" {
		t.Error("429 response has no Retry-After header")
	}
}

func TestRepeatedAuthenticationFailuresAreRateLimitedPerClient(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{UnauthenticatedPerMinute: 3})
	_, admin := api.signUp("Rudolfo")

	assertStatus(t, api.do(http.MethodGet, "/v1/whoami", "lst_wrong", nil), http.StatusUnauthorized, "unauthorized")
	assertStatus(t, api.do(http.MethodGet, "/v1/whoami", "lst_wrong", nil), http.StatusUnauthorized, "unauthorized")

	assertStatus(t, api.do(http.MethodGet, "/v1/whoami", admin, nil), http.StatusTooManyRequests, "rate_limited")
}

func TestTokensNeverReachTheLogs(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, caseToken := api.createKey(admin, "case", "expenses:write")
	api.do(http.MethodGet, "/v1/whoami", caseToken, nil)
	api.do(http.MethodGet, "/v1/whoami", caseToken+"x", nil)

	logs := api.logs.String()
	if !strings.Contains(logs, `"route":"GET /v1/whoami"`) {
		t.Fatalf("access log missing: %s", logs)
	}
	for _, secret := range []string{admin, caseToken, "lst_"} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain token material %q", secret)
		}
	}
}
