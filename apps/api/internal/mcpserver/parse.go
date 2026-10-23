package mcpserver

import (
	"strings"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
)

func parseID(field, value string) (uuid.UUID, error) {
	id, err := uuid.Parse(strings.TrimSpace(value))
	if err != nil {
		return uuid.Nil, invalid(field, field+" must be an ID returned by Lastro")
	}
	return id, nil
}

func parseOptionalDate(field, value string) (*calendar.Date, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	date, err := calendar.ParseDate(strings.TrimSpace(value))
	if err != nil {
		return nil, invalid(field, err.Error())
	}
	return &date, nil
}

func optionalString(value string) *string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return &trimmed
}

func enumValues[T ~string](values []T) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}
