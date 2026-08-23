// Package cli routes the frozen standalone automation grammar before the
// product-facing bounded hybrid. Product commands can never become a fallback
// for an accepted glab-axi/v1 invocation.
package cli

import (
	"context"

	v1cli "gl-axi/internal/contractcli/v1"
	"gl-axi/internal/product"
)

func Run(ctx context.Context, args []string, deps product.Dependencies) int {
	return RunAs(ctx, args, deps, "gl-axi")
}

func RunAs(ctx context.Context, args []string, deps product.Dependencies, programName string) int {
	if programName != "glab-axi" {
		programName = "gl-axi"
	}
	deps.ProgramName = programName
	if contractArgs, ok := explicitV1(args); ok {
		return v1cli.RunNativeAs(ctx, contractArgs, deps.Runtime, programName)
	}
	if isVersionAlias(args) {
		return v1cli.RunNativeAs(ctx, []string{"--version"}, deps.Runtime, programName)
	}
	if isLegacyV1(args) {
		return v1cli.RunNativeAs(ctx, args, deps.Runtime, programName)
	}
	return product.Run(ctx, args, deps)
}

func explicitV1(args []string) ([]string, bool) {
	if len(args) >= 2 && args[0] == "--contract" && args[1] == "glab-axi/v1" {
		return args[2:], true
	}
	if len(args) >= 1 && args[0] == "--contract=glab-axi/v1" {
		return args[1:], true
	}
	return nil, false
}

func isVersionAlias(args []string) bool {
	return len(args) == 1 && (args[0] == "--version" || args[0] == "-v" || args[0] == "-V")
}

func isLegacyV1(args []string) bool {
	if len(args) < 2 {
		return false
	}
	path := args[0] + " " + args[1]
	switch path {
	case "auth import", "ci status", "ci jobs", "ci trace":
		return true
	case "auth status", "mr ensure", "mr view":
		for _, arg := range args[2:] {
			if arg == "--host" {
				return true
			}
		}
	}
	return false
}
