package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
)

type accountJSON struct {
	ID        uuid.UUID `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"created_at"`
}

type apiKeyJSON struct {
	ID         uuid.UUID        `json:"id"`
	Name       string           `json:"name"`
	Scopes     []accounts.Scope `json:"scopes"`
	CreatedAt  time.Time        `json:"created_at"`
	LastUsedAt *time.Time       `json:"last_used_at"`
	RevokedAt  *time.Time       `json:"revoked_at"`
	Token      string           `json:"token,omitempty"`
}

func toAccountJSON(account accounts.Account) accountJSON {
	return accountJSON{ID: account.ID, Name: account.Name, CreatedAt: account.CreatedAt.UTC()}
}

func toAPIKeyJSON(key accounts.APIKey) apiKeyJSON {
	return apiKeyJSON{
		ID:         key.ID,
		Name:       key.Name,
		Scopes:     key.Scopes,
		CreatedAt:  key.CreatedAt.UTC(),
		LastUsedAt: utc(key.LastUsedAt),
		RevokedAt:  utc(key.RevokedAt),
	}
}

func toIssuedKeyJSON(key accounts.IssuedKey) apiKeyJSON {
	body := toAPIKeyJSON(key.APIKey)
	body.Token = key.Token
	return body
}

func utc(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	value := t.UTC()
	return &value
}

func (a *api) handleSignUp(w http.ResponseWriter, r *http.Request) {
	if !a.clientLimits.allow(clientIP(r, a.trustProxyHeaders)) {
		writeRateLimited(w, a.clientLimits)
		return
	}
	var body struct {
		InviteCode string `json:"invite_code"`
		Name       string `json:"name"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	account, key, err := a.accounts.SignUp(r.Context(), body.InviteCode, body.Name)
	if err != nil {
		a.writeAccountsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"account": toAccountJSON(account),
		"api_key": toIssuedKeyJSON(key),
	})
}

func (a *api) handleWhoami(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	account, key, err := a.accounts.Whoami(r.Context(), principal)
	if err != nil {
		a.writeAccountsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"account": toAccountJSON(account),
		"api_key": toAPIKeyJSON(key),
	})
}

func (a *api) handleListKeys(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	keys, err := a.accounts.ListKeys(r.Context(), principal.AccountID)
	if err != nil {
		a.writeAccountsError(w, r, err)
		return
	}
	body := make([]apiKeyJSON, 0, len(keys))
	for _, key := range keys {
		body = append(body, toAPIKeyJSON(key))
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_keys": body})
}

func (a *api) handleCreateKey(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	var body struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	key, err := a.accounts.CreateKey(r.Context(), principal.AccountID, body.Name, body.Scopes)
	if err != nil {
		a.writeAccountsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"api_key": toIssuedKeyJSON(key)})
}

func (a *api) handleRevokeKey(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	keyID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	key, err := a.accounts.RevokeKey(r.Context(), principal.AccountID, keyID)
	if err != nil {
		a.writeAccountsError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"api_key": toAPIKeyJSON(key)})
}

func (a *api) writeAccountsError(w http.ResponseWriter, r *http.Request, err error) {
	var validation *accounts.ValidationError
	switch {
	case errors.As(err, &validation):
		writeErrorMessage(w, http.StatusBadRequest, "invalid_request", validation.Error())
	case errors.Is(err, accounts.ErrInvalidInvite):
		writeErrorMessage(w, http.StatusBadRequest, "invalid_invite", err.Error())
	case errors.Is(err, accounts.ErrKeyNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, accounts.ErrLastAdminKey):
		writeErrorMessage(w, http.StatusConflict, "last_admin_key", err.Error())
	default:
		a.writeInternalError(w, r, err)
	}
}
