package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/nubjs/nub/pm-go/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], cli.System())
	stop()
	os.Exit(code)
}
