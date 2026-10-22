package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/rudolfoborges/lastro/apps/api/internal/accounts"
	"github.com/rudolfoborges/lastro/apps/api/internal/config"
	"github.com/rudolfoborges/lastro/apps/api/internal/store"
)

const defaultInviteTTL = 7 * 24 * time.Hour

func runInvites(ctx context.Context, args []string, env Env) error {
	if len(args) == 0 || args[0] != "create" {
		return fmt.Errorf("%w: invites expects the create action", ErrUsage)
	}
	flags := flag.NewFlagSet("invites create", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	expiresIn := flags.Duration("expires-in", defaultInviteTTL, "how long the invite stays valid")
	note := flags.String("note", "", "who the invite is for, kept with the invite")
	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("%w: %w", ErrUsage, err)
	}

	cfg, err := config.Load(env.Getenv)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	invite, err := accounts.NewService(db).CreateInvite(ctx, *expiresIn, *note)
	if err != nil {
		return err
	}
	fmt.Fprintf(env.Stdout, "invite code: %s\nexpires at:  %s\n\nShare the code privately; it is shown only once.\n",
		invite.Code, invite.ExpiresAt.UTC().Format(time.RFC3339))
	return nil
}
