package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
)

type Env struct {
	Stdout io.Writer
	Stderr io.Writer
	Getenv func(string) string
}

type command struct {
	name    string
	summary string
	run     func(ctx context.Context, args []string, env Env) error
}

func commands() []command {
	return []command{
		{name: "serve", summary: "Start the HTTP API", run: runServe},
		{name: "migrate", summary: "Apply (up), roll back one (down) or list (status) migrations", run: runMigrate},
		{name: "invites", summary: "Create a single-use sign-up invite (invites create)", run: runInvites},
		{name: "seed", summary: "Create a development account with admin, case and android keys", run: runSeed},
	}
}

var ErrUsage = errors.New("usage error")

func Run(ctx context.Context, args []string, env Env) error {
	if len(args) == 0 {
		printUsage(env.Stderr)
		return fmt.Errorf("%w: missing command", ErrUsage)
	}
	name := args[0]
	if name == "help" || name == "-h" || name == "--help" {
		printUsage(env.Stdout)
		return nil
	}
	for _, cmd := range commands() {
		if cmd.name == name {
			return cmd.run(ctx, args[1:], env)
		}
	}
	printUsage(env.Stderr)
	return fmt.Errorf("%w: unknown command %q", ErrUsage, name)
}

func printUsage(w io.Writer) {
	var b strings.Builder
	b.WriteString("Usage: lastro <command> [arguments]\n\nCommands:\n")
	for _, cmd := range commands() {
		fmt.Fprintf(&b, "  %-12s %s\n", cmd.name, cmd.summary)
	}
	_, _ = io.WriteString(w, b.String())
}
