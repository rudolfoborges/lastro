package oauth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
)

const maxTokenRequestBytes = 16 << 10

var errInvalidGrant = errors.New("invalid_grant")

type tokenError struct {
	status      int
	code        string
	description string
}

func (s *Server) Routes(mux *http.ServeMux, limited func(http.HandlerFunc) http.HandlerFunc) {
	mux.HandleFunc("GET /.well-known/oauth-protected-resource", s.handleProtectedResource)
	mux.HandleFunc("GET /.well-known/oauth-protected-resource/mcp", s.handleProtectedResource)
	mux.HandleFunc("GET /.well-known/oauth-authorization-server", s.handleAuthorizationServer)
	if s.cfg.DynamicRegistration {
		mux.HandleFunc("POST /oauth/register", limited(s.handleRegister))
	}
	mux.HandleFunc("GET /oauth/authorize", s.handleAuthorizePage)
	mux.HandleFunc("POST /oauth/authorize", limited(s.handleAuthorizeDecision))
	mux.HandleFunc("POST /oauth/token", limited(s.handleToken))
	mux.HandleFunc("POST /oauth/revoke", limited(s.handleRevoke))
}

func (s *Server) handleToken(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTokenRequestBytes)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "the body must be application/x-www-form-urlencoded")
		return
	}
	client, tokenErr := s.authenticateClient(r)
	if tokenErr != nil {
		s.writeTokenError(w, *tokenErr)
		return
	}
	if resource := r.PostForm.Get("resource"); resource != "" && resource != s.Resource() {
		writeOAuthError(w, http.StatusBadRequest, "invalid_target", "resource must be "+s.Resource())
		return
	}

	var tokens issuedTokens
	var err error
	switch r.PostForm.Get("grant_type") {
	case grantCode:
		tokens, err = s.exchangeCode(r.Context(), client, r.PostForm.Get("code"), r.PostForm.Get("redirect_uri"), r.PostForm.Get("code_verifier"))
	case grantRefresh:
		tokens, err = s.refresh(r.Context(), client, r.PostForm.Get("refresh_token"), r.PostForm.Get("scope"))
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type must be authorization_code or refresh_token")
		return
	}
	var invalidScope *tokenError
	switch {
	case errors.Is(err, errInvalidGrant):
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "the code or refresh token is invalid, expired, already used or revoked")
	case errors.As(err, &invalidScope):
		s.writeTokenError(w, *invalidScope)
	case err != nil:
		s.internalError(w, r, err)
	default:
		noStore(w)
		writeJSON(w, http.StatusOK, tokens)
	}
}

func (e *tokenError) Error() string {
	return e.code
}

func (s *Server) exchangeCode(ctx context.Context, client platformdb.OauthClient, code, redirectURI, verifier string) (issuedTokens, error) {
	var tokens issuedTokens
	reused := false
	err := s.store.PlatformTx(ctx, func(q *platformdb.Queries) error {
		row, err := q.GetAuthorizationCode(ctx, digest([]byte(strings.TrimSpace(code))))
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidGrant
		}
		if err != nil {
			return fmt.Errorf("get authorization code: %w", err)
		}
		if row.UsedAt != nil {
			reused = true
			return q.RevokeOAuthGrant(ctx, row.GrantID)
		}
		grant, err := q.GetOAuthGrant(ctx, row.GrantID)
		if err != nil {
			return fmt.Errorf("get OAuth grant: %w", err)
		}
		valid := row.ClientID == client.ID &&
			row.RedirectUri == redirectURI &&
			s.now().Before(row.ExpiresAt) &&
			row.Resource == s.Resource() &&
			grant.RevokedAt == nil &&
			verifierMatches(verifier, row.CodeChallenge)
		if !valid {
			return errInvalidGrant
		}
		if err := q.UseAuthorizationCode(ctx, row.ID); err != nil {
			return fmt.Errorf("use authorization code: %w", err)
		}
		tokens, err = s.issueTokens(ctx, q, grant.ID, grant.Scopes)
		return err
	})
	if reused && err == nil {
		return issuedTokens{}, errInvalidGrant
	}
	return tokens, err
}

func (s *Server) refresh(ctx context.Context, client platformdb.OauthClient, refreshToken, scope string) (issuedTokens, error) {
	id, secret, err := parseSecret(refreshTokenPrefix, refreshToken)
	if err != nil {
		return issuedTokens{}, errInvalidGrant
	}
	var tokens issuedTokens
	reused := false
	err = s.store.PlatformTx(ctx, func(q *platformdb.Queries) error {
		row, err := q.GetOAuthToken(ctx, id)
		if errors.Is(err, pgx.ErrNoRows) {
			return errInvalidGrant
		}
		if err != nil {
			return fmt.Errorf("get refresh token: %w", err)
		}
		if row.Kind != kindRefresh || !secretMatches(secret, row.TokenHash) || row.ClientID != client.ID {
			return errInvalidGrant
		}
		if row.UsedAt != nil {
			reused = true
			return q.RevokeOAuthGrant(ctx, row.GrantID)
		}
		if row.GrantRevokedAt != nil || !s.now().Before(row.ExpiresAt) || row.Resource != s.Resource() {
			return errInvalidGrant
		}
		scopes := row.Scopes
		if requested := strings.Fields(scope); len(requested) > 0 {
			for _, value := range requested {
				if !slices.Contains(row.Scopes, value) {
					return &tokenError{status: http.StatusBadRequest, code: "invalid_scope", description: "a refresh can only keep or narrow the granted scopes"}
				}
			}
			scopes = requested
		}
		if err := q.UseRefreshToken(ctx, row.ID); err != nil {
			return fmt.Errorf("use refresh token: %w", err)
		}
		tokens, err = s.issueTokens(ctx, q, row.GrantID, scopes)
		return err
	})
	if reused && err == nil {
		return issuedTokens{}, errInvalidGrant
	}
	return tokens, err
}

func (s *Server) handleRevoke(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxTokenRequestBytes)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, errInvalidRequest, "the body must be application/x-www-form-urlencoded")
		return
	}
	client, tokenErr := s.authenticateClient(r)
	if tokenErr != nil {
		s.writeTokenError(w, *tokenErr)
		return
	}
	token := r.PostForm.Get("token")
	for _, prefix := range []string{AccessTokenPrefix, refreshTokenPrefix} {
		id, secret, err := parseSecret(prefix, token)
		if err != nil {
			continue
		}
		row, err := s.store.Platform().GetOAuthToken(r.Context(), id)
		if err == nil && secretMatches(secret, row.TokenHash) && row.ClientID == client.ID {
			if err := s.store.Platform().RevokeOAuthGrant(r.Context(), row.GrantID); err != nil {
				s.internalError(w, r, err)
				return
			}
		}
	}
	noStore(w)
	w.WriteHeader(http.StatusOK)
}

func (s *Server) authenticateClient(r *http.Request) (platformdb.OauthClient, *tokenError) {
	clientID, clientSecret, basic := r.BasicAuth()
	if !basic {
		clientID, clientSecret = r.PostForm.Get("client_id"), r.PostForm.Get("client_secret")
	}
	invalid := &tokenError{status: http.StatusUnauthorized, code: errInvalidClient, description: "client authentication failed"}
	client, err := s.client(r.Context(), clientID)
	if err != nil {
		return client, invalid
	}
	if client.SecretHash == nil {
		if clientSecret != "" {
			return client, invalid
		}
		return client, nil
	}
	if !secretMatches([]byte(clientSecret), client.SecretHash) {
		return client, invalid
	}
	return client, nil
}

func (s *Server) writeTokenError(w http.ResponseWriter, e tokenError) {
	if e.status == http.StatusUnauthorized {
		w.Header().Set("WWW-Authenticate", `Basic realm="lastro"`)
	}
	writeOAuthError(w, e.status, e.code, e.description)
}
