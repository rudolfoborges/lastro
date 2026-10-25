package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/evaluation"
)

var evaluationTools = []string{"start_case_evaluation", "finish_case_evaluation", "get_case_evaluation"}

type ConversationMessageOutput struct {
	ID   string `json:"id"`
	Text string `json:"text" jsonschema:"handle it exactly as if Rudolfo had sent it, in this order, in this same conversation"`
}

type StartCaseEvaluationOutput struct {
	Outcome         Outcome                     `json:"outcome"`
	EvaluationID    string                      `json:"evaluation_id"`
	ExpiresAt       string                      `json:"expires_at" jsonschema:"until then, every call of this credential goes to the evaluation sandbox"`
	ScenarioVersion int                         `json:"scenario_version"`
	Conversation    []ConversationMessageOutput `json:"conversation"`
	Statement       string                      `json:"statement" jsonschema:"text of a statement PDF, as you would read it from the file"`
	Summary         string                      `json:"summary"`
}

func (o StartCaseEvaluationOutput) reportedOutcome() Outcome {
	return o.Outcome
}

type FlowResultOutput struct {
	Flow   string `json:"flow"`
	Passed int    `json:"passed"`
	Total  int    `json:"total"`
	Rate   int    `json:"rate" jsonschema:"percent of checks passed"`
}

type CheckResultOutput struct {
	Flow  string `json:"flow"`
	Check string `json:"check"`
}

type EvaluationReportOutput struct {
	EvaluationID    string              `json:"evaluation_id"`
	ScenarioVersion int                 `json:"scenario_version"`
	StartedAt       string              `json:"started_at"`
	FinishedAt      string              `json:"finished_at"`
	Flows           []FlowResultOutput  `json:"flows"`
	Errors          []CheckResultOutput `json:"errors" jsonschema:"checks that failed, in Portuguese"`
	Criterion8      bool                `json:"criterion_8" jsonschema:"convenience snacks kept apart from meals, groceries and fuel"`
}

type FinishCaseEvaluationOutput struct {
	Outcome Outcome                `json:"outcome"`
	Report  EvaluationReportOutput `json:"report"`
	Summary string                 `json:"summary"`
}

func (o FinishCaseEvaluationOutput) reportedOutcome() Outcome {
	return o.Outcome
}

type GetCaseEvaluationOutput struct {
	Reports []EvaluationReportOutput `json:"reports" jsonschema:"latest finished evaluations, newest first"`
	Summary string                   `json:"summary"`
}

func reportOutput(run evaluation.Run) EvaluationReportOutput {
	out := EvaluationReportOutput{EvaluationID: run.ID.String(), ScenarioVersion: run.ScenarioVersion, StartedAt: run.StartedAt.UTC().Format(time.RFC3339), Flows: []FlowResultOutput{}, Errors: []CheckResultOutput{}}
	if run.FinishedAt != nil {
		out.FinishedAt = run.FinishedAt.UTC().Format(time.RFC3339)
	}
	if run.Report == nil {
		return out
	}
	out.Criterion8 = run.Report.Criterion8
	for _, flow := range run.Report.Flows {
		out.Flows = append(out.Flows, FlowResultOutput{Flow: string(flow.Flow), Passed: flow.Passed, Total: flow.Total, Rate: flow.Rate()})
	}
	for _, failed := range run.Report.Errors() {
		out.Errors = append(out.Errors, CheckResultOutput{Flow: string(failed.Flow), Check: failed.Check})
	}
	return out
}

func reportSummary(report EvaluationReportOutput) string {
	parts := make([]string, len(report.Flows))
	for i, flow := range report.Flows {
		parts[i] = fmt.Sprintf("%s %d%% (%d/%d)", flowLabel(flow.Flow), flow.Rate, flow.Passed, flow.Total)
	}
	summary := "Avaliação da Case: " + strings.Join(parts, ", ") + "."
	if len(report.Errors) == 0 {
		return summary + " Nenhum erro."
	}
	errorsList := make([]string, len(report.Errors))
	for i, failed := range report.Errors {
		errorsList[i] = failed.Check
	}
	return summary + " Erros: " + strings.Join(errorsList, "; ") + "."
}

func flowLabel(flow string) string {
	switch flow {
	case string(evaluation.FlowConversation):
		return "conversa"
	case string(evaluation.FlowNotifications):
		return "notificações"
	case string(evaluation.FlowStatements):
		return "faturas"
	default:
		return "classificação"
	}
}

func (s *Server) registerEvaluation() {
	addTool(s, toolSpec{
		name:  "start_case_evaluation",
		title: "Start an evaluation of how you use Lastro",
		description: "Opens an evaluation sandbox for about two hours: until it is finished, every call you make goes to a separate account, never to Rudolfo's records. " +
			"Then handle the returned conversation messages, the Google Wallet inbox and the statement exactly as the procedures say, and call finish_case_evaluation. Read get_instructions topic evaluation first.",
		scope: accounts.ScopeExpensesWrite,
	}, func(ctx context.Context, c call, _ NoInput) (StartCaseEvaluationOutput, error) {
		run, created, err := s.deps.Evaluations.Start(ctx, c.principal, c.receivedAt)
		if err != nil {
			return StartCaseEvaluationOutput{}, err
		}
		out := StartCaseEvaluationOutput{
			Outcome: OutcomeCompleted, EvaluationID: run.ID.String(), ExpiresAt: run.ExpiresAt.UTC().Format(time.RFC3339),
			ScenarioVersion: run.ScenarioVersion, Statement: evaluation.Statement,
		}
		for _, message := range evaluation.Conversation {
			out.Conversation = append(out.Conversation, ConversationMessageOutput{ID: message.ID, Text: message.Text})
		}
		out.Summary = "Avaliação iniciada: até finalizar, tudo o que você registrar vai para a conta de avaliação. Trate as mensagens, a caixa de entrada e a fatura e depois chame finish_case_evaluation."
		if !created {
			out.Summary = "Já havia uma avaliação aberta com esta conexão; continue nela e chame finish_case_evaluation ao terminar."
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:        "finish_case_evaluation",
		title:       "Finish the evaluation and get the report",
		description: "Grades what you recorded in the evaluation sandbox against the expected results, gives the hit rate per flow and the list of errors, and sends your calls back to Rudolfo's account.",
		scope:       accounts.ScopeExpensesWrite,
	}, func(ctx context.Context, c call, _ NoInput) (FinishCaseEvaluationOutput, error) {
		run, err := s.deps.Evaluations.Finish(ctx, c.principal, c.receivedAt)
		if errors.Is(err, evaluation.ErrNoEvaluation) {
			return FinishCaseEvaluationOutput{}, &ToolError{Outcome: OutcomeFailed, Code: "no_evaluation", Message: err.Error()}
		}
		if err != nil {
			return FinishCaseEvaluationOutput{}, err
		}
		report := reportOutput(run)
		return FinishCaseEvaluationOutput{Outcome: OutcomeCompleted, Report: report, Summary: reportSummary(report)}, nil
	})

	addTool(s, toolSpec{
		name:        "get_case_evaluation",
		title:       "Show the latest evaluation reports",
		description: "Returns the latest finished evaluations: hit rate per flow and errors.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
	}, func(ctx context.Context, c call, _ NoInput) (GetCaseEvaluationOutput, error) {
		runs, err := s.deps.Evaluations.Runs(ctx, c.principal.AccountID, 5)
		if err != nil {
			return GetCaseEvaluationOutput{}, err
		}
		out := GetCaseEvaluationOutput{Reports: []EvaluationReportOutput{}}
		for _, run := range runs {
			if run.Report != nil {
				out.Reports = append(out.Reports, reportOutput(run))
			}
		}
		out.Summary = "Nenhuma avaliação concluída ainda."
		if len(out.Reports) > 0 {
			out.Summary = reportSummary(out.Reports[0])
		}
		return out, nil
	})
}
