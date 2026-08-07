package main

import (
	"context"
	"os"

	"glab-axi/internal/cli"
	"glab-axi/internal/commandctx"
	runtimepkg "glab-axi/internal/runtime"
)

func main() {
	os.Exit(commandctx.Run(func(ctx context.Context) int {
		return cli.RunNative(ctx, os.Args[1:], runtimepkg.Defaults())
	}))
}
