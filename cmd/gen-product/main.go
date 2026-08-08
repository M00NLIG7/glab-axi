// Command gen-product writes public docs and Agent Skill assets from the
// executable product registry.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"glab-axi/internal/product"
)

func main() {
	root := flag.String("root", ".", "repository root")
	flag.Parse()
	if flag.NArg() != 0 {
		fail("unexpected positional arguments")
	}
	files := map[string]string{
		filepath.Join("skills", "glab-axi", "SKILL.md"): product.SkillMarkdown(),
		filepath.Join("docs", "command-reference.md"):   product.CommandReferenceMarkdown(),
	}
	for relative, content := range files {
		path := filepath.Join(*root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			fail(err.Error())
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			fail(err.Error())
		}
	}
}

func fail(message string) {
	_, _ = fmt.Fprintln(os.Stderr, message)
	os.Exit(2)
}
