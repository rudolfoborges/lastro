package httpapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/oauth"
)

type authenticatedHandler func(w http.ResponseWriter, r *http.Request, principal accounts.Principal)

func (a *api) authenticated(scope accounts.Scope, next authenticatedHandler) http.HandlerFunc {
	return a.guard(scope, false, next)
}

func (a *api) guard(scope accounts.Scope, acceptOAuth bool, next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client := clientIP(r, a.trustProxyHeaders)
		if a.clientLimits.exhausted(client) {
			writeRateLimited(w, a.clientLimits)
			return
		}
		token, ok := bearerToken(r)
		if !ok {
			a.rejectUnauthenticated(w, client, acceptOAuth)
			return
		}
		principal, err := a.authenticate(r, token, acceptOAuth)
		if errors.Is(err, accounts.ErrUnauthenticated) || errors.Is(err, oauth.ErrUnauthenticated) {
			a.rejectUnauthenticated(w, client, acceptOAuth)
			return
		}
		if err != nil {
			a.writeInternalError(w, r, err)
			return
		}

		started := time.Now()
		recorder := &statusRecorder{ResponseWriter: w}
		defer a.audit(r, principal, recorder, started)

		switch {
		case !a.keyLimits.allow(principal.CredentialID().String()):
			writeRateLimited(recorder, a.keyLimits)
		case scope != "" && !principal.Has(scope):
			writeError(recorder, http.StatusForbidden, "forbidden")
		default:
			next(recorder, r, principal)
		}
	}
}

func (a *api) authenticate(r *http.Request, token string, acceptOAuth bool) (accounts.Principal, error) {
	if strings.HasPrefix(token, oauth.AccessTokenPrefix) {
		if !acceptOAuth || a.oauth == nil {
			return accounts.Principal{}, accounts.ErrUnauthenticated
		}
		return a.oauth.Authenticate(r.Context(), token)
	}
	return a.accounts.Authenticate(r.Context(), token)
}

func (a *api) rejectUnauthenticated(w http.ResponseWriter, client string, acceptOAuth bool) {
	a.clientLimits.allow(client)
	challenge := `Bearer realm="lastro"`
	if acceptOAuth && a.oauth != nil {
		challenge += fmt.Sprintf(`, resource_metadata=%q`, a.oauth.ResourceMetadataURL())
	}
	w.Header().Set("WWW-Authenticate", challenge)
	writeError(w, http.StatusUnauthorized, "unauthorized")
}

func (a *api) limitedByClient(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.clientLimits.allow(clientIP(r, a.trustProxyHeaders)) {
			writeRateLimited(w, a.clientLimits)
			return
		}
		next(w, r)
	}
}

func (a *api) audit(r *http.Request, principal accounts.Principal, recorder *statusRecorder, started time.Time) {
	status := recorder.status
	if status == 0 {
		status = http.StatusOK
	}
	err := a.accounts.RecordRequest(context.WithoutCancel(r.Context()), principal, accounts.RequestRecord{
		RequestID: RequestID(r.Context()),
		Method:    r.Method,
		Route:     r.Pattern,
		Status:    status,
		Duration:  time.Since(started),
	})
	if err != nil {
		a.logger.ErrorContext(r.Context(), "audit record failed",
			slog.String("request_id", RequestID(r.Context())),
			slog.String("error", err.Error()),
		)
	}
}

func bearerToken(r *http.Request) (string, bool) {
	scheme, token, ok := strings.Cut(r.Header.Get("Authorization"), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	return token, token != ""
}
