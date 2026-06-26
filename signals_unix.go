//go:build !windows

package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// notifyContext cancels on SIGINT or SIGTERM.
func notifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
