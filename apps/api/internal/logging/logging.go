package logging

import (
	"io"
	"log/slog"

	"github.com/rudolfoborges/lastro/apps/api/internal/config"
)

func New(w io.Writer, format config.LogFormat, level slog.Level) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	if format == config.LogFormatJSON {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}
