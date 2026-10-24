package httpapi_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/httpapi"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
	"github.com/rudolfoborges/lastro/apps/api/internal/oauth"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/storetest"
)

const (
	chatGPTClientID = "https://chatgpt.com/oauth/client.json"
	chatGPTRedirect = "https://chatgpt.com/connector_platform_oauth_redirect"
	verifier        = "a-verifier-long-enough-to-satisfy-rfc-7636-requirements"
)

type stubFetcher struct{ calls int }

func (f *stubFetcher) Fetch(_ context.Context, documentURL string) ([]byte, error) {
	f.calls++
	return []byte(`{"client_id":"` + documentURL + `","client_name":"ChatGPT","redirect_uris":["` + chatGPTRedirect + `"],` +
		`"token_endpoint_auth_method":"private_key_jwt","token_endpoint_auth_methods_supported":["none","private_key_jwt"],` +
		`"grant_types":["authorization_code","refresh_token"],"response_types":["code"]}`), nil
}

type oauthEnv struct {
	t        *testing.T
	base     string
	accounts *accounts.Service
	fetcher  *stubFetcher
	logs     *syncBuffer
	client   *http.Client
}

func newOAuthEnv(t *testing.T, options ...func(*oauth.Config)) *oauthEnv {
	t.Helper()
	db, err := store.Open(t.Context(), storetest.MigratedDatabaseURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(db.Close)
	server := httptest.NewUnstartedServer(nil)
	base := "http://" + server.Listener.Addr().String()
	issuer, _ := url.Parse(base)
	logs := &syncBuffer{}
	logger := slog.New(slog.NewJSONHandler(logs, nil))
	accountsService := accounts.NewService(db)
	fetcher := &stubFetcher{}
	cfg := oauth.Config{
		Issuer:              issuer,
		RedirectHosts:       []string{"chatgpt.com"},
		MetadataHosts:       []string{"chatgpt.com"},
		DynamicRegistration: true,
		AllowLoopback:       true,
		Fetcher:             fetcher,
	}
	for _, option := range options {
		option(&cfg)
	}
	authorization := oauth.New(db, accountsService, logger, cfg)
	mcp := mcpserver.New(mcpserver.Deps{Accounts: accountsService, Ledger: ledger.NewService(db), Logger: logger})
	server.Config.Handler = httpapi.NewHandler(httpapi.Deps{
		Logger: logger, Database: db, Accounts: accountsService, RateLimits: config.RateLimits{},
		MCP: mcp.HTTPHandler(), OAuth: authorization,
	})
	server.Start()
	t.Cleanup(server.Close)
	noRedirects := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return &oauthEnv{t: t, base: base, accounts: accountsService, fetcher: fetcher, logs: logs, client: noRedirects}
}

func challengeOf(v string) string {
	sum := sha256.Sum256([]byte(v))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (e *oauthEnv) admin(name string) string {
	e.t.Helper()
	return keyFor(e.t, e.accounts, name, "admin")
}

func (e *oauthEnv) authorizeQuery(clientID, redirect string) url.Values {
	return url.Values{
		"response_type":         {"code"},
		"client_id":             {clientID},
		"redirect_uri":          {redirect},
		"state":                 {"state-123"},
		"code_challenge":        {challengeOf(verifier)},
		"code_challenge_method": {"S256"},
		"scope":                 {"expenses:read expenses:write"},
		"resource":              {e.base + "/mcp"},
	}
}

type reply struct {
	status int
	header http.Header
	body   []byte
}

func (e *oauthEnv) do(req *http.Request) reply {
	e.t.Helper()
	res, err := e.client.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return reply{status: res.StatusCode, header: res.Header, body: body}
}

func (e *oauthEnv) post(path string, form url.Values) reply {
	e.t.Helper()
	req, err := http.NewRequestWithContext(e.t.Context(), http.MethodPost, e.base+path, strings.NewReader(form.Encode()))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return e.do(req)
}

func (e *oauthEnv) get(path string) reply {
	e.t.Helper()
	req, err := http.NewRequestWithContext(e.t.Context(), http.MethodGet, e.base+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	return e.do(req)
}

func (e *oauthEnv) approve(clientID, redirect, adminToken string, scopes ...string) url.Values {
	e.t.Helper()
	form := e.authorizeQuery(clientID, redirect)
	form.Set("decision", "approve")
	form.Set("api_key", adminToken)
	for _, scope := range scopes {
		form.Add("scopes", scope)
	}
	res := e.post("/oauth/authorize", form)
	if res.status != http.StatusFound {
		e.t.Fatalf("authorize status = %d, body = %s", res.status, res.body)
	}
	location, err := url.Parse(res.header.Get("Location"))
	if err != nil {
		e.t.Fatal(err)
	}
	return location.Query()
}

func (e *oauthEnv) token(form url.Values) (int, map[string]any) {
	e.t.Helper()
	res := e.post("/oauth/token", form)
	var body map[string]any
	_ = json.Unmarshal(res.body, &body)
	return res.status, body
}

func (e *oauthEnv) exchange(clientID, redirect, code string) (int, map[string]any) {
	e.t.Helper()
	return e.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code}, "redirect_uri": {redirect}, "code_verifier": {verifier}, "resource": {e.base + "/mcp"}})
}

func (e *oauthEnv) mcpWorks(accessToken string) bool {
	e.t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "chatgpt-test", Version: "1"}, nil)
	session, err := client.Connect(e.t.Context(), &mcp.StreamableClientTransport{
		Endpoint:   e.base + "/mcp",
		HTTPClient: &http.Client{Transport: bearerTransport{token: accessToken}},
	}, nil)
	if err != nil {
		return false
	}
	defer func() { _ = session.Close() }()
	res, err := session.CallTool(e.t.Context(), &mcp.CallToolParams{Name: "list_categories", Arguments: map[string]any{}})
	return err == nil && !res.IsError
}

func TestDiscoveryDocumentsDescribeTheAuthorizationServer(t *testing.T) {
	e := newOAuthEnv(t)

	resource, server := e.getJSON("/.well-known/oauth-protected-resource"), e.getJSON("/.well-known/oauth-authorization-server")

	if resource["resource"] != e.base+"/mcp" || resource["authorization_servers"].([]any)[0] != e.base {
		t.Errorf("protected resource = %+v", resource)
	}
	if server["issuer"] != e.base || server["client_id_metadata_document_supported"] != true || server["authorization_response_iss_parameter_supported"] != true {
		t.Errorf("authorization server = %+v", server)
	}
	if methods := server["code_challenge_methods_supported"].([]any); len(methods) != 1 || methods[0] != "S256" {
		t.Errorf("code challenge methods = %v", methods)
	}

	res := e.post("/mcp", url.Values{})
	if res.status != http.StatusUnauthorized || !strings.Contains(res.header.Get("WWW-Authenticate"), `resource_metadata="`+e.base+`/.well-known/oauth-protected-resource"`) {
		t.Errorf("/mcp without token = %d %q", res.status, res.header.Get("WWW-Authenticate"))
	}
}

func TestDynamicClientRegistrationIsOffUnlessConfigured(t *testing.T) {
	off := newOAuthEnv(t, func(cfg *oauth.Config) { cfg.DynamicRegistration = false })
	on := newOAuthEnv(t)

	if _, advertised := off.getJSON("/.well-known/oauth-authorization-server")["registration_endpoint"]; advertised {
		t.Error("the metadata advertises registration while it is off")
	}
	res, err := http.Post(off.base+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["`+chatGPTRedirect+`"]}`)) //nolint:noctx // test helper
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Errorf("registering while off = %d, want 404", res.StatusCode)
	}
	if on.getJSON("/.well-known/oauth-authorization-server")["registration_endpoint"] != on.base+"/oauth/register" {
		t.Error("the metadata does not advertise registration while it is on")
	}
}

func TestAClientThatAsksForNoScopeIsOfferedEveryScope(t *testing.T) {
	e := newOAuthEnv(t)
	query := e.authorizeQuery(chatGPTClientID, chatGPTRedirect)
	query.Del("scope")

	page := e.get("/oauth/authorize?" + query.Encode())

	if page.status != http.StatusOK || !strings.Contains(string(page.body), `value="statements:write"`) {
		t.Errorf("consent page = %d, want statements:write offered", page.status)
	}
}

func TestChatGPTConnectsWithAMetadataDocumentClient(t *testing.T) {
	e := newOAuthEnv(t)
	adminToken := e.admin("Rudolfo")

	page := e.get("/oauth/authorize?" + e.authorizeQuery(chatGPTClientID, chatGPTRedirect).Encode())
	if page.status != http.StatusOK || !strings.Contains(string(page.body), "ChatGPT quer acessar o Lastro") || !strings.Contains(page.header.Get("Content-Security-Policy"), "form-action 'self' https://chatgpt.com") {
		t.Fatalf("consent page = %d %s", page.status, page.header)
	}

	callback := e.approve(chatGPTClientID, chatGPTRedirect, adminToken, "expenses:read", "expenses:write")
	if callback.Get("state") != "state-123" || callback.Get("iss") != e.base || callback.Get("code") == "" {
		t.Fatalf("callback = %v", callback)
	}
	status, tokens := e.exchange(chatGPTClientID, chatGPTRedirect, callback.Get("code"))
	if status != http.StatusOK || tokens["token_type"] != "Bearer" || tokens["scope"] != "expenses:read expenses:write" {
		t.Fatalf("token = %d %+v", status, tokens)
	}
	if !e.mcpWorks(tokens["access_token"].(string)) {
		t.Error("the access token does not open /mcp")
	}
	if e.fetcher.calls != 1 {
		t.Errorf("metadata document fetched %d times, want once (cached)", e.fetcher.calls)
	}

	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, e.base+"/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["access_token"].(string))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusUnauthorized {
		t.Errorf("/v1/whoami with an MCP token = %d, want 401 (wrong audience)", res.StatusCode)
	}
	logs := e.logs.String()
	for _, secret := range []string{tokens["access_token"].(string), tokens["refresh_token"].(string), callback.Get("code"), adminToken} {
		if strings.Contains(logs, secret) {
			t.Errorf("logs contain a secret")
		}
	}
}

func TestDynamicClientRegistrationAndRefreshRotation(t *testing.T) {
	e := newOAuthEnv(t)
	adminToken := e.admin("Rudolfo")
	redirect := "http://127.0.0.1:9999/callback"
	res, err := http.Post(e.base+"/oauth/register", "application/json", strings.NewReader(`{"client_name":"Inspector","redirect_uris":["`+redirect+`"]}`)) //nolint:noctx // test helper
	if err != nil {
		t.Fatal(err)
	}
	var registered map[string]any
	_ = json.NewDecoder(res.Body).Decode(&registered)
	_ = res.Body.Close()
	clientID, _ := registered["client_id"].(string)
	if res.StatusCode != http.StatusCreated || !strings.HasPrefix(clientID, "lcl_") || registered["client_secret"] != nil {
		t.Fatalf("register = %d %+v", res.StatusCode, registered)
	}

	callback := e.approve(clientID, redirect, adminToken, "expenses:read")
	status, first := e.exchange(clientID, redirect, callback.Get("code"))
	if status != http.StatusOK || first["scope"] != "expenses:read" {
		t.Fatalf("token = %d %+v", status, first)
	}

	status, second := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {first["refresh_token"].(string)}})
	if status != http.StatusOK || second["access_token"] == first["access_token"] {
		t.Fatalf("refresh = %d %+v", status, second)
	}
	if !e.mcpWorks(second["access_token"].(string)) {
		t.Fatal("the refreshed access token does not open /mcp")
	}

	status, reused := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {clientID}, "refresh_token": {first["refresh_token"].(string)}})
	if status != http.StatusBadRequest || reused["error"] != "invalid_grant" {
		t.Errorf("reusing a rotated refresh token = %d %+v", status, reused)
	}
	if e.mcpWorks(second["access_token"].(string)) {
		t.Error("refresh token reuse did not revoke the grant")
	}
}

func TestTheTokenEndpointRefusesForgedOrReplayedCodes(t *testing.T) {
	e := newOAuthEnv(t)
	adminToken := e.admin("Rudolfo")

	wrongVerifier := e.approve(chatGPTClientID, chatGPTRedirect, adminToken, "expenses:read")
	status, body := e.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {chatGPTClientID}, "code": {wrongVerifier.Get("code")}, "redirect_uri": {chatGPTRedirect}, "code_verifier": {verifier + "x"}})
	if status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("wrong verifier = %d %+v", status, body)
	}

	wrongRedirect := e.approve(chatGPTClientID, chatGPTRedirect, adminToken, "expenses:read")
	status, body = e.exchange(chatGPTClientID, chatGPTRedirect+"?other=1", wrongRedirect.Get("code"))
	if status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("wrong redirect = %d %+v", status, body)
	}

	replayed := e.approve(chatGPTClientID, chatGPTRedirect, adminToken, "expenses:read")
	status, tokens := e.exchange(chatGPTClientID, chatGPTRedirect, replayed.Get("code"))
	if status != http.StatusOK {
		t.Fatalf("first exchange = %d %+v", status, tokens)
	}
	status, body = e.exchange(chatGPTClientID, chatGPTRedirect, replayed.Get("code"))
	if status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("replayed code = %d %+v", status, body)
	}
	if e.mcpWorks(tokens["access_token"].(string)) {
		t.Error("replaying the code did not revoke the tokens it produced")
	}

	status, body = e.token(url.Values{"grant_type": {"password"}, "client_id": {chatGPTClientID}})
	if status != http.StatusBadRequest || body["error"] != "unsupported_grant_type" {
		t.Errorf("password grant = %d %+v", status, body)
	}
}

func TestOnlyAnAdminKeyAuthorizesAndRudolfoCanRevoke(t *testing.T) {
	e := newOAuthEnv(t)
	adminToken := e.admin("Rudolfo")
	reader := keyFor(t, e.accounts, "Other", "expenses:read")

	form := e.authorizeQuery(chatGPTClientID, chatGPTRedirect)
	form.Set("decision", "approve")
	form.Set("api_key", reader)
	form.Add("scopes", "expenses:read")
	if res := e.post("/oauth/authorize", form); res.status != http.StatusUnauthorized {
		t.Errorf("authorizing with a non-admin key = %d, want 401", res.status)
	}
	form.Set("decision", "deny")
	res := e.post("/oauth/authorize", form)
	location, _ := url.Parse(res.header.Get("Location"))
	if res.status != http.StatusFound || location.Query().Get("error") != "access_denied" {
		t.Errorf("declining = %d %s", res.status, res.header.Get("Location"))
	}

	callback := e.approve(chatGPTClientID, chatGPTRedirect, adminToken, "expenses:read")
	_, tokens := e.exchange(chatGPTClientID, chatGPTRedirect, callback.Get("code"))
	listing := e.adminCall(http.MethodGet, "/v1/oauth/grants", adminToken)
	grants, _ := listing["oauth_grants"].([]any)
	if len(grants) != 1 {
		t.Fatalf("grants = %+v", listing)
	}
	grantID := grants[0].(map[string]any)["id"].(string)
	revoked := e.adminCall(http.MethodPost, "/v1/oauth/grants/"+grantID+"/revoke", adminToken)
	if revoked["oauth_grant"].(map[string]any)["revoked_at"] == nil {
		t.Errorf("revoke = %+v", revoked)
	}
	if e.mcpWorks(tokens["access_token"].(string)) {
		t.Error("a revoked grant still opens /mcp")
	}
}

func TestRevokingAnAdminKeyRevokesTheGrantsItAuthorized(t *testing.T) {
	e := newOAuthEnv(t)
	invite, err := e.accounts.CreateInvite(t.Context(), time.Hour, "")
	if err != nil {
		t.Fatal(err)
	}
	account, owner, err := e.accounts.SignUp(t.Context(), invite.Code, "Rudolfo")
	if err != nil {
		t.Fatal(err)
	}
	laptop, err := e.accounts.CreateKey(t.Context(), account.ID, "laptop", []string{"admin"})
	if err != nil {
		t.Fatal(err)
	}
	_, byLaptop := e.exchange(chatGPTClientID, chatGPTRedirect, e.approve(chatGPTClientID, chatGPTRedirect, laptop.Token, "expenses:read").Get("code"))
	_, byOwner := e.exchange(chatGPTClientID, chatGPTRedirect, e.approve(chatGPTClientID, chatGPTRedirect, owner.Token, "expenses:read").Get("code"))

	e.adminCall(http.MethodPost, "/v1/api-keys/"+laptop.ID.String()+"/revoke", owner.Token)

	if e.mcpWorks(byLaptop["access_token"].(string)) {
		t.Error("a grant authorized with a revoked admin key still opens /mcp")
	}
	status, body := e.token(url.Values{"grant_type": {"refresh_token"}, "client_id": {chatGPTClientID}, "refresh_token": {byLaptop["refresh_token"].(string)}})
	if status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Errorf("refreshing a grant of a revoked key = %d %+v", status, body)
	}
	if !e.mcpWorks(byOwner["access_token"].(string)) {
		t.Error("revoking one admin key revoked a grant authorized with another")
	}
}

func TestTheAuthorizePageRefusesUnknownClientsAndRedirects(t *testing.T) {
	e := newOAuthEnv(t)

	unknown := e.get("/oauth/authorize?" + e.authorizeQuery("lcl_nope", chatGPTRedirect).Encode())
	foreignRedirect := e.get("/oauth/authorize?" + e.authorizeQuery(chatGPTClientID, "https://evil.example/callback").Encode())
	query := e.authorizeQuery(chatGPTClientID, chatGPTRedirect)
	query.Set("code_challenge_method", "plain")
	noPKCE := e.get("/oauth/authorize?" + query.Encode())

	if unknown.status != http.StatusBadRequest || unknown.header.Get("Location") != "" {
		t.Errorf("unknown client = %d %s", unknown.status, unknown.header.Get("Location"))
	}
	if foreignRedirect.status != http.StatusBadRequest || foreignRedirect.header.Get("Location") != "" {
		t.Errorf("foreign redirect = %d %s", foreignRedirect.status, foreignRedirect.header.Get("Location"))
	}
	location, _ := url.Parse(noPKCE.header.Get("Location"))
	if noPKCE.status != http.StatusFound || location.Query().Get("error") != "invalid_request" {
		t.Errorf("plain PKCE = %d %s", noPKCE.status, noPKCE.header.Get("Location"))
	}
	res, err := http.Post(e.base+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["https://evil.example/cb"]}`)) //nolint:noctx // test helper
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("registering a foreign redirect = %d, want 400", res.StatusCode)
	}
}

func (e *oauthEnv) adminCall(method, path, token string) map[string]any {
	e.t.Helper()
	req, err := http.NewRequestWithContext(e.t.Context(), method, e.base+path, nil)
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body map[string]any
	_ = json.NewDecoder(res.Body).Decode(&body)
	return body
}

func TestConfidentialClientsMustPresentTheirSecret(t *testing.T) {
	e := newOAuthEnv(t)
	adminToken := e.admin("Rudolfo")
	redirect := "http://localhost:9999/callback"
	res, err := http.Post(e.base+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["`+redirect+`"],"token_endpoint_auth_method":"client_secret_post"}`)) //nolint:noctx // test helper
	if err != nil {
		t.Fatal(err)
	}
	var registered map[string]any
	_ = json.NewDecoder(res.Body).Decode(&registered)
	_ = res.Body.Close()
	clientID, secret := registered["client_id"].(string), registered["client_secret"].(string)
	if !strings.HasPrefix(secret, "lcs_") {
		t.Fatalf("register = %+v", registered)
	}

	callback := e.approve(clientID, redirect, adminToken, "expenses:read")
	status, body := e.exchange(clientID, redirect, callback.Get("code"))
	if status != http.StatusUnauthorized || body["error"] != "invalid_client" {
		t.Errorf("exchange without secret = %d %+v", status, body)
	}
	callback = e.approve(clientID, redirect, adminToken, "expenses:read")
	status, body = e.token(url.Values{"grant_type": {"authorization_code"}, "client_id": {clientID}, "client_secret": {secret}, "code": {callback.Get("code")}, "redirect_uri": {redirect}, "code_verifier": {verifier}})
	if status != http.StatusOK || body["access_token"] == nil {
		t.Errorf("exchange with secret = %d %+v", status, body)
	}
}

func (e *oauthEnv) getJSON(path string) map[string]any {
	e.t.Helper()
	res := e.get(path)
	var body map[string]any
	if err := json.Unmarshal(res.body, &body); err != nil {
		e.t.Fatal(err)
	}
	return body
}
