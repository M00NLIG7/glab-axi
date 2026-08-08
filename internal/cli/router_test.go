package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"glab-axi/internal/product"
	runtimepkg "glab-axi/internal/runtime"
)

func TestFrozenV1RoutesNeverConstructOfficialGlab(t *testing.T) {
	vectors := [][]string{
		{"auth", "import"},
		{"auth", "status", "--host", "gitlab.com"},
		{"mr", "ensure", "--host", "gitlab.com"},
		{"mr", "view", "1", "--host", "gitlab.com"},
		{"ci", "status"},
		{"ci", "jobs"},
		{"ci", "trace"},
		{"--contract", "glab-axi/v1"},
		{"--contract=glab-axi/v1", "mr", "view"},
	}
	for _, args := range vectors {
		var stdout bytes.Buffer
		deps := product.Dependencies{
			Runtime:  runtimepkg.Dependencies{Stdout: &stdout, Stderr: &bytes.Buffer{}},
			GlabPath: "/must/not/be/executed/glab",
		}
		if code := Run(context.Background(), args, deps); code == 0 {
			t.Fatalf("incomplete v1 vector unexpectedly succeeded: %v", args)
		}
		if !strings.Contains(stdout.String(), `schema: "glab-axi/v1"`) {
			t.Fatalf("%v did not use v1: %s", args, stdout.String())
		}
	}
}

func TestVersionAliasesPreserveNativeHandshake(t *testing.T) {
	var outputs []string
	for _, arg := range []string{"--version", "-v", "-V"} {
		var stdout bytes.Buffer
		deps := product.Dependencies{Runtime: runtimepkg.Dependencies{Stdout: &stdout, Stderr: &bytes.Buffer{}}}
		if code := Run(context.Background(), []string{arg}, deps); code != 0 {
			t.Fatalf("%s exit=%d", arg, code)
		}
		outputs = append(outputs, stdout.String())
	}
	if outputs[0] != outputs[1] || outputs[1] != outputs[2] || !strings.Contains(outputs[0], "(contract glab-axi/v1)\n") {
		t.Fatalf("version aliases=%q", outputs)
	}
}

func TestProductAndNativeHelpAreSeparate(t *testing.T) {
	var productOut, nativeOut bytes.Buffer
	if code := Run(context.Background(), []string{"--help"}, product.Dependencies{Runtime: runtimepkg.Dependencies{Stdout: &productOut, Stderr: &bytes.Buffer{}}}); code != 0 {
		t.Fatalf("product help exit=%d", code)
	}
	if code := Run(context.Background(), []string{"--contract", "glab-axi/v1", "--help"}, product.Dependencies{Runtime: runtimepkg.Dependencies{Stdout: &nativeOut, Stderr: &bytes.Buffer{}}}); code != 0 {
		t.Fatalf("native help exit=%d", code)
	}
	if !strings.Contains(productOut.String(), "pinned official glab 1.112.0") || !strings.Contains(nativeOut.String(), "bounded native GitLab MR/CI client") {
		t.Fatalf("product=%q native=%q", productOut.String(), nativeOut.String())
	}
}
