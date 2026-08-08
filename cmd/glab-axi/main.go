package main

import (
	"context"
	"os"

	"glab-axi/internal/cli"
	"glab-axi/internal/commandctx"
	"glab-axi/internal/product"
	runtimepkg "glab-axi/internal/runtime"
)

func main() {
	os.Exit(commandctx.Run(func(ctx context.Context) int {
		runtimeDeps := runtimepkg.Defaults()
		return cli.Run(ctx, os.Args[1:], product.Defaults(runtimeDeps))
	}))
}
