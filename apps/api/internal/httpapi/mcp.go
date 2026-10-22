package httpapi

import (
	"context"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/auth"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

const maxMCPBodyBytes = 1 << 20

type principalKey struct{}

func (a *api) mcpEndpoint(next http.Handler) http.HandlerFunc {
	withTokenInfo := auth.RequireBearerToken(func(ctx context.Context, _ string, _ *http.Request) (*auth.TokenInfo, error) {
		principal, ok := ctx.Value(principalKey{}).(accounts.Principal)
		if !ok {
			return nil, auth.ErrInvalidToken
		}
		return mcpserver.TokenInfo(principal), nil
	}, nil)(http.MaxBytesHandler(next, maxMCPBodyBytes))

	return a.guard("", true, func(w http.ResponseWriter, r *http.Request, principal accounts.Principal) {
		withTokenInfo.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey{}, principal)))
	})
}
