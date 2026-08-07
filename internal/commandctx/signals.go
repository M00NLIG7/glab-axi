// Package commandctx provides cancellation with deterministic shell exit codes.
package commandctx

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

func Run(run func(context.Context) int) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	signals := make(chan os.Signal, 1)
	received := make(chan os.Signal, 1)
	done := make(chan struct{})
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	go func() {
		select {
		case sig := <-signals:
			received <- sig
			cancel()
		case <-done:
		}
	}()
	code := run(ctx)
	close(done)
	select {
	case sig := <-received:
		if code == 130 && sig == syscall.SIGTERM {
			return 143
		}
	default:
	}
	return code
}
