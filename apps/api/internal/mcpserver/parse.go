package mcpserver

import (
	"strings"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/calendar"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

type CategoryPart struct {
	Category string `json:"category" jsonschema:"category code from list_categories"`
	Amount   string `json:"amount,omitempty" jsonschema:"this part of the amount as a canonical decimal such as 30.00; omit when there is a single category"`
}

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

func splitFrom(parts []CategoryPart) []ledger.Split {
	if parts == nil {
		return nil
	}
	split := make([]ledger.Split, len(parts))
	for i, part := range parts {
		split[i] = ledger.Split{Code: strings.TrimSpace(part.Category), AmountText: strings.TrimSpace(part.Amount)}
	}
	return split
}

func enumValues[T ~string](values []T) []any {
	out := make([]any, len(values))
	for i, v := range values {
		out[i] = string(v)
	}
	return out
}
