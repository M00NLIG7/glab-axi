package product

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
)

func TestReadListOutputBoundsExecutable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("process fixture uses a POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	description := strings.Repeat("x", 18<<10)
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, output)
			}
			script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$GL_AXI_READ_RECORD"
if [ "$*" = version ]; then
  printf '%s\n' 'glab 1.112.0 (816e3a52)'
  exit 0
fi
[ "$#" -eq 10 ] || exit 1
page=$6
case "$page" in 1|2|3|4|5|6) ;; *) exit 1 ;; esac
[ "$*" = "$GL_AXI_READ_GROUP list --output json --page $page --per-page 100 -R group/project" ] || exit 1
/bin/cat "$GL_AXI_READ_PAGES/$page.json"
`
			if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			for _, group := range []string{"issue", "mr"} {
				for _, count := range []int{400, 500} {
					pages := t.TempDir()
					for page := 1; page <= count/100+1; page++ {
						items := make([]any, 0, 100)
						for iid := (page-1)*100 + 1; iid <= min(page*100, count); iid++ {
							items = append(items, readParityObject(group, description, iid))
						}
						body, err := json.Marshal(items)
						if err != nil {
							t.Fatal(err)
						}
						if len(body) > limits.MaxJSONPageBytes {
							t.Fatalf("fixture exceeded provider page bound: %d", len(body))
						}
						if err := os.WriteFile(filepath.Join(pages, strconv.Itoa(page)+".json"), body, 0600); err != nil {
							t.Fatal(err)
						}
					}
					for _, format := range []string{"json", "toon"} {
						t.Run(fmt.Sprintf("%s/%d/%s", group, count, format), func(t *testing.T) {
							record := filepath.Join(t.TempDir(), "calls")
							command := exec.Command(binary, readParityArgs([]string{group, "list", "--fields=description", "--limit=500", "--format=" + format})...)
							command.Dir = dir
							command.Env = []string{"PATH=" + dir + ":/usr/bin:/bin", "HOME=" + dir, "GLAB_CONFIG_DIR=" + filepath.Join(dir, "config"), "GL_AXI_READ_RECORD=" + record, "GL_AXI_READ_GROUP=" + group, "GL_AXI_READ_PAGES=" + pages}
							var stdout, stderr bytes.Buffer
							command.Stdout, command.Stderr = &stdout, &stderr
							err := command.Run()
							code := 0
							if err != nil {
								exit, ok := err.(*exec.ExitError)
								if !ok {
									t.Fatal(err)
								}
								code = exit.ExitCode()
							}
							wantCode := 0
							if count == 500 {
								wantCode = 8
							}
							if code != wantCode || stderr.Len() != 0 {
								t.Fatalf("exit=%d want=%d output bytes=%d stderr=%s", code, wantCode, stdout.Len(), &stderr)
							}
							assertReadListOutputBound(t, stdout.Bytes(), group, format, count, description)
							expected := "version\n"
							for page := 1; page <= count/100+1; page++ {
								expected += fmt.Sprintf("%s list --output json --page %d --per-page 100 -R group/project\n", group, page)
							}
							calls, err := os.ReadFile(record)
							if err != nil || string(calls) != expected {
								t.Fatalf("unexpected child work: %s error=%v", calls, err)
							}
						})
					}
				}
			}
		})
	}
}

func assertReadListOutputBound(t *testing.T, body []byte, group, format string, count int, description string) {
	t.Helper()
	oversized := count == 500
	if len(body) > limits.MaxOperationBytes+1 || oversized && len(body) > limits.MaxErrorOutputBytes || !oversized && len(body) < count*len(description) {
		t.Fatalf("unexpected output size: %d", len(body))
	}
	if format == "json" {
		var envelope struct {
			Schema string          `json:"schema"`
			OK     bool            `json:"ok"`
			Data   json.RawMessage `json:"data"`
			Error  *uxv1.Error     `json:"error"`
			Meta   uxv1.Meta       `json:"meta"`
		}
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Schema != uxv1.Schema || envelope.OK == oversized || envelope.Meta.Complete == oversized || envelope.Meta.Count != count || envelope.Meta.Truncated != oversized {
			t.Fatalf("unexpected envelope: schema=%s ok=%v meta=%#v", envelope.Schema, envelope.OK, envelope.Meta)
		}
		if oversized {
			if len(envelope.Data) != 0 || envelope.Error == nil || envelope.Error.Code != uxv1.CodeUpstream || envelope.Error.Message != "output exceeds the operation limit" || envelope.Error.Retryable || envelope.Meta.Reason != "operation_limit" {
				t.Fatalf("unexpected error envelope: %s", body)
			}
			return
		}
		var data map[string][]struct {
			IID         int    `json:"iid"`
			Description string `json:"description"`
		}
		if err := json.Unmarshal(envelope.Data, &data); err != nil {
			t.Fatal(err)
		}
		key := "issues"
		if group == "mr" {
			key = "mrs"
		}
		if envelope.Error != nil || len(data[key]) != count || envelope.Meta.Reason != "" {
			t.Fatalf("successful result: error=%v items=%d reason=%s", envelope.Error, len(data[key]), envelope.Meta.Reason)
		}
		for index, item := range data[key] {
			if item.IID != index+1 || item.Description != description {
				t.Fatalf("resource %d changed: iid=%d description bytes=%d", index, item.IID, len(item.Description))
			}
		}
		return
	}
	text := string(body)
	if !strings.HasPrefix(text, "schema: \"glab-axi/ux-v1\"\nok: "+strconv.FormatBool(!oversized)+"\n") || !strings.Contains(text, "\n  complete: "+strconv.FormatBool(!oversized)+"\n") || !strings.Contains(text, fmt.Sprintf("\n  count: %d\n", count)) || !strings.Contains(text, "\n  truncated: "+strconv.FormatBool(oversized)+"\n") {
		t.Fatalf("unexpected TOON envelope metadata; bytes=%d", len(body))
	}
	if oversized {
		if !strings.Contains(text, "\nerror:\n  code: \"upstream_error\"\n  message: \"output exceeds the operation limit\"\n  retryable: false\n") || !strings.Contains(text, "\n  reason: \"operation_limit\"\n") || strings.Contains(text, "\ndata:") || strings.Count(text, "schema:") != 1 {
			t.Fatalf("unexpected TOON error envelope: %s", text)
		}
		return
	}
	key := "issues"
	if group == "mr" {
		key = "mrs"
	}
	if !strings.Contains(text, fmt.Sprintf("\ndata:\n  %s[%d]:\n", key, count)) || strings.Count(text, "      description: "+strconv.Quote(description)+"\n") != count || strings.Contains(text, "\nerror:") {
		t.Fatalf("unexpected TOON list descriptions; bytes=%d", len(body))
	}
}
