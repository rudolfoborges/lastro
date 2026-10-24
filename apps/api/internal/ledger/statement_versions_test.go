package ledger_test

import (
	"bytes"
	"testing"

	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
)

func reissued(in ledger.StatementExtraction) ledger.StatementExtraction {
	out := in
	out.Items = append([]ledger.StatementItem(nil), in.Items...)
	out.FileSHA256 = bytes.Repeat([]byte{2}, 32)
	return out
}

func TestAChangedVersionHighlightsTheDifferencesWithoutDuplicating(t *testing.T) {
	f := newFixture(t)
	first := reconciledStatement()
	first.FileSHA256 = bytes.Repeat([]byte{1}, 32)
	v1 := f.submitStatement(first)
	transactions := f.count("transactions")
	removedTransaction := v1.Lines[6].Transaction.ID
	changedTransaction := v1.Lines[1].Transaction.ID

	second := reissued(first)
	second.Items[1].Amount = brl(3500)
	pharmacy := line(ledger.ItemPurchase, "FARMACIA EXEMPLO", 4500, "2026-09-30", 2)
	pharmacy.Category = ptr("health")
	second.Items = append(append(second.Items[:6], second.Items[7:]...), pharmacy)
	second.Total = brl(94182 + 203 - 20000 + 4500)
	v2 := f.submitStatement(second)

	if v2.Result != ledger.StatementNewVersion || v2.Statement.ID != v1.Statement.ID || v2.Statement.Version.Number != 2 {
		t.Fatalf("submission = %+v", v2)
	}
	diff := v2.Diff
	if diff.Cause != ledger.CauseReissued || len(diff.Added) != 1 || len(diff.Removed) != 1 || len(diff.Changed) != 1 {
		t.Fatalf("diff = %+v", diff)
	}
	if diff.Changed[0].Before.Amount != brl(3297) || diff.Changed[0].After.Amount != brl(3500) || diff.Changed[0].Fields[0] != "amount" {
		t.Errorf("changed = %+v", diff.Changed[0])
	}
	if f.count("transactions") != transactions+1 {
		t.Errorf("transactions = %d, want only the added line to create one", f.count("transactions"))
	}
	if got := v2.Lines[1]; got.Effect != ledger.EffectUpdated || got.Transaction.ID != changedTransaction || got.Transaction.Amount != brl(3500) {
		t.Errorf("changed line = %+v, want the same transaction with the new amount", got)
	}
	if v2.Review.Status != ledger.ReviewReconciled {
		t.Errorf("review = %+v", v2.Review)
	}
	kinds := map[string]int{}
	for _, c := range v2.Review.Clarifications {
		if c.Kind == ledger.ClarificationStatementVersionDiff {
			kinds[c.Subject]++
			if c.Subject == "removed_line" && *c.TransactionID != removedTransaction {
				t.Errorf("removed line question = %+v", c)
			}
		}
	}
	if kinds["removed_line"] != 1 || kinds["version:2"] != 1 {
		t.Errorf("version questions = %v, want one for the removed line and one summary", kinds)
	}
	removed, err := f.ledger.GetTransaction(t.Context(), f.account, removedTransaction)
	if err != nil {
		t.Fatal(err)
	}
	if removed.Status != ledger.StatusConfirmed {
		t.Errorf("removed line transaction = %s, want it kept until Rudolfo decides", removed.Status)
	}
}

func TestAKeptLineKeepsItsTransactionAndWhatRudolfoConfirmed(t *testing.T) {
	f := newFixture(t)
	v1 := f.submitStatement(reconciledStatement())
	purchase := v1.Lines[1].Transaction.ID
	if _, err := f.ledger.Categorize(t.Context(), f.account, purchase, "groceries", ledger.AllocationConfirmed, "Rudolfo corrigiu.", ledger.Actor{Author: ledger.AuthorUser}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ledger.Correct(t.Context(), f.account, v1.Lines[2].Transaction.ID, ledger.Patch{Amount: ptr(brl(10990))}, "Conferido por Rudolfo.", ledger.Actor{Author: ledger.AuthorUser}); err != nil {
		t.Fatal(err)
	}

	second := reconciledStatement()
	second.Items[2].Amount = brl(11050)
	second.Items[2].Category = ptr("leisure")
	second.Total = brl(94232)
	v2 := f.submitStatement(second)

	kept := v2.Lines[1]
	if kept.Transaction.ID != purchase || kept.Category.Code != "groceries" || kept.Category.Status != ledger.AllocationConfirmed {
		t.Errorf("kept line = %+v, want the same transaction with Rudolfo's category", kept)
	}
	changed := v2.Lines[2]
	if changed.Transaction.Amount != brl(10990) || len(changed.Conflicts) != 1 {
		t.Errorf("changed line = %+v, want the amount Rudolfo confirmed kept and a conflict opened", changed)
	}
}

func TestACompleteReadingAfterAPartialOneCompletesTheStatement(t *testing.T) {
	f := newFixture(t)
	full := syntheticStatement()
	full.Items = full.Items[:len(full.Items)-1]
	full.Total = brl(94182)
	partial := full
	partial.Items = append([]ledger.StatementItem(nil), full.Items[:7]...)
	partial.Reading, partial.Missing = ledger.ReadingPartial, "Página 4 ilegível."
	v1 := f.submitStatement(partial)
	if v1.Review.Status != ledger.ReviewReadIncomplete {
		t.Fatalf("partial = %+v", v1.Review)
	}
	full.Doubts = nil

	v2 := f.submitStatement(full)

	if v2.Result != ledger.StatementNewVersion || v2.Diff.Cause != ledger.CauseUnknown || len(v2.Diff.Added) != 2 || len(v2.Diff.Removed) != 0 {
		t.Fatalf("diff = %+v", v2.Diff)
	}
	if v2.Review.Status != ledger.ReviewReconciled {
		t.Errorf("review = %+v, want reconciled once the whole statement is read and the earlier doubt dropped", v2.Review)
	}
}

func TestADryRunOfANewVersionChangesNothing(t *testing.T) {
	f := newFixture(t)
	f.submitStatement(reconciledStatement())
	transactions, versions := f.count("transactions"), f.count("statement_versions")
	second := reconciledStatement()
	second.Items[1].Amount = brl(3500)
	second.Total = brl(94385)
	second.DryRun = true

	out := f.submitStatement(second)

	if out.Result != ledger.StatementPreviewed || out.Diff == nil || len(out.Diff.Changed) != 1 {
		t.Fatalf("dry run = %+v", out)
	}
	if f.count("transactions") != transactions || f.count("statement_versions") != versions || f.countWhere("transactions", "amount_minor = 3500") != 0 {
		t.Error("a dry run of a new version changed the records")
	}
}

func TestALineThatComesBackInALaterVersionKeepsItsTransaction(t *testing.T) {
	f := newFixture(t)
	first := reconciledStatement()
	v1 := f.submitStatement(first)
	fuel := v1.Lines[6].Transaction.ID
	without := reissued(first)
	without.Items = append(without.Items[:6], without.Items[7:]...)
	without.Total = brl(94182 - 20000)
	f.submitStatement(without)
	transactions := f.count("transactions")

	back := reissued(first)
	back.Total = brl(94182)
	back.Items[1].Amount = brl(3300)
	back.Total = brl(94185)
	v3 := f.submitStatement(back)

	if v3.Result != ledger.StatementNewVersion || v3.Statement.Version.Number != 3 || v3.Lines[6].Transaction.ID != fuel {
		t.Fatalf("third version = %+v, want the fuel line back on its transaction", v3.Lines[6])
	}
	if f.count("transactions") != transactions {
		t.Error("a line that came back created another transaction")
	}
	for _, c := range v3.Review.Clarifications {
		if c.Subject == "removed_line" {
			t.Errorf("removed line question still open: %+v", c)
		}
	}
}
