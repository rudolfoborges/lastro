package logging

import (
	"io"
	"log/slog"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/redaction"
)

func New(w io.Writer, format config.LogFormat, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var handler slog.Handler = slog.NewTextHandler(w, opts)
	if format == config.LogFormatJSON {
		handler = slog.NewJSONHandler(w, opts)
	}
	return slog.New(redaction.NewHandler(handler))
}
