package redaction

import (
	"context"
	"log/slog"
)

type handler struct {
	next slog.Handler
}

func NewHandler(next slog.Handler) slog.Handler {
	return handler{next: next}
}

func (h handler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h handler) Handle(ctx context.Context, record slog.Record) error {
	redacted := slog.NewRecord(record.Time, record.Level, Text(record.Message).Text, record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		redacted.AddAttrs(redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, redacted)
}

func (h handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		redacted[i] = redactAttr(attr)
	}
	return handler{next: h.next.WithAttrs(redacted)}
}

func (h handler) WithGroup(name string) slog.Handler {
	return handler{next: h.next.WithGroup(name)}
}

func redactAttr(attr slog.Attr) slog.Attr {
	value := attr.Value.Resolve()
	switch value.Kind() {
	case slog.KindString:
		return slog.String(attr.Key, Text(value.String()).Text)
	case slog.KindGroup:
		group := value.Group()
		redacted := make([]any, len(group))
		for i, item := range group {
			redacted[i] = redactAttr(item)
		}
		return slog.Group(attr.Key, redacted...)
	case slog.KindAny:
		if err, ok := value.Any().(error); ok {
			return slog.String(attr.Key, Text(err.Error()).Text)
		}
	}
	return attr
}
