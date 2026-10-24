package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

type FindMatchCandidatesInput struct {
	TransactionID string `json:"transaction_id,omitempty" jsonschema:"transaction to look for in the other sources"`
	ObservationID string `json:"observation_id,omitempty" jsonschema:"or one of its evidence: a notification observation_id or a statement line observation"`
}

type FindMatchCandidatesOutput struct {
	Transaction TransactionOutput      `json:"transaction"`
	Sources     []string               `json:"sources" jsonschema:"where this transaction already has evidence from"`
	Candidates  []MatchCandidateOutput `json:"candidates" jsonschema:"open transactions from other sources that may be the same purchase, with reasons and differences"`
	Summary     string                 `json:"summary"`
}

type MatchDecisionInput struct {
	TransactionID      string `json:"transaction_id"`
	OtherTransactionID string `json:"other_transaction_id" jsonschema:"the other record, usually a candidate from possible_match or find_match_candidates"`
	RequestID          string `json:"request_id" jsonschema:"generated once for this decision and reused on every retry"`
	Reason             string `json:"reason" jsonschema:"why, in Portuguese: what matched, or what Rudolfo said; kept in the history"`
}

type ConfirmMatchOutput struct {
	Outcome     Outcome           `json:"outcome"`
	Transaction TransactionOutput `json:"transaction" jsonschema:"the transaction that holds every record now"`
	Merged      TransactionOutput `json:"merged" jsonschema:"the other one, kept with status merged; it no longer counts"`
	Changed     []string          `json:"changed,omitempty" jsonschema:"fields of the kept transaction filled from the merged one"`
	Conflicts   []string          `json:"conflicts,omitempty" jsonschema:"fields Rudolfo confirmed differently on each side; a clarification was opened"`
	Summary     string            `json:"summary"`
}

func (o ConfirmMatchOutput) reportedOutcome() Outcome {
	return o.Outcome
}

type RejectMatchOutput struct {
	Outcome      Outcome             `json:"outcome"`
	Transactions []TransactionOutput `json:"transactions"`
	Summary      string              `json:"summary"`
}

func (o RejectMatchOutput) reportedOutcome() Outcome {
	return o.Outcome
}

type UndoMatchInput struct {
	TransactionID string `json:"transaction_id" jsonschema:"the transaction that holds the record now"`
	ObservationID string `json:"observation_id" jsonschema:"the record linked by mistake, from get_transaction evidence"`
	RequestID     string `json:"request_id" jsonschema:"generated once and reused on every retry"`
	Reason        string `json:"reason" jsonschema:"why the link was wrong, in Portuguese"`
}

type UndoMatchOutput struct {
	Outcome      Outcome             `json:"outcome"`
	Effect       string              `json:"effect" jsonschema:"unmerged (both merged transactions are back), separated (the record has its own transaction again) or detached (the record no longer confirms the transaction and was not a purchase of its own)"`
	Transactions []TransactionOutput `json:"transactions"`
	Summary      string              `json:"summary"`
}

func (o UndoMatchOutput) reportedOutcome() Outcome {
	return o.Outcome
}

func matchDecision(in MatchDecisionInput) (ledger.MatchDecision, error) {
	id, err := parseID("transaction_id", in.TransactionID)
	if err != nil {
		return ledger.MatchDecision{}, err
	}
	other, err := parseID("other_transaction_id", in.OtherTransactionID)
	if err != nil {
		return ledger.MatchDecision{}, err
	}
	return ledger.MatchDecision{TransactionID: id, OtherTransactionID: other, Reason: in.Reason}, nil
}

func (s *Server) registerMatching() {
	addTool(s, toolSpec{
		name:  "find_match_candidates",
		title: "Find records of the same purchase in other sources",
		description: "Looks for open transactions from other sources (conversation, Google Wallet, statement) that may be the same purchase as a transaction: same amount (or the same original amount for international purchases), compatible payment method and close dates. " +
			"Each candidate says what matches and what differs. Lastro never links them by itself: you decide, asking Rudolfo when in doubt. Read get_instructions topic matching first.",
		scope:      accounts.ScopeExpensesRead,
		readOnly:   true,
		idempotent: true,
	}, func(ctx context.Context, c call, in FindMatchCandidatesInput) (FindMatchCandidatesOutput, error) {
		var ref ledger.MatchRef
		if strings.TrimSpace(in.TransactionID) != "" {
			id, err := parseID("transaction_id", in.TransactionID)
			if err != nil {
				return FindMatchCandidatesOutput{}, err
			}
			ref.TransactionID = &id
		}
		if strings.TrimSpace(in.ObservationID) != "" {
			id, err := parseID("observation_id", in.ObservationID)
			if err != nil {
				return FindMatchCandidatesOutput{}, err
			}
			ref.ObservationID = &id
		}
		search, err := s.deps.Ledger.FindMatches(ctx, c.principal.AccountID, ref)
		if err != nil {
			return FindMatchCandidatesOutput{}, err
		}
		out := FindMatchCandidatesOutput{
			Transaction: transactionOutput(search.Transaction, &search.Category),
			Sources:     stringsOf(search.Sources),
			Candidates:  candidateOutputs(search.Candidates),
		}
		if out.Candidates == nil {
			out.Candidates = []MatchCandidateOutput{}
		}
		switch len(out.Candidates) {
		case 0:
			out.Summary = fmt.Sprintf("Nenhum lançamento de outra fonte parece ser \"%s\".", search.Transaction.Description)
		default:
			out.Summary = fmt.Sprintf("%d lançamento(s) de outras fontes podem ser \"%s\". Compare os motivos e as diferenças antes de decidir.", len(out.Candidates), search.Transaction.Description)
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:  "confirm_match",
		title: "Confirm that two records are the same purchase",
		description: "Merges two open transactions that are the same purchase, after you or Rudolfo decided it: every record moves to one of them (the one confirmed by a statement or a notification, else the oldest) and the other is kept as merged, never deleted. " +
			"Missing fields are filled from the merged one and Rudolfo's confirmed values are kept. Lastro refuses records of the same source, of different natures or currencies. Undo with undo_match.",
		scope:      accounts.ScopeExpensesWrite,
		idempotent: true,
	}, func(ctx context.Context, c call, in MatchDecisionInput) (ConfirmMatchOutput, error) {
		decision, err := matchDecision(in)
		if err != nil {
			return ConfirmMatchOutput{}, err
		}
		outcome, err := s.deps.Ledger.ConfirmMatch(ctx, c.principal.AccountID, decision, c.actor(ledger.AuthorAgent, in.RequestID))
		if err != nil {
			return ConfirmMatchOutput{}, err
		}
		out := ConfirmMatchOutput{
			Outcome:     OutcomeCompleted,
			Transaction: transactionOutput(outcome.Survivor, &outcome.Category),
			Merged:      transactionOutput(outcome.Merged, nil),
			Changed:     outcome.Changed,
			Summary:     fmt.Sprintf("Juntei os registros da mesma compra em %s em \"%s\"; o outro registro foi mantido como fundido e não conta mais.", outcome.Survivor.Amount.Format(), outcome.Survivor.Description),
		}
		for _, conflict := range outcome.Conflicts {
			out.Conflicts = append(out.Conflicts, string(conflict.Field))
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:        "reject_match",
		title:       "Record that two records are different purchases",
		description: "Records that two transactions are different purchases, so they are never offered as candidates of each other again, and closes the possible_match questions this settles. Nothing else changes.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
	}, func(ctx context.Context, c call, in MatchDecisionInput) (RejectMatchOutput, error) {
		decision, err := matchDecision(in)
		if err != nil {
			return RejectMatchOutput{}, err
		}
		outcome, err := s.deps.Ledger.RejectMatch(ctx, c.principal.AccountID, decision, c.actor(ledger.AuthorAgent, in.RequestID))
		if err != nil {
			return RejectMatchOutput{}, err
		}
		out := RejectMatchOutput{Outcome: OutcomeCompleted, Summary: "Registrei que são compras diferentes; elas não serão mais sugeridas como a mesma compra."}
		for _, transaction := range outcome.Transactions {
			out.Transactions = append(out.Transactions, transactionOutput(transaction, nil))
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:  "undo_match",
		title: "Undo a wrong link between records",
		description: "Separates a record that was linked to a transaction by mistake, by a merge or when it was submitted. Nothing is lost: a merge brings both transactions back as they were, and a record linked at submission gets its own transaction again from its original data. " +
			"Fields Rudolfo confirmed are kept, and the pair is never offered as a match again.",
		scope:      accounts.ScopeExpensesWrite,
		idempotent: true,
	}, func(ctx context.Context, c call, in UndoMatchInput) (UndoMatchOutput, error) {
		id, err := parseID("transaction_id", in.TransactionID)
		if err != nil {
			return UndoMatchOutput{}, err
		}
		observation, err := parseID("observation_id", in.ObservationID)
		if err != nil {
			return UndoMatchOutput{}, err
		}
		outcome, err := s.deps.Ledger.UndoMatch(ctx, c.principal.AccountID, ledger.UndoRequest{TransactionID: id, ObservationID: observation, Reason: in.Reason}, c.actor(ledger.AuthorAgent, in.RequestID))
		if err != nil {
			return UndoMatchOutput{}, err
		}
		out := UndoMatchOutput{Outcome: OutcomeCompleted, Effect: string(outcome.Effect)}
		descriptions := make([]string, len(outcome.Transactions))
		for i, transaction := range outcome.Transactions {
			out.Transactions = append(out.Transactions, transactionOutput(transaction, nil))
			descriptions[i] = fmt.Sprintf("%s em \"%s\"", transaction.Amount.Format(), transaction.Description)
		}
		switch outcome.Effect {
		case ledger.UndoDetached:
			out.Summary = fmt.Sprintf("O registro deixou de confirmar %s.", descriptions[0])
		default:
			out.Summary = fmt.Sprintf("Separei os registros: %s.", joinPortuguese(descriptions))
		}
		return out, nil
	})
}
