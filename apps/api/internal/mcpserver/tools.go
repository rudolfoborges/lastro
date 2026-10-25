package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/evaluation"
	"github.com/rudolfoborges/lastro/apps/api/internal/idempotency"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/money"
)

type Outcome string

const (
	OutcomeCompleted Outcome = "completed"
	OutcomePending   Outcome = "pending"
	OutcomeFailed    Outcome = "failed"
)

const openAISessionMeta = "openai/session"

type toolSpec struct {
	name        string
	title       string
	description string
	scope       accounts.Scope
	readOnly    bool
	idempotent  bool
	destructive bool
	enums       map[string][]any
}

type call struct {
	principal    accounts.Principal
	conversation string
	receivedAt   time.Time
}

func (c call) actor(author ledger.Author, requestID string) ledger.Actor {
	return ledger.Actor{
		Author:    author,
		Ref:       string(c.principal.CredentialKind()) + ":" + c.principal.CredentialID().String(),
		RequestID: requestID,
	}
}

type outcomeReporter interface {
	reportedOutcome() Outcome
}

type ToolError struct {
	Outcome Outcome `json:"outcome"`
	Code    string  `json:"error"`
	Field   string  `json:"field,omitempty"`
	Message string  `json:"message"`
}

func (e *ToolError) Error() string {
	encoded, err := json.Marshal(e)
	if err != nil {
		return e.Message
	}
	return string(encoded)
}

func invalid(field, message string) *ToolError {
	return &ToolError{Outcome: OutcomeFailed, Code: "invalid_request", Field: field, Message: message}
}

func addTool[In, Out any](s *Server, spec toolSpec, run func(ctx context.Context, c call, in In) (Out, error)) {
	input, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("input schema of %s: %v", spec.name, err))
	}
	for property, values := range spec.enums {
		schema := propertySchema(input, property)
		if schema == nil {
			panic(fmt.Sprintf("tool %s has no property %s to restrict", spec.name, property))
		}
		schema.Enum = values
	}
	destructive := spec.destructive
	tool := &mcp.Tool{
		Name:        spec.name,
		Title:       spec.title,
		Description: spec.description,
		InputSchema: input,
		Annotations: &mcp.ToolAnnotations{
			Title:           spec.title,
			ReadOnlyHint:    spec.readOnly,
			IdempotentHint:  spec.idempotent,
			DestructiveHint: &destructive,
			OpenWorldHint:   new(bool),
		},
	}
	mcp.AddTool(s.mcp, tool, func(ctx context.Context, req *mcp.CallToolRequest, in In) (*mcp.CallToolResult, Out, error) {
		var zero Out
		started := time.Now()
		principal, ok := s.principal(req)
		if !ok {
			return nil, zero, &ToolError{Outcome: OutcomeFailed, Code: "unauthorized", Message: "the call carries no valid credential"}
		}
		if s.deps.Evaluations != nil && !slices.Contains(evaluationTools, spec.name) {
			routed, err := s.deps.Evaluations.Route(ctx, principal, s.now())
			if err != nil {
				return nil, zero, s.toToolError(ctx, spec.name, err)
			}
			principal = routed
		}
		c := call{principal: principal, conversation: conversationOf(req), receivedAt: s.now()}
		var out Out
		var toolErr *ToolError
		if spec.scope != "" && !principal.Has(spec.scope) {
			toolErr = &ToolError{Outcome: OutcomeFailed, Code: "forbidden", Message: fmt.Sprintf("this credential lacks the %s scope", spec.scope)}
		} else if out, err = run(ctx, c, in); err != nil {
			toolErr = s.toToolError(ctx, spec.name, err)
		}
		s.audit(ctx, principal, spec.name, out, toolErr, time.Since(started))
		if toolErr != nil {
			return nil, zero, toolErr
		}
		return nil, out, nil
	})
}

// propertySchema resolves "items.field" paths to a property of an array's items.
func propertySchema(schema *jsonschema.Schema, path string) *jsonschema.Schema {
	name, rest, nested := strings.Cut(path, ".")
	property, ok := schema.Properties[name]
	if !ok {
		return nil
	}
	if !nested {
		return property
	}
	if property.Items == nil {
		return nil
	}
	return propertySchema(property.Items, rest)
}

func (s *Server) audit(ctx context.Context, principal accounts.Principal, tool string, out any, toolErr *ToolError, duration time.Duration) {
	record := accounts.ToolCallRecord{Tool: tool, Outcome: string(OutcomeCompleted), Duration: duration}
	if reporter, ok := out.(outcomeReporter); ok {
		record.Outcome = string(reporter.reportedOutcome())
	}
	if toolErr != nil {
		record.Outcome, record.ErrorCode = string(OutcomeFailed), toolErr.Code
	}
	if err := s.deps.Accounts.RecordToolCall(context.WithoutCancel(ctx), principal, record); err != nil {
		s.deps.Logger.ErrorContext(ctx, "tool call audit failed", slog.String("tool", tool), slog.String("error", err.Error()))
	}
}

func (s *Server) toToolError(ctx context.Context, tool string, err error) *ToolError {
	var toolErr *ToolError
	var validation *ledger.ValidationError
	var parse *money.ParseError
	switch {
	case errors.As(err, &toolErr):
		return toolErr
	case errors.As(err, &validation):
		return invalid(validation.Field, validation.Error())
	case errors.As(err, &parse):
		return invalid("amount", parse.Error())
	case errors.Is(err, money.ErrUnsupportedCurrency):
		return invalid("currency", err.Error())
	case errors.Is(err, ledger.ErrNotFound):
		return &ToolError{Outcome: OutcomeFailed, Code: "not_found", Message: "no record with this ID exists in Rudolfo's account"}
	case errors.Is(err, idempotency.ErrConflict):
		return &ToolError{Outcome: OutcomeFailed, Code: "request_id_conflict", Field: "request_id", Message: "this request_id was already used for different content; generate a new request_id for a different expense"}
	case errors.Is(err, idempotency.ErrInvalidKey):
		return invalid("request_id", err.Error())
	case errors.Is(err, ledger.ErrTransactionClosed):
		return &ToolError{Outcome: OutcomeFailed, Code: "transaction_closed", Message: err.Error()}
	case errors.Is(err, ledger.ErrClarificationClosed):
		return &ToolError{Outcome: OutcomeFailed, Code: "clarification_closed", Message: err.Error()}
	case errors.Is(err, ledger.ErrObservationClaimed):
		return &ToolError{Outcome: OutcomeFailed, Code: "evidence_conflict", Message: err.Error()}
	case errors.Is(err, evaluation.ErrExpired):
		return &ToolError{Outcome: OutcomeFailed, Code: "evaluation_expired", Message: err.Error()}
	default:
		s.deps.Logger.ErrorContext(ctx, "tool failed", slog.String("tool", tool), slog.String("error", err.Error()))
		return &ToolError{Outcome: OutcomeFailed, Code: "internal_error", Message: "Lastro failed and nothing was recorded; retry the same call"}
	}
}

func conversationOf(req *mcp.CallToolRequest) string {
	if req == nil || req.Params == nil {
		return ""
	}
	value, _ := req.Params.Meta[openAISessionMeta].(string)
	return strings.TrimSpace(value)
}
