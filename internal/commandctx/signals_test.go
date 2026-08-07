package commandctx

import (
	"context"
	"testing"
)

func TestRunReturnsCommandExit(t *testing.T) {
	if got := Run(func(ctx context.Context) int {
		if ctx.Err() != nil {
			t.Fatal("fresh command context is canceled")
		}
		return 7
	}); got != 7 {
		t.Fatalf("exit=%d", got)
	}
}
