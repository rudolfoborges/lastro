package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
)

type authenticatedHandler func(w http.ResponseWriter, r *http.Request, principal accounts.Principal)

func (a *api) authenticated(scope accounts.Scope, next authenticatedHandler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		client := clientIP(r, a.trustProxyHeaders)
		if a.clientLimits.exhausted(client) {
			writeRateLimited(w, a.clientLimits)
			return
		}
		token, ok := bearerToken(r)
		if !ok {
			a.rejectUnauthenticated(w, client)
			return
		}
		principal, err := a.accounts.Authenticate(r.Context(), token)
		if errors.Is(err, accounts.ErrUnauthenticated) {
			a.rejectUnauthenticated(w, client)
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
		case !a.keyLimits.allow(principal.KeyID.String()):
			writeRateLimited(recorder, a.keyLimits)
		case scope != "" && !principal.Has(scope):
			writeError(recorder, http.StatusForbidden, "forbidden")
		default:
			next(recorder, r, principal)
		}
	}
}

func (a *api) rejectUnauthenticated(w http.ResponseWriter, client string) {
	a.clientLimits.allow(client)
	w.Header().Set("WWW-Authenticate", `Bearer realm="lastro"`)
	writeError(w, http.StatusUnauthorized, "unauthorized")
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
