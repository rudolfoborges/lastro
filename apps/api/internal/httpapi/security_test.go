package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
)

var protectedRoutes = []struct {
	method string
	path   string
}{
	{http.MethodGet, "/v1/whoami"},
	{http.MethodPatch, "/v1/account"},
	{http.MethodGet, "/v1/api-keys"},
	{http.MethodPost, "/v1/api-keys"},
	{http.MethodPost, "/v1/api-keys/01a17000-0000-7000-8000-000000000000/revoke"},
	{http.MethodPost, "/v1/wallet/notifications"},
	{http.MethodGet, "/v1/wallet/notifications"},
	{http.MethodPost, "/v1/wallet/heartbeat"},
}

func TestEveryRouteWithDataRefusesAnonymousAndWrongScopeRequests(t *testing.T) {
	a := newTestAPI(t, config.RateLimits{PerKeyPerMinute: 100, UnauthenticatedPerMinute: 100})
	_, admin := a.signUp("Rudolfo")
	phone := a.do(http.MethodPost, "/v1/api-keys", admin, map[string]any{"name": "android", "scopes": []string{"wallet:ingest"}})
	phoneToken := field(phone.body, "api_key", "token")

	for _, route := range protectedRoutes {
		rec := httptest.NewRecorder()
		a.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), route.method, route.path, strings.NewReader("{}")))
		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a key = %d, want 401", route.method, route.path, rec.Code)
		}
	}
	for _, route := range protectedRoutes[1:5] {
		if got := a.do(route.method, route.path, phoneToken, map[string]any{}); got.status != http.StatusForbidden {
			t.Errorf("%s %s with the phone key = %d, want 403", route.method, route.path, got.status)
		}
	}
}
