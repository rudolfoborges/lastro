package httpapi_test

import (
	"net/http"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
)

func TestTheAppReadsTheMonthsAndTheirComparison(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, app := api.createKey(admin, "android", "expenses:read")
	_, ingest := api.createKey(admin, "android-old", "wallet:ingest")

	res := api.do(http.MethodGet, "/v1/analysis?months=2", app, nil)

	assertStatus(t, res, http.StatusOK, "")
	months := list(res.body, "months")
	if len(months) != 2 || months[1]["in_progress"] != true || field(months[0], "coverage") != "none" {
		t.Errorf("months = %v, want last month untracked and this one in progress", months)
	}
	if field(res.body, "same_days", "reason") != "no_history" || res.body["history_starts_on"] != nil || len(list(res.body, "categories")) != 0 {
		t.Errorf("analysis = %s, want no comparison before any tracking", res.raw)
	}
	assertStatus(t, api.do(http.MethodGet, "/v1/analysis?months=13", app, nil), http.StatusBadRequest, "invalid_request")
	assertStatus(t, api.do(http.MethodGet, "/v1/analysis?months=x", app, nil), http.StatusBadRequest, "invalid_request")
	assertStatus(t, api.do(http.MethodGet, "/v1/analysis", ingest, nil), http.StatusForbidden, "forbidden")
}
