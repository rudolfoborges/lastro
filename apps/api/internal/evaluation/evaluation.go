package evaluation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/ledger"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
	"github.com/rudolfoborges/lastro/apps/api/internal/store/platformdb"
	"github.com/rudolfoborges/lastro/apps/api/internal/taxonomy"
)

const Window = 2 * time.Hour

var (
	ErrNoEvaluation = errors.New("no evaluation was started with this credential")
	ErrExpired      = errors.New("the Case evaluation ran out of time; call finish_case_evaluation to close it before anything else")
)

type Run struct {
	ID               uuid.UUID
	SandboxAccountID uuid.UUID
	ScenarioVersion  int
	StartedAt        time.Time
	ExpiresAt        time.Time
	FinishedAt       *time.Time
	Report           *Report
}

type Service struct {
	store  *store.Store
	ledger *ledger.Service
}

func NewService(s *store.Store, l *ledger.Service) *Service {
	return &Service{store: s, ledger: l}
}

// Start opens an evaluation for the credential, or returns the one already
// open, in a sandbox account of its own that the scenarios are loaded into.
func (s *Service) Start(ctx context.Context, p accounts.Principal, now time.Time) (Run, bool, error) {
	if open, err := s.unfinished(ctx, p, now); err != nil || open != nil {
		if open != nil {
			return *open, false, nil
		}
		return Run{}, false, err
	}
	var row platformdb.CaseEvaluation
	err := s.store.PlatformTx(ctx, func(q *platformdb.Queries) error {
		sandbox, err := q.CreateSandboxAccount(ctx, platformdb.CreateSandboxAccountParams{ID: store.NewID(), Name: "Avaliação da Case", OwnerID: p.AccountID})
		if err != nil {
			return fmt.Errorf("create sandbox account: %w", err)
		}
		for _, category := range taxonomy.Defaults {
			if err := q.CreateCategory(ctx, platformdb.CreateCategoryParams{ID: store.NewID(), AccountID: sandbox.ID, Code: category.Code, Label: category.Label}); err != nil {
				return fmt.Errorf("create sandbox category %s: %w", category.Code, err)
			}
		}
		row, err = q.InsertCaseEvaluation(ctx, platformdb.InsertCaseEvaluationParams{
			ID:               store.NewID(),
			AccountID:        p.AccountID,
			SandboxAccountID: sandbox.ID,
			CredentialKind:   string(p.CredentialKind()),
			CredentialID:     p.CredentialID(),
			ScenarioVersion:  ScenarioVersion,
			StartedAt:        now,
			ExpiresAt:        now.Add(Window),
		})
		if err != nil {
			return fmt.Errorf("record evaluation: %w", err)
		}
		return nil
	})
	if err != nil {
		return Run{}, false, err
	}
	if err := prepare(ctx, s.ledger, row.SandboxAccountID, now); err != nil {
		return Run{}, false, err
	}
	run, err := toRun(row)
	return run, true, err
}

// Route sends the calls of a credential with an open evaluation to its
// sandbox account, so the scenarios never touch Rudolfo's records. An
// evaluation that ran out of time keeps refusing calls until it is finished,
// instead of letting scenario records fall into Rudolfo's account.
func (s *Service) Route(ctx context.Context, p accounts.Principal, now time.Time) (accounts.Principal, error) {
	open, err := s.unfinished(ctx, p, now)
	if err != nil || open == nil {
		return p, err
	}
	p.AccountID = open.SandboxAccountID
	return p, nil
}

func (s *Service) Finish(ctx context.Context, p accounts.Principal, now time.Time) (Run, error) {
	var run Run
	err := s.store.PlatformTx(ctx, func(q *platformdb.Queries) error {
		open, err := q.OpenCaseEvaluation(ctx, platformdb.OpenCaseEvaluationParams{CredentialID: p.CredentialID(), CredentialKind: string(p.CredentialKind()), AccountID: p.AccountID})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNoEvaluation
		}
		if err != nil {
			return fmt.Errorf("find evaluation: %w", err)
		}
		report, err := grade(ctx, s.ledger, open.SandboxAccountID, now)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(report)
		if err != nil {
			return fmt.Errorf("encode report: %w", err)
		}
		finished, err := q.FinishCaseEvaluation(ctx, platformdb.FinishCaseEvaluationParams{ID: open.ID, FinishedAt: &now, Report: encoded})
		if err != nil {
			return fmt.Errorf("finish evaluation: %w", err)
		}
		run, err = toRun(finished)
		return err
	})
	return run, err
}

func (s *Service) Runs(ctx context.Context, accountID uuid.UUID, limit int) ([]Run, error) {
	rows, err := s.store.Platform().ListCaseEvaluations(ctx, platformdb.ListCaseEvaluationsParams{AccountID: accountID, RowLimit: int32(limit)}) //nolint:gosec // callers pass small limits
	if err != nil {
		return nil, fmt.Errorf("list evaluations: %w", err)
	}
	runs := make([]Run, len(rows))
	for i, row := range rows {
		if runs[i], err = toRun(row); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

func (s *Service) unfinished(ctx context.Context, p accounts.Principal, now time.Time) (*Run, error) {
	row, err := s.store.Platform().UnfinishedCaseEvaluation(ctx, platformdb.UnfinishedCaseEvaluationParams{
		CredentialID: p.CredentialID(), CredentialKind: string(p.CredentialKind()), AccountID: p.AccountID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find open evaluation: %w", err)
	}
	if !now.Before(row.ExpiresAt) {
		return nil, ErrExpired
	}
	run, err := toRun(row)
	return &run, err
}

func toRun(row platformdb.CaseEvaluation) (Run, error) {
	run := Run{ID: row.ID, SandboxAccountID: row.SandboxAccountID, ScenarioVersion: int(row.ScenarioVersion), StartedAt: row.StartedAt, ExpiresAt: row.ExpiresAt, FinishedAt: row.FinishedAt}
	if len(row.Report) > 0 {
		var report Report
		if err := json.Unmarshal(row.Report, &report); err != nil {
			return Run{}, fmt.Errorf("decode report: %w", err)
		}
		run.Report = &report
	}
	return run, nil
}
