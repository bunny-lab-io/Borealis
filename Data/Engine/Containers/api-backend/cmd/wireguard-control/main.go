package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"borealis/api-backend/internal/wireguardcontrol"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := wireguardcontrol.Serve(ctx, wireguardcontrol.ConfigFromEnv()); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
