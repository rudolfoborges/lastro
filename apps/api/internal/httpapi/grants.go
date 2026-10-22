package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/oauth"
)

type grantJSON struct {
	ID         uuid.UUID        `json:"id"`
	ClientID   string           `json:"client_id,omitempty"`
	ClientName *string          `json:"client_name,omitempty"`
	Scopes     []accounts.Scope `json:"scopes"`
	CreatedAt  time.Time        `json:"created_at"`
	LastUsedAt *time.Time       `json:"last_used_at"`
	RevokedAt  *time.Time       `json:"revoked_at"`
}

func toGrantJSON(g oauth.Grant) grantJSON {
	return grantJSON{
		ID: g.ID, ClientID: g.ClientID, ClientName: g.ClientName, Scopes: g.Scopes,
		CreatedAt: g.CreatedAt.UTC(), LastUsedAt: utc(g.LastUsedAt), RevokedAt: utc(g.RevokedAt),
	}
}

func (a *api) handleListGrants(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	grants, err := a.oauth.Grants(r.Context(), principal.AccountID)
	if err != nil {
		a.writeInternalError(w, r, err)
		return
	}
	body := make([]grantJSON, 0, len(grants))
	for _, grant := range grants {
		body = append(body, toGrantJSON(grant))
	}
	writeJSON(w, http.StatusOK, map[string]any{"oauth_grants": body})
}

func (a *api) handleRevokeGrant(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	grant, err := a.oauth.RevokeGrant(r.Context(), principal.AccountID, id)
	if errors.Is(err, oauth.ErrGrantNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"oauth_grant": toGrantJSON(grant)})
}
