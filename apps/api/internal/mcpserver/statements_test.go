package mcpserver_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/mcpserver"
)

const statementSchemaFile = "../../docs/statement-extraction.schema.json"

func statementFixture(t *testing.T) map[string]any {
	t.Helper()
	raw, err := os.ReadFile("testdata/statements/complete.json")
	if err != nil {
		t.Fatal(err)
	}
	var in map[string]any
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func fixtureItem(in map[string]any, index int) map[string]any {
	return in["items"].([]any)[index].(map[string]any)
}

func (e *env) count(table string) int {
	e.t.Helper()
	conn, err := pgx.Connect(e.t.Context(), e.databaseURL)
	if err != nil {
		e.t.Fatal(err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	var count int
	if err := conn.QueryRow(e.t.Context(), "SELECT count(*) FROM "+pgx.Identifier{table}.Sanitize()).Scan(&count); err != nil {
		e.t.Fatal(err)
	}
	return count
}

func TestCaseSubmitsAStatementReadingWithEveryLineAnchored(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write", "statements:write"))

	first := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", statementFixture(t), nil)

	if first.isError || first.out.Outcome != mcpserver.OutcomeCompleted || first.out.Result != "recorded" || len(first.out.Lines) != 12 {
		t.Fatalf("submission = %+v %s", first.out, first.text)
	}
	effects := map[string]int{}
	for i, line := range first.out.Lines {
		effects[line.Effect]++
		if line.Index != i || line.Page == 0 {
			t.Errorf("line %d = %+v", i, line)
		}
	}
	if effects["created"] != 9 || effects["none"] != 3 {
		t.Errorf("effects = %v, want 9 transactions and installments plus the unknown line kept only in the statement", effects)
	}
	if st := first.out.Statement; st == nil || st.Total.Amount != "957.45" || st.PreviousBalance.Amount != "1500.00" || st.Version != 1 {
		t.Errorf("statement = %+v", first.out.Statement)
	}
	if len(first.out.Clarifications) != 2 {
		t.Errorf("clarifications = %+v, want the reading doubt and the unknown line", first.out.Clarifications)
	}
	transactions := e.count("transactions")

	again := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", statementFixture(t), nil)

	if again.out.Result != "already_recorded" || again.out.Statement.ID != first.out.Statement.ID || e.count("transactions") != transactions {
		t.Errorf("resubmission = %+v, want the same statement and no new transactions", again.out)
	}
}

func TestCaseReviewsWhatIsLeftToCheckOnTheLatestStatement(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write", "statements:write"))
	submitted := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", statementFixture(t), nil)

	got := callTool[mcpserver.GetStatementReviewOutput](t, session, "get_statement_review", map[string]any{}, nil)

	review := got.out.Review
	if got.isError || got.out.Statement.ID != submitted.out.Statement.ID || review.Status != "read_incomplete" {
		t.Fatalf("review = %+v %s", got.out, got.text)
	}
	if review.Composition.ComputedTotal.Amount != "957.45" || review.Composition.Difference.Amount != "0.00" || review.Composition.Unknown.Amount.Amount != "-12.34" {
		t.Errorf("composition = %+v", review.Composition)
	}
	kinds := map[string]bool{}
	for _, pending := range review.Pending {
		kinds[pending.Kind] = true
	}
	if !kinds["unknown_lines"] || !kinds["reading_doubts"] || kinds["difference"] {
		t.Errorf("pending = %+v", review.Pending)
	}
	if submitted.out.Review == nil || submitted.out.Review.Status != "read_incomplete" {
		t.Errorf("submission review = %+v", submitted.out.Review)
	}
	listed := callTool[mcpserver.ListClarificationsOutput](t, session, "list_clarifications", map[string]any{"statement_id": submitted.out.Statement.ID}, nil)
	if listed.isError || listed.out.Count != 2 {
		t.Errorf("clarifications of the statement = %+v %s", listed.out, listed.text)
	}
}

func TestCaseLocatesAStatementLineForRudolfo(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write", "statements:write"))
	submitted := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", statementFixture(t), nil)

	byTransaction := callTool[mcpserver.GetStatementLineSourceOutput](t, session, "get_statement_line_source", map[string]any{"transaction_id": *submitted.out.Lines[5].TransactionID}, nil)
	byIndex := callTool[mcpserver.GetStatementLineSourceOutput](t, session, "get_statement_line_source", map[string]any{"statement_id": submitted.out.Statement.ID, "index": 3}, nil)

	if len(byTransaction.out.Sources) != 1 || byTransaction.out.Sources[0].Page != 3 || byTransaction.out.Sources[0].Excerpt != "20/09 EXAMPLE SOFTWARE INC USD 20,00 110,00" {
		t.Errorf("by transaction = %+v %s", byTransaction.out, byTransaction.text)
	}
	if len(byIndex.out.Sources) != 1 || byIndex.out.Sources[0].Kind != "installment" || byIndex.out.Sources[0].Index != 3 {
		t.Errorf("by index = %+v %s", byIndex.out, byIndex.text)
	}
}

func TestADryRunShowsTheReviewAndRecordsNothing(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write", "statements:write"))
	in := statementFixture(t)
	in["dry_run"] = true

	got := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", in, nil)

	if got.isError || got.out.Result != "previewed" || got.out.Review == nil || got.out.Review.Composition.ComputedTotal.Amount != "957.45" {
		t.Fatalf("dry run = %+v %s", got.out, got.text)
	}
	if e.count("statements") != 0 || e.count("transactions") != 0 || e.count("observations") != 0 {
		t.Error("a dry run recorded the statement")
	}
}

func TestADifferentReadingOfTheSameStatementBecomesANewVersion(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write", "statements:write"))
	first := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", statementFixture(t), nil)
	transactions := e.count("transactions")
	changed := statementFixture(t)
	fixtureItem(changed, 4)["amount"] = "210.00"
	changed["total"] = "967.45"

	second := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", changed, nil)

	if second.isError || second.out.Result != "new_version" || second.out.Statement.Version != 2 || second.out.Statement.ID != first.out.Statement.ID {
		t.Fatalf("second reading = %+v %s", second.out, second.text)
	}
	diff := second.out.Diff
	if diff == nil || len(diff.Changed) != 1 || diff.Changed[0].Before.Amount.Amount != "200.00" || diff.Changed[0].After.Amount.Amount != "210.00" || len(diff.Metadata) != 1 {
		t.Errorf("diff = %+v", diff)
	}
	if e.count("transactions") != transactions || *second.out.Lines[4].TransactionID != *first.out.Lines[4].TransactionID {
		t.Error("a new version duplicated transactions")
	}
}

func TestAStatementLineWithoutItsAnchorIsRejectedWithThePathToFix(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write", "statements:write"))
	noPage := statementFixture(t)
	fixtureItem(noPage, 3)["page"] = 0
	badComponents := statementFixture(t)
	fixtureItem(badComponents, 7)["components"].([]any)[1].(map[string]any)["amount"] = "55.00"
	badAmount := statementFixture(t)
	fixtureItem(badAmount, 1)["amount"] = "32,97"

	for field, in := range map[string]map[string]any{"items[3].page": noPage, "items[7].components": badComponents, "items[1].amount": badAmount} {
		res := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", in, nil)
		if !res.isError || res.toolErr.Code != "invalid_request" || res.toolErr.Field != field {
			t.Errorf("%s: result = %+v %s", field, res.toolErr, res.text)
		}
	}
	if e.count("statements") != 0 || e.count("observations") != 0 {
		t.Error("a rejected reading left records behind")
	}
}

func TestSubmittingAStatementNeedsTheStatementsScope(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read", "expenses:write"))

	res := callTool[mcpserver.SubmitStatementExtractionOutput](t, session, "submit_statement_extraction", statementFixture(t), nil)

	if !res.isError || res.toolErr.Code != "forbidden" {
		t.Errorf("result = %+v", res.toolErr)
	}
}

func TestTheStatementProcedureIsAvailableToCase(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "expenses:read"))

	got := callTool[mcpserver.GetInstructionsOutput](t, session, "get_instructions", map[string]any{"topic": "statements"}, nil)

	if got.isError || got.out.Content == "" {
		t.Errorf("get_instructions(statements) = %+v %s", got.out, got.text)
	}
}

func TestTheStatementContractIsPublishedAsJSONSchema(t *testing.T) {
	e := newEnv(t)
	session := e.session(e.principal("Rudolfo", "statements:write"))
	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var schema []byte
	for _, tool := range tools.Tools {
		if tool.Name == "submit_statement_extraction" {
			if schema, err = json.MarshalIndent(tool.InputSchema, "", "  "); err != nil {
				t.Fatal(err)
			}
		}
	}
	schema = append(schema, '\n')
	if os.Getenv("UPDATE_SCHEMAS") == "1" {
		if err := os.WriteFile(statementSchemaFile, schema, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	published, err := os.ReadFile(statementSchemaFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(published) != string(schema) {
		t.Errorf("%s is out of date; run the test with UPDATE_SCHEMAS=1", statementSchemaFile)
	}
}
