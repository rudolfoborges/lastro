package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
)

func runAccounts(ctx context.Context, args []string, env Env) error {
	if len(args) != 1 || args[0] != "list" {
		return fmt.Errorf("%w: accounts expects the list action", ErrUsage)
	}
	return withAccounts(ctx, env, func(svc *accounts.Service) error {
		list, err := svc.ListAccounts(ctx)
		if err != nil {
			return err
		}
		for _, account := range list {
			fmt.Fprintf(env.Stdout, "%s  %s  %s\n", account.ID, account.CreatedAt.UTC().Format(time.RFC3339), account.Name)
		}
		return nil
	})
}

func runKeys(ctx context.Context, args []string, env Env) error {
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("%w: keys expects the create action", ErrUsage)
	}
	flags := flag.NewFlagSet("keys create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	account := flags.String("account", "", "ID of the account, from accounts list")
	name := flags.String("name", "", "name of the key, such as admin or android")
	scopes := flags.String("scopes", "", "comma-separated scopes, such as admin or wallet:ingest")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}
	accountID, err := uuid.Parse(*account)
	if err != nil {
		return fmt.Errorf("%w: --account must be an account ID from accounts list", ErrUsage)
	}
	if *name == "" || *scopes == "" {
		return fmt.Errorf("%w: --name and --scopes are required", ErrUsage)
	}
	return withAccounts(ctx, env, func(svc *accounts.Service) error {
		key, err := svc.CreateKey(ctx, accountID, *name, strings.Split(*scopes, ","))
		if err != nil {
			return err
		}
		scopeNames := make([]string, len(key.Scopes))
		for i, scope := range key.Scopes {
			scopeNames[i] = string(scope)
		}
		fmt.Fprintf(env.Stdout, "key id: %s\nscopes: %s\ntoken:  %s\n\nStore the token now; it is shown only once.\n",
			key.ID, strings.Join(scopeNames, ","), key.Token)
		return nil
	})
}

func withAccounts(ctx context.Context, env Env, fn func(*accounts.Service) error) error {
	cfg, err := config.Load(env.Getenv)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()
	return fn(accounts.NewService(db))
}
