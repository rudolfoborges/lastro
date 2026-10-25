package httpapi

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/export"
)

func (a *api) handleExportDownload(w http.ResponseWriter, r *http.Request) {
	accountID, err := a.exports.Resolve(r.Context(), r.PathValue("token"))
	if errors.Is(err, export.ErrLinkNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		a.logger.ErrorContext(r.Context(), "export link failed", slog.String("request_id", RequestID(r.Context())), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	now := time.Now()
	doc, err := a.exports.Dump(r.Context(), accountID, now)
	if err != nil {
		a.logger.ErrorContext(r.Context(), "export failed", slog.String("request_id", RequestID(r.Context())), slog.String("error", err.Error()))
		writeError(w, http.StatusInternalServerError, "internal_error")
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="lastro-export-%s.zip"`, now.UTC().Format("2006-01-02")))
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	if err := a.exports.Write(r.Context(), w, doc); err != nil {
		a.logger.ErrorContext(r.Context(), "export write failed", slog.String("request_id", RequestID(r.Context())), slog.String("error", err.Error()))
	}
}
