package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/rudolfoborges/lastro/apps/api/internal/cli"
)

func main() {
	os.Exit(run())
}

func run() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	err := cli.Run(ctx, os.Args[1:], cli.Env{Stdout: os.Stdout, Stderr: os.Stderr, Getenv: os.Getenv})
	switch {
	case err == nil:
		return 0
	case errors.Is(err, cli.ErrUsage):
		fmt.Fprintln(os.Stderr, "lastro:", err)
		return 2
	default:
		fmt.Fprintln(os.Stderr, "lastro:", err)
		return 1
	}
}
