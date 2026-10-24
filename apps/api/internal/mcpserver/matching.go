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
}
