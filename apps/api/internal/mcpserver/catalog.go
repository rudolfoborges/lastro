package mcpserver

import (
	"context"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
)

type ListCategoriesInput struct {
	IncludeArchived bool `json:"include_archived,omitempty" jsonschema:"also list archived categories, which can no longer be used"`
}

type CategoryOutput struct {
	Code     string `json:"code" jsonschema:"code to send in category fields"`
	Label    string `json:"label" jsonschema:"name shown to Rudolfo"`
	Archived bool   `json:"archived,omitempty"`
}

type ListCategoriesOutput struct {
	Categories []CategoryOutput `json:"categories"`
}

type SaveCategoryInput struct {
	Code  string `json:"code" jsonschema:"snake_case English code, such as pet_care; an existing code is renamed"`
	Label string `json:"label" jsonschema:"name shown to Rudolfo, in Portuguese"`
}

type SaveCategoryOutput struct {
	Outcome  Outcome        `json:"outcome"`
	Category CategoryOutput `json:"category"`
	Summary  string         `json:"summary"`
}

func (s *Server) registerCatalog() {
	addTool(s, toolSpec{
		name:        "list_categories",
		title:       "List categories",
		description: "Lists Rudolfo's expense categories: the code to send in category fields and the label to show him.",
		scope:       accounts.ScopeExpensesRead,
		readOnly:    true,
		idempotent:  true,
	}, func(ctx context.Context, c call, in ListCategoriesInput) (ListCategoriesOutput, error) {
		categories, err := s.deps.Ledger.Categories(ctx, c.principal.AccountID)
		if err != nil {
			return ListCategoriesOutput{}, err
		}
		out := ListCategoriesOutput{Categories: []CategoryOutput{}}
		for _, category := range categories {
			if category.Archived && !in.IncludeArchived {
				continue
			}
			out.Categories = append(out.Categories, CategoryOutput{Code: category.Code, Label: category.Label, Archived: category.Archived})
		}
		return out, nil
	})

	addTool(s, toolSpec{
		name:        "save_category",
		title:       "Create or rename a category",
		description: "Creates a category in Rudolfo's taxonomy, or renames it when the code already exists. Use only when Rudolfo asks for a new category or a new name.",
		scope:       accounts.ScopeExpensesWrite,
		idempotent:  true,
	}, func(ctx context.Context, c call, in SaveCategoryInput) (SaveCategoryOutput, error) {
		category, err := s.deps.Ledger.SaveCategory(ctx, c.principal.AccountID, in.Code, in.Label)
		if err != nil {
			return SaveCategoryOutput{}, err
		}
		return SaveCategoryOutput{
			Outcome:  OutcomeCompleted,
			Category: CategoryOutput{Code: category.Code, Label: category.Label, Archived: category.Archived},
			Summary:  "Categoria \"" + category.Label + "\" salva.",
		}, nil
	})
}

func (o SaveCategoryOutput) reportedOutcome() Outcome {
	return o.Outcome
}
