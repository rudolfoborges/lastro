package httpapi_test

import (
	"archive/zip"
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
)

func TestAnExportLinkDownloadsTheZipWithoutLeakingTheToken(t *testing.T) {
	a := newTestAPI(t, config.RateLimits{PerKeyPerMinute: 100, UnauthenticatedPerMinute: 100})
	accountID, _ := a.signUp("Rudolfo")
	token, _, err := a.exports.NewLink(t.Context(), uuid.MustParse(accountID), time.Now())
	if err != nil {
		t.Fatal(err)
	}

	rec := httptest.NewRecorder()
	a.handler.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/exports/"+token, nil))
	unknown := httptest.NewRecorder()
	a.handler.ServeHTTP(unknown, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/v1/exports/not-a-token", nil))

	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/zip" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("download = %d %v", rec.Code, rec.Header())
	}
	archive, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, file := range archive.File {
		names[file.Name] = true
	}
	if !names["lastro-export.json"] || !names["transactions.csv"] || !names["statements.csv"] {
		t.Errorf("zip files = %v", names)
	}
	if unknown.Code != http.StatusNotFound {
		t.Errorf("unknown token = %d, want 404", unknown.Code)
	}
	if strings.Contains(a.logs.String(), token) {
		t.Error("the export token reached the logs")
	}
}
