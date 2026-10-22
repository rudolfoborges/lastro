package mcpserver

import (
	"context"
	"fmt"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

type ListClarificationsInput struct {
	Kind   string `json:"kind,omitempty" jsonschema:"only clarifications of this kind; category_unknown answers which expenses have no category"`
	Status string `json:"status,omitempty" jsonschema:"open (default), resolved or dismissed"`
	From   string `json:"from,omitempty" jsonschema:"first purchase date to include, YYYY-MM-DD"`
	To     string `json:"to,omitempty" jsonschema:"last purchase date to include, YYYY-MM-DD"`
	Limit  int    `json:"limit,omitempty" jsonschema:"maximum number of clarifications, up to 200 (default 50)"`
}

type ListClarificationsOutput struct {
	Clarifications []ClarificationOutput `json:"clarifications"`
	Count          int                   `json:"count"`
	Summary        string                `json:"summary"`
}

type ResolveClarificationInput struct {
	ClarificationID string         `json:"clarification_id" jsonschema:"ID from list_clarifications"`
	RequestID       string         `json:"request_id" jsonschema:"generated once for this answer and reused on every retry"`
	Action          string         `json:"action" jsonschema:"resolve applies Rudolfo's answer; dismiss stops asking without changing the transaction"`
	Note            string         `json:"note" jsonschema:"Rudolfo's answer in Portuguese, kept in the history"`
	Purpose         string         `json:"purpose,omitempty" jsonschema:"the purpose Rudolfo gave, for purpose_unknown"`
	Categories      []CategoryPart `json:"categories,omitempty" jsonschema:"the category or exact split Rudolfo gave, for category_unknown; it becomes confirmed"`
}

type ResolveClarificationOutput struct {
	Outcome       Outcome             `json:"outcome"`
	Result        string              `json:"result" jsonschema:"resolved or dismissed"`
	Clarification ClarificationOutput `json:"clarification"`
	Transaction   *TransactionOutput  `json:"transaction,omitempty" jsonschema:"the transaction after the answer was applied"`
	Summary       string              `json:"summary"`
}

func (o ResolveClarificationOutput) reportedOutcome() Outcome {
	return o.Outcome
}

func (s *Server) registerClarifications() {
	addTool(s, toolSpec{
		name:        "list_clarifications",
		title:       "List pending questions",
		description: "Lists the questions Lastro has for Rudolfo, such as expenses without category, Pix without purpose and conflicts with what he confirmed, each with description, date and amount. Filter by kind, status and purchase period.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
		enums: map[string][]any{
			"kind":   enumValues(ledger.ClarificationKinds),
			"status": {"open", "resolved", "dismissed"},
		},
	}, func(ctx context.Context, c call, in ListClarificationsInput) (ListClarificationsOutput, error) {
		from, err := parseOptionalDate("from", in.From)
		if err != nil {
			return ListClarificationsOutput{}, err
		}
		to, err := parseOptionalDate("to", in.To)
		if err != nil {
			return ListClarificationsOutput{}, err
		}
		filter := ledger.ClarificationFilter{Status: ledger.ClarificationStatus(in.Status), From: from, To: to, Limit: in.Limit}
		if in.Kind != "" {
			filter.Kinds = []ledger.ClarificationKind{ledger.ClarificationKind(in.Kind)}
		}
		clarifications, err := s.deps.Ledger.Clarifications(ctx, c.principal.AccountID, filter)
		if err != nil {
			return ListClarificationsOutput{}, err
		}
		out := ListClarificationsOutput{Clarifications: []ClarificationOutput{}, Count: len(clarifications)}
		for _, clarification := range clarifications {
			out.Clarifications = append(out.Clarifications, clarificationOutput(clarification))
		}
		out.Summary = clarificationsSummary(len(clarifications), filter.Status)
		return out, nil
	})

	addTool(s, toolSpec{
		name:        "resolve_clarification",
		title:       "Answer a pending question",
		description: "Records Rudolfo's answer to a clarification. resolve applies it (a purpose or a category becomes confirmed) and closes the question; dismiss closes it without changing anything, and the same question is not asked again. Every answer is kept in the transaction history.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
		enums:       map[string][]any{"action": {"resolve", "dismiss"}},
	}, func(ctx context.Context, c call, in ResolveClarificationInput) (ResolveClarificationOutput, error) {
		id, err := parseID("clarification_id", in.ClarificationID)
		if err != nil {
			return ResolveClarificationOutput{}, err
		}
		resolved, err := s.deps.Ledger.ResolveClarification(ctx, c.principal.AccountID, id, ledger.Resolution{
			Action:     ledger.ResolutionAction(in.Action),
			Note:       in.Note,
			Purpose:    optionalString(in.Purpose),
			Categories: splitFrom(in.Categories),
		}, c.actor(ledger.AuthorUser, in.RequestID))
		if err != nil {
			return ResolveClarificationOutput{}, err
		}
		out := ResolveClarificationOutput{
			Outcome:       OutcomeCompleted,
			Result:        string(resolved.Clarification.Status),
			Clarification: clarificationOutput(resolved.Clarification),
			Summary:       "Dúvida resolvida.",
		}
		if resolved.Clarification.Status == ledger.ClarificationDismissed {
			out.Summary = "Dúvida dispensada; não será perguntada de novo."
		}
		if resolved.Correction != nil {
			transaction := transactionOutput(resolved.Correction.Transaction, resolved.Correction.Categories)
			out.Transaction = &transaction
		}
		return out, nil
	})
}

func clarificationsSummary(count int, status ledger.ClarificationStatus) string {
	if status == "" {
		status = ledger.ClarificationOpen
	}
	label := map[ledger.ClarificationStatus][2]string{
		ledger.ClarificationOpen:      {"aberta", "abertas"},
		ledger.ClarificationResolved:  {"resolvida", "resolvidas"},
		ledger.ClarificationDismissed: {"dispensada", "dispensadas"},
	}[status]
	switch count {
	case 0:
		return fmt.Sprintf("Nenhuma dúvida %s.", label[0])
	case 1:
		return fmt.Sprintf("1 dúvida %s.", label[0])
	default:
		return fmt.Sprintf("%d dúvidas %s.", count, label[1])
	}
}
