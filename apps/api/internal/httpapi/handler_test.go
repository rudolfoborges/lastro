package httpapi_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/httpapi"
)

type fakeDatabase struct{ err error }

func (f fakeDatabase) Ping(context.Context) error { return f.err }

func newHandler(database httpapi.Pinger, logs io.Writer) http.Handler {
	return httpapi.NewHandler(httpapi.Deps{
		Logger:   slog.New(slog.NewJSONHandler(logs, nil)),
		Database: database,
	})
}

func get(t *testing.T, handler http.Handler, path string, header http.Header) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, nil)
	for key, values := range header {
		for _, value := range values {
			req.Header.Add(key, value)
		}
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestHealthzReportsLivenessWithoutTouchingTheDatabase(t *testing.T) {
	handler := newHandler(fakeDatabase{err: errors.New("down")}, io.Discard)

	rec := get(t, handler, "/healthz", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ok"}` {
		t.Errorf("body = %s", got)
	}
}

func TestReadyzReportsReadyWhenTheDatabaseAnswers(t *testing.T) {
	rec := get(t, newHandler(fakeDatabase{}, io.Discard), "/readyz", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := strings.TrimSpace(rec.Body.String()); got != `{"status":"ready"}` {
		t.Errorf("body = %s", got)
	}
}

func TestReadyzReportsUnavailableWithoutLeakingTheCause(t *testing.T) {
	rec := get(t, newHandler(fakeDatabase{err: errors.New("password authentication failed")}, io.Discard), "/readyz", nil)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "password") {
		t.Errorf("body leaks the database error: %s", rec.Body.String())
	}
}

func TestRequestIDIsGeneratedWhenMissing(t *testing.T) {
	rec := get(t, newHandler(fakeDatabase{}, io.Discard), "/healthz", nil)

	if rec.Header().Get(httpapi.RequestIDHeader) == "" {
		t.Error("response has no request ID")
	}
}

func TestRequestIDIsPropagatedWhenValid(t *testing.T) {
	header := http.Header{httpapi.RequestIDHeader: {"case-123"}}

	rec := get(t, newHandler(fakeDatabase{}, io.Discard), "/healthz", header)

	if got := rec.Header().Get(httpapi.RequestIDHeader); got != "case-123" {
		t.Errorf("request ID = %q, want case-123", got)
	}
}

func TestRequestIDIsReplacedWhenUnsafe(t *testing.T) {
	header := http.Header{httpapi.RequestIDHeader: {"bad id\nwith newline"}}

	rec := get(t, newHandler(fakeDatabase{}, io.Discard), "/healthz", header)

	if got := rec.Header().Get(httpapi.RequestIDHeader); got == "" || strings.ContainsAny(got, " \n") {
		t.Errorf("request ID = %q, want a generated safe ID", got)
	}
}

func TestAccessLogRecordsRouteAndStatusWithoutHeaders(t *testing.T) {
	var logs bytes.Buffer
	header := http.Header{"Authorization": {"Bearer secret-token"}}

	get(t, newHandler(fakeDatabase{}, &logs), "/healthz", header)

	line := logs.String()
	for _, fragment := range []string{`"route":"GET /healthz"`, `"status":200`, `"request_id":`} {
		if !strings.Contains(line, fragment) {
			t.Errorf("access log %s does not contain %s", line, fragment)
		}
	}
	if strings.Contains(line, "secret-token") {
		t.Errorf("access log leaks the Authorization header: %s", line)
	}
}

func TestUnknownRouteReturnsNotFound(t *testing.T) {
	rec := get(t, newHandler(fakeDatabase{}, io.Discard), "/nope", nil)

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
