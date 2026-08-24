package main

import (
	"context"
	"os"

	"gl-axi/internal/cli"
	"gl-axi/internal/commandctx"
	"gl-axi/internal/product"
	runtimepkg "gl-axi/internal/runtime"
)

func main() {
	os.Exit(commandctx.Run(func(ctx context.Context) int {
		runtimeDeps := runtimepkg.Defaults()
		return cli.RunAs(ctx, os.Args[1:], product.DefaultsFor(runtimeDeps, "glab-axi"), "glab-axi")
	}))
}
