package main

import (
	"context"
	"os"

	"glab-axi/internal/commandctx"
	v1cli "glab-axi/internal/contractcli/v1"
	runtimepkg "glab-axi/internal/runtime"
)

func main() {
	os.Exit(commandctx.Run(func(ctx context.Context) int {
		return v1cli.RunNative(ctx, os.Args[1:], runtimepkg.Defaults())
	}))
}
