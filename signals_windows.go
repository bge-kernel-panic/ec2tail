//go:build windows

package main

import (
	"context"
	"os"
	"os/signal"
)

// notifyContext cancels on interrupt. Windows has no SIGTERM; os.Interrupt covers Ctrl-C.
func notifyContext(parent context.Context) (context.Context, context.CancelFunc) {
	return signal.NotifyContext(parent, os.Interrupt)
}
