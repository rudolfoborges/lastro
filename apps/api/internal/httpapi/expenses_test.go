package httpapi_test

import (
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

func (a *testAPI) spend(accountID, key string, minor int64, on string, category string, status ledger.AllocationStatus) uuid.UUID {
	a.t.Helper()
	day, err := calendar.ParseDate(on)
	if err != nil {
		a.t.Fatal(err)
	}
	report := ledger.ExpenseReport{
		RequestID: key, Conversation: "conversation-1", ReceivedAt: time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC),
		RawText: "gasto " + key, Description: "Compra " + key, Amount: &money.Money{Minor: minor, Currency: "BRL"},
		OccurredOn: &day, PaymentMethod: &ledger.PaymentMethodRef{Kind: ledger.PaymentPix}, ConfirmDistinct: true,
	}
	if category != "" {
		report.Category, report.CategoryStatus = &category, status
	}
	outcome, err := a.ledger.RecordExpense(a.t.Context(), uuid.MustParse(accountID), report, ledger.Actor{Author: ledger.AuthorAgent})
	if err != nil {
		a.t.Fatal(err)
	}
	return outcome.Transaction.ID
}

func number(body map[string]any, path ...string) float64 {
	var current any = body
	for _, key := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return -1
		}
		current = object[key]
	}
	value, ok := current.(float64)
	if !ok {
		return -1
	}
	return value
}

func list(body map[string]any, key string) []map[string]any {
	raw, _ := body[key].([]any)
	out := make([]map[string]any, 0, len(raw))
	for _, item := range raw {
		if object, ok := item.(map[string]any); ok {
			out = append(out, object)
		}
	}
	return out
}

func TestTheAppReadsAMonthWithItsTotalsCategoriesAndDays(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	accountID, admin := api.signUp("Rudolfo")
	_, app := api.createKey(admin, "android", "wallet:ingest", "expenses:read")
	api.spend(accountID, "a", 10000, "2026-09-03", "groceries", ledger.AllocationConfirmed)
	api.spend(accountID, "b", 2500, "2026-09-04", "", "")

	res := api.do(http.MethodGet, "/v1/overview?month=2026-09", app, nil)

	assertStatus(t, res, http.StatusOK, "")
	if number(res.body, "totals", "gross", "amount", "minor") != 12500 || field(res.body, "totals", "gross", "amount", "formatted") != "R$ 125,00" {
		t.Errorf("totals = %s, want R$ 125,00", res.raw)
	}
	if field(res.body, "period", "month") != "2026-09" || field(res.body, "period", "basis") != "purchase_date" || field(res.body, "period", "timezone") != "America/Sao_Paulo" {
		t.Errorf("period = %v, want September by purchase date in the account time zone", res.body["period"])
	}
	categories := list(res.body, "categories")
	if len(categories) != 2 || field(categories[0], "key") != "groceries" || field(categories[1], "kind") != "uncategorized" || number(categories[0], "share_basis_points") != 8000 {
		t.Fatalf("categories = %v, want groceries (80%%) and the expense without category", categories)
	}
	filter, _ := categories[0]["filter"].(map[string]any)
	if from, _ := filter["from"].([]any); len(from) != 1 || from[0] != "2026-09-01" {
		t.Errorf("filter = %v, want the period to open the list with", filter)
	}
	if days := list(res.body, "days"); len(days) != 30 || number(days[2], "gross", "amount", "minor") != 10000 || field(days[2], "coverage") != "unknown" {
		t.Errorf("days = %d, day 3 = %v; want 30 days with the untracked coverage", len(days), days[2])
	}
	if field(res.body, "comparison", "reason") != "no_history" || res.body["comparison"].(map[string]any)["difference"] != nil {
		t.Errorf("comparison = %v, want it unavailable without a difference", res.body["comparison"])
	}
}

func TestTheListOpenedFromABarAddsUpToIt(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	accountID, admin := api.signUp("Rudolfo")
	_, app := api.createKey(admin, "android", "expenses:read")
	api.spend(accountID, "a", 10000, "2026-09-03", "groceries", ledger.AllocationConfirmed)
	api.spend(accountID, "b", 3000, "2026-09-05", "groceries", ledger.AllocationConfirmed)
	api.spend(accountID, "c", 2500, "2026-09-04", "groceries", ledger.AllocationSuggested)
	overview := api.do(http.MethodGet, "/v1/overview?month=2026-09", app, nil)
	group := list(overview.body, "categories")[0]
	query := url.Values{}
	for key, raw := range group["filter"].(map[string]any) {
		for _, value := range raw.([]any) {
			query.Add(key, value.(string))
		}
	}

	res := api.do(http.MethodGet, "/v1/expenses?"+query.Encode(), app, nil)

	assertStatus(t, res, http.StatusOK, "")
	if number(res.body, "totals", "gross", "amount", "minor") != number(group, "gross", "amount", "minor") || number(res.body, "matching") != 2 {
		t.Errorf("list = %s, want the R$ 130,00 of the groceries bar", res.raw)
	}
	items := list(res.body, "items")
	if len(items) != 2 || field(items[0], "date") != "2026-09-05" || field(items[0], "payment", "label") != "Pix" || items[0]["in_total"] != true {
		t.Errorf("items = %v, want the newest first with its payment", items)
	}
}

func TestExpenseRoutesNeedTheReadScope(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, ingest := api.createKey(admin, "android", "wallet:ingest")

	for _, path := range []string{"/v1/overview", "/v1/expenses", "/v1/expenses/" + uuid.NewString()} {
		assertStatus(t, api.do(http.MethodGet, path, ingest, nil), http.StatusForbidden, "forbidden")
		assertStatus(t, api.do(http.MethodGet, path, "", nil), http.StatusUnauthorized, "unauthorized")
	}
}

func TestExpenseQueriesExplainInvalidParameters(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	_, admin := api.signUp("Rudolfo")
	_, app := api.createKey(admin, "android", "expenses:read")

	for _, path := range []string{
		"/v1/overview?month=2026-13",
		"/v1/overview?month=2999-01",
		"/v1/overview?currency=XXX",
		"/v1/expenses?from=2026-09-01",
		"/v1/expenses?limit=0",
		"/v1/expenses?classification=maybe",
	} {
		assertStatus(t, api.do(http.MethodGet, path, app, nil), http.StatusBadRequest, "invalid_request")
	}
}

func TestTheExpenseDetailStaysInsideTheAccount(t *testing.T) {
	api := newTestAPI(t, config.RateLimits{})
	accountID, admin := api.signUp("Rudolfo")
	_, app := api.createKey(admin, "android", "expenses:read")
	otherID, _ := api.signUp("Bob")
	mine := api.spend(accountID, "a", 10000, "2026-09-03", "groceries", ledger.AllocationSuggested)
	theirs := api.spend(otherID, "b", 2000, "2026-09-03", "", "")

	detail := api.do(http.MethodGet, "/v1/expenses/"+mine.String(), app, nil)

	assertStatus(t, detail, http.StatusOK, "")
	if field(detail.body, "status") != "confirmed" || field(detail.body, "classification") != "suggested" || field(detail.body, "category", "label") != "Mercado" || detail.body["in_total"] != true {
		t.Errorf("detail = %s, want a confirmed expense with a suggested category", detail.raw)
	}
	if field(detail.body, "last_change", "author") != "agent" || field(detail.body, "date") != "2026-09-03" {
		t.Errorf("detail = %s, want the date and who changed it last", detail.raw)
	}
	assertStatus(t, api.do(http.MethodGet, "/v1/expenses/"+theirs.String(), app, nil), http.StatusNotFound, "not_found")
	assertStatus(t, api.do(http.MethodGet, "/v1/expenses/not-an-id", app, nil), http.StatusNotFound, "not_found")
}
