package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
)

var codeChallenge = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

var scopeLabels = map[accounts.Scope]string{
	accounts.ScopeExpensesRead:    "Consultar despesas, categorias e dúvidas",
	accounts.ScopeExpensesWrite:   "Registrar, corrigir e classificar despesas",
	accounts.ScopeStatementsWrite: "Enviar leituras de faturas",
}

type authorizeRequest struct {
	ClientID            string
	RedirectURI         string
	ResponseType        string
	State               string
	CodeChallenge       string
	CodeChallengeMethod string
	Scope               string
	Resource            string
}

type pageError struct {
	status  int
	message string
}

type redirectError struct {
	code        string
	description string
}

type consentPage struct {
	Request    authorizeRequest
	ClientName string
	ClientHost string
	Scopes     []scopeOption
	Error      string
}

type scopeOption struct {
	Value string
	Label string
}

func (s *Server) handleProtectedResource(w http.ResponseWriter, _ *http.Request) {
	scopes := make([]string, len(SupportedScopes))
	for i, scope := range SupportedScopes {
		scopes[i] = string(scope)
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 s.Resource(),
		"authorization_servers":    []string{s.Issuer()},
		"scopes_supported":         scopes,
		"bearer_methods_supported": []string{"header"},
		"resource_name":            "Lastro",
	})
}

func (s *Server) handleAuthorizationServer(w http.ResponseWriter, _ *http.Request) {
	scopes := make([]string, len(SupportedScopes))
	for i, scope := range SupportedScopes {
		scopes[i] = string(scope)
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         s.Issuer(),
		"authorization_endpoint":                         s.Issuer() + "/oauth/authorize",
		"token_endpoint":                                 s.Issuer() + "/oauth/token",
		"registration_endpoint":                          s.Issuer() + "/oauth/register",
		"revocation_endpoint":                            s.Issuer() + "/oauth/revoke",
		"response_types_supported":                       []string{responseTypeCode},
		"grant_types_supported":                          []string{grantCode, grantRefresh},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{authMethodNone, authMethodPost, authMethodBasic},
		"scopes_supported":                               scopes,
		"authorization_response_iss_parameter_supported": true,
		"client_id_metadata_document_supported":          true,
	})
}

func (s *Server) handleAuthorizePage(w http.ResponseWriter, r *http.Request) {
	request := readAuthorizeRequest(r.URL.Query())
	client, scopes, redirect, pageErr, redirectErr := s.validateAuthorize(r.Context(), request)
	switch {
	case pageErr != nil:
		s.renderError(w, *pageErr)
	case redirectErr != nil:
		s.redirectWithError(w, r, redirect, request.State, *redirectErr)
	default:
		s.renderConsent(w, http.StatusOK, consentFor(request, client, scopes, redirect, ""), redirect)
	}
}

func (s *Server) handleAuthorizeDecision(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		s.renderError(w, pageError{status: http.StatusBadRequest, message: "Formulário inválido."})
		return
	}
	request := readAuthorizeRequest(r.PostForm)
	client, requested, redirect, pageErr, redirectErr := s.validateAuthorize(r.Context(), request)
	switch {
	case pageErr != nil:
		s.renderError(w, *pageErr)
		return
	case redirectErr != nil:
		s.redirectWithError(w, r, redirect, request.State, *redirectErr)
		return
	}
	if r.PostForm.Get("decision") != "approve" {
		s.redirectWithError(w, r, redirect, request.State, redirectError{code: "access_denied", description: "Rudolfo declined the authorization"})
		return
	}

	principal, err := s.accounts.Authenticate(r.Context(), r.PostForm.Get("api_key"))
	if errors.Is(err, accounts.ErrUnauthenticated) || (err == nil && !principal.Has(accounts.ScopeAdmin)) {
		s.renderConsent(w, http.StatusUnauthorized, consentFor(request, client, requested, redirect, "Chave inválida, revogada ou sem permissão de administrador."), redirect)
		return
	}
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	var granted []string
	for _, scope := range r.PostForm["scopes"] {
		if slices.Contains(requested, scope) && !slices.Contains(granted, scope) {
			granted = append(granted, scope)
		}
	}
	if len(granted) == 0 {
		s.renderConsent(w, http.StatusBadRequest, consentFor(request, client, requested, redirect, "Escolha ao menos uma permissão."), redirect)
		return
	}

	code, codeHash := opaqueValue(codePrefix)
	err = s.store.PlatformTx(r.Context(), func(q *platformdb.Queries) error {
		grant, err := q.CreateOAuthGrant(r.Context(), platformdb.CreateOAuthGrantParams{
			ID:        store.NewID(),
			AccountID: principal.AccountID,
			ClientID:  client.ID,
			ApiKeyID:  principal.KeyID,
			Scopes:    granted,
		})
		if err != nil {
			return fmt.Errorf("create OAuth grant: %w", err)
		}
		return q.CreateAuthorizationCode(r.Context(), platformdb.CreateAuthorizationCodeParams{
			ID:            store.NewID(),
			CodeHash:      codeHash,
			GrantID:       grant.ID,
			ClientID:      client.ID,
			RedirectUri:   request.RedirectURI,
			CodeChallenge: request.CodeChallenge,
			Resource:      s.Resource(),
			ExpiresAt:     s.now().Add(codeTTL),
		})
	})
	if err != nil {
		s.internalError(w, r, err)
		return
	}
	target := *redirect
	query := target.Query()
	query.Set("code", code)
	if request.State != "" {
		query.Set("state", request.State)
	}
	query.Set("iss", s.Issuer())
	target.RawQuery = query.Encode()
	noStore(w)
	http.Redirect(w, r, target.String(), http.StatusFound) //nolint:gosec // target is a redirect URI the client registered, on an allowed host
}

func (s *Server) validateAuthorize(ctx context.Context, request authorizeRequest) (platformdb.OauthClient, []string, *url.URL, *pageError, *redirectError) {
	client, err := s.client(ctx, request.ClientID)
	if errors.Is(err, errUnknownClient) {
		return client, nil, nil, &pageError{status: http.StatusBadRequest, message: "O aplicativo que pediu acesso não é reconhecido pelo Lastro."}, nil
	}
	if err != nil {
		s.logger.ErrorContext(ctx, "authorize failed", slog.String("error", err.Error()))
		return client, nil, nil, &pageError{status: http.StatusInternalServerError, message: "O Lastro falhou ao processar o pedido. Tente de novo."}, nil
	}
	if !slices.Contains(client.RedirectUris, request.RedirectURI) || !s.allowedRedirect(request.RedirectURI) {
		return client, nil, nil, &pageError{status: http.StatusBadRequest, message: "O endereço de retorno não pertence a esse aplicativo."}, nil
	}
	redirect, _ := url.Parse(request.RedirectURI)
	switch {
	case request.ResponseType != responseTypeCode:
		return client, nil, redirect, nil, &redirectError{code: "unsupported_response_type", description: "response_type must be code"}
	case request.CodeChallengeMethod != "S256" || !codeChallenge.MatchString(request.CodeChallenge):
		return client, nil, redirect, nil, &redirectError{code: errInvalidRequest, description: "PKCE with code_challenge_method S256 is required"}
	case request.Resource != "" && request.Resource != s.Resource():
		return client, nil, redirect, nil, &redirectError{code: "invalid_target", description: "resource must be " + s.Resource()}
	}
	scopes, ok := parseScopes(request.Scope)
	if !ok {
		return client, nil, redirect, nil, &redirectError{code: "invalid_scope", description: "scope may only list expenses:read, expenses:write and statements:write"}
	}
	return client, scopes, redirect, nil, nil
}

func readAuthorizeRequest(values url.Values) authorizeRequest {
	return authorizeRequest{
		ClientID:            values.Get("client_id"),
		RedirectURI:         values.Get("redirect_uri"),
		ResponseType:        values.Get("response_type"),
		State:               values.Get("state"),
		CodeChallenge:       values.Get("code_challenge"),
		CodeChallengeMethod: values.Get("code_challenge_method"),
		Scope:               values.Get("scope"),
		Resource:            values.Get("resource"),
	}
}

func consentFor(request authorizeRequest, client platformdb.OauthClient, scopes []string, redirect *url.URL, message string) consentPage {
	page := consentPage{Request: request, ClientHost: redirect.Hostname(), ClientName: redirect.Hostname(), Error: message}
	if client.ClientName != nil {
		page.ClientName = *client.ClientName
	}
	for _, scope := range scopes {
		page.Scopes = append(page.Scopes, scopeOption{Value: scope, Label: scopeLabels[accounts.Scope(scope)]})
	}
	return page
}

func (s *Server) renderConsent(w http.ResponseWriter, status int, page consentPage, redirect *url.URL) {
	pageHeaders(w, redirect.Scheme+"://"+redirect.Host)
	w.WriteHeader(status)
	if err := s.pages.ExecuteTemplate(w, "consent.html", page); err != nil {
		s.logger.Error("render consent page", slog.String("error", err.Error()))
	}
}

func (s *Server) renderError(w http.ResponseWriter, e pageError) {
	pageHeaders(w, "")
	w.WriteHeader(e.status)
	if err := s.pages.ExecuteTemplate(w, "error.html", e.message); err != nil {
		s.logger.Error("render error page", slog.String("error", err.Error()))
	}
}

func (s *Server) redirectWithError(w http.ResponseWriter, r *http.Request, redirect *url.URL, state string, e redirectError) {
	target := *redirect
	query := target.Query()
	query.Set("error", e.code)
	query.Set("error_description", e.description)
	if state != "" {
		query.Set("state", state)
	}
	query.Set("iss", s.Issuer())
	target.RawQuery = query.Encode()
	noStore(w)
	http.Redirect(w, r, target.String(), http.StatusFound) //nolint:gosec // target is a redirect URI the client registered, on an allowed host
}

func pageHeaders(w http.ResponseWriter, redirectOrigin string) {
	formAction := "'self'"
	if redirectOrigin != "" {
		formAction += " " + redirectOrigin
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; form-action "+formAction+"; frame-ancestors 'none'; base-uri 'none'")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	noStore(w)
}

func verifierMatches(verifier, challenge string) bool {
	if len(verifier) < 43 || len(verifier) > 128 || strings.Trim(verifier, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~") != "" {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:]) == challenge
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeOAuthError(w http.ResponseWriter, status int, code, description string) {
	noStore(w)
	writeJSON(w, status, map[string]string{"error": code, "error_description": description})
}

func (s *Server) internalError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.ErrorContext(r.Context(), "oauth request failed", slog.String("path", r.URL.Path), slog.String("error", err.Error()))
	writeOAuthError(w, http.StatusInternalServerError, "server_error", "Lastro failed to process the request")
}
