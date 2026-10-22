package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
)

var errSeedInProduction = errors.New("seed only runs outside production")

type seedKey struct {
	name   string
	scopes []string
}

var seedKeys = []seedKey{
	{name: "case", scopes: []string{"expenses:read", "expenses:write", "statements:write"}},
	{name: "android", scopes: []string{"wallet:ingest"}},
}

func runSeed(ctx context.Context, _ []string, env Env) error {
	cfg, err := config.Load(env.Getenv)
	if err != nil {
		return err
	}
	if cfg.Environment == config.Production {
		return errSeedInProduction
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	svc := accounts.NewService(db)
	invite, err := svc.CreateInvite(ctx, time.Hour, "development seed")
	if err != nil {
		return err
	}
	account, admin, err := svc.SignUp(ctx, invite.Code, "Development "+time.Now().UTC().Format("2006-01-02 15:04:05"))
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "account %s (%s)\n\n%-8s %s\n", account.Name, account.ID, admin.Name, admin.Token)
	for _, key := range seedKeys {
		issued, err := svc.CreateKey(ctx, account.ID, key.name, key.scopes)
		if err != nil {
			return err
		}
		fmt.Fprintf(env.Stdout, "%-8s %s\n", issued.Name, issued.Token)
	}
	return nil
}
