package main

import (
	"context"
	"os"

	"glab-axi/internal/commandctx"
	"glab-axi/internal/compat"
	runtimepkg "glab-axi/internal/runtime"
)

func main() {
	os.Exit(commandctx.Run(func(ctx context.Context) int {
		return compat.Run(ctx, os.Args[1:], runtimepkg.Defaults())
	}))
}
