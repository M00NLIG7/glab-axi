package product

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gl-axi/internal/limits"
)

// Provider JSON can be small while JSON/TOON escaping makes selected fields
// exceed the final envelope cap. Exercise the real CLI, not just normalization.
func TestSnippetExecutableOutputOverflow(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX child fixture")
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := buildSnippetCLI(t, program, dir)
			items := make([]upstreamSnippet, 100)
			for i := range items {
				items[i] = snippetFixture(int64(i+1), false)
				items[i].Description = strings.Repeat("<", 15000)
			}
			var page bytes.Buffer
			encoder := json.NewEncoder(&page)
			encoder.SetEscapeHTML(false)
			if err := encoder.Encode(items); err != nil || page.Len() > limits.MaxJSONPageBytes {
				t.Fatalf("fixture: bytes=%d err=%v", page.Len(), err)
			}
			if err := os.WriteFile(filepath.Join(dir, "page.json"), page.Bytes(), 0600); err != nil {
				t.Fatal(err)
			}
			script := `#!/bin/sh
set -eu
case "$*" in
 'version') printf 'glab 1.112.0 (816e3a52)\n';;
 'api --method GET --hostname gitlab.com user') printf '{"id":7,"username":"reader"}';;
 'api --method GET --hostname gitlab.com snippets?page=1&per_page=100') /bin/cat "$PAGE";;
 'api --method GET --hostname gitlab.com snippets?page=2&per_page=100') printf '[]';;
 *) exit 91;;
esac
`
			if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			for _, format := range []string{"json", "toon"} {
				cmd := exec.Command(binary, "snippet", "list", "--scope", "personal", "--fields", "description", "--limit", "1000", "--format", format)
				cmd.Dir = dir
				cmd.Env = []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin", "PAGE=" + filepath.Join(dir, "page.json")}
				var out, stderr bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &stderr
				err := cmd.Run()
				if err == nil || out.Len() == 0 || out.Len() > 4096 || stderr.Len() != 0 || !strings.Contains(out.String(), "upstream_error") {
					t.Fatalf("%s: err=%v stdout bytes=%d stderr=%s output=%.512s", format, err, out.Len(), stderr.String(), out.String())
				}
				if format == "json" {
					var envelope snippetEnvelope
					if err := json.Unmarshal(out.Bytes(), &envelope); err != nil || envelope.OK || envelope.Meta.Complete {
						t.Fatalf("invalid failure envelope: %s (%v)", out.String(), err)
					}
				}
			}
		})
	}
}
