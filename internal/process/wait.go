package process

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// Wait blocks until SIGINT or SIGTERM.
func Wait() error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()
	return nil
}

// Hostname identifies the idle process in logs.
func Hostname() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
