package product

import (
	"bytes"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gl-axi/internal/config"
	"gl-axi/internal/contract/uxv1"
)

// Exercise the compiled public interface, native config/CA/token selection,
// typed issue flow and receipts against TLS, without official-glab acquisition.
func TestIssueEditNativeExecutableAliasesEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("child-denial sentinel uses a POSIX shell")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	validateCLI := issueEditCLIValidator(t)
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binDir := t.TempDir()
			binary := filepath.Join(binDir, program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, output)
			}
			for _, mode := range []string{"success", "description", "clear description", "combined", "nested distinct", "nested conflict", "nested replacement", "quick action", "public title file", "noop", "preview", "stale", "drift", "label reused", "wrong target", "wrong issue", "ID drift", "alternate ID", "lost", "server failure", "malformed", "unapplied", "wrong response", "partial wrong identity", "duplicate response", "read failure", "redirect"} {
				t.Run(mode, func(t *testing.T) {
					f := newIssueEditNativeFixture(t)
					// Publish scenario state under the handler's lock: a child
					// process request is not a Go memory synchronization edge.
					f.mu.Lock()
					f.mode = mode
					if mode == "alternate ID" {
						// The caller binds IID/URL, not a fixture-specific global ID.
						f.before.ID++
						f.after.ID++
					}
					f.redirectStatus = 301
					f.redirectURL = f.server.URL + "/gitlab/api/v4/unapproved-path"
					home := t.TempDir()
					caPath := filepath.Join(home, "fixture-ca.pem")
					if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}), 0o600); err != nil {
						t.Fatal(err)
					}
					cfg, err := config.Load(f.configPath)
					if err != nil {
						t.Fatal(err)
					}
					host := cfg.Hosts[f.host]
					host.CABundle = caPath
					cfg.Hosts[f.host] = host
					if err := config.Save(f.configPath, cfg); err != nil {
						t.Fatal(err)
					}
					marker := filepath.Join(home, "unexpected-glab")
					if err := os.WriteFile(filepath.Join(home, "glab"), []byte("#!/bin/sh\nprintf invoked > \"$GL_AXI_CHILD_SENTINEL\"\nexit 99\n"), 0o700); err != nil {
						t.Fatal(err)
					}
					args := append(f.args(t), "--auth-source", "native")
					switch mode {
					case "description", "clear description", "combined", "quick action":
						body := "private new body"
						if mode == "clear description" {
							body = ""
						}
						if mode == "quick action" {
							body = "ordinary text\n/close"
						}
						bodyPath := filepath.Join(home, "body")
						if err := os.WriteFile(bodyPath, []byte(body), 0o600); err != nil {
							t.Fatal(err)
						}
						args = append(args, "--description-file", bodyPath)
						f.payload = map[string]any{"description": body}
						if mode == "combined" {
							args = append(args, "--remove-label", "bug")
							f.after.Labels = []string{"keep", "triage"}
							f.payload["title"], f.payload["add_labels"], f.payload["remove_labels"] = "new title", "triage", "bug"
						} else {
							args = removeFlag(removeFlag(args, "--title-file", true), "--add-label", true)
							f.after = f.before
							f.after.UpdatedAt = timePointer(issueEditNextTime)
						}
						f.after.Description = body
					case "nested distinct", "nested conflict", "nested replacement":
						beforeLabel, addLabel := "workflow::backend::review", "workflow::frontend::review"
						if mode != "nested distinct" {
							addLabel = "workflow::backend::development"
						}
						f.before.Labels = []string{"keep", beforeLabel}
						f.after.Labels = []string{"keep", beforeLabel, addLabel}
						f.catalog = []issueEditLabel{{ID: 10, Name: beforeLabel}, {ID: 11, Name: addLabel}, {ID: 12, Name: "keep"}}
						f.payload = map[string]any{"title": "new title", "add_labels": addLabel}
						args = replaceArg(args, "triage", addLabel)
						if mode == "nested replacement" {
							args = append(args, "--remove-label", beforeLabel)
							f.after.Labels = []string{"keep", addLabel}
							f.payload["remove_labels"] = beforeLabel
						}
					case "public title file":
						if err := os.Chmod(args[indexOfIssueEditFlag(args, "--title-file")+1], 0o644); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "preview" {
						args = append(args, "--dry-run")
					}
					if mode == "noop" {
						args = replaceArg(args, "triage", "keep")
						if err := os.WriteFile(args[indexOfIssueEditFlag(args, "--title-file")+1], []byte(f.before.Title), 0o600); err != nil {
							t.Fatal(err)
						}
					}
					if mode == "wrong target" {
						args = replaceArg(args, f.before.WebURL, f.webBase+"/other/project/-/issues/42")
					}
					f.mu.Unlock()
					command := exec.Command(binary, args...)
					command.Dir = home
					command.Env = []string{"PATH=" + home + ":/usr/bin:/bin", "HOME=" + home, "GL_AXI_CONFIG=" + f.configPath, "GL_AXI_TOKEN=" + f.token, "GL_AXI_CHILD_SENTINEL=" + marker, "GIT_CONFIG_NOSYSTEM=1"}
					var stdout, stderr bytes.Buffer
					command.Stdout, command.Stderr = &stdout, &stderr
					runErr := command.Run()
					validateCLI(t, stdout.Bytes())
					if _, err := os.Stat(marker); !os.IsNotExist(err) {
						t.Fatal("native CLI invoked official glab")
					}
					f.assertConfidential(t, stdout.String()+stderr.String())
					var envelope struct {
						Schema string          `json:"schema"`
						OK     bool            `json:"ok"`
						Data   issueEditOutput `json:"data"`
						Meta   uxv1.Meta       `json:"meta"`
						Error  struct {
							Code uxv1.Code `json:"code"`
						} `json:"error"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
						t.Fatalf("decode: %v output=%s stderr=%s", err, &stdout, &stderr)
					}
					wantAction, wantPuts := "", 0
					switch mode {
					case "success", "alternate ID", "description", "clear description", "combined", "nested distinct", "nested replacement":
						wantAction, wantPuts = "updated", 1
					case "noop":
						wantAction = "unchanged"
					case "preview":
						wantAction = "preview"
					case "lost", "server failure", "malformed":
						wantAction, wantPuts = "reconciled_update", 1
					case "unapplied", "wrong response", "partial wrong identity", "duplicate response", "read failure", "redirect":
						wantPuts = 1
					}
					if envelope.Schema != uxv1.Schema {
						t.Fatalf("wrong schema: %s", &stdout)
					}
					if wantAction != "" {
						if runErr != nil || !envelope.OK || envelope.Data.Edit.Action != wantAction || envelope.Meta.Backend != "native" || envelope.Meta.UpstreamVersion != "" {
							t.Fatalf("action=%s err=%v output=%s stderr=%s", wantAction, runErr, &stdout, &stderr)
						}
					} else if runErr == nil || envelope.OK {
						t.Fatalf("unsafe success: %s", &stdout)
					}
					if wantAction == "" && wantPuts == 1 {
						assertIssueEditAmbiguous(t, 1, stdout.Bytes())
					}
					if wantAction == "" && wantPuts == 0 {
						wantCode := uxv1.CodeSafety
						switch mode {
						case "nested conflict", "stale", "drift", "label reused":
							wantCode = uxv1.CodeConflict
						}
						if envelope.Error.Code != wantCode {
							t.Fatalf("refusal code=%s want=%s output=%s", envelope.Error.Code, wantCode, &stdout)
						}
					}
					if mode == "wrong issue" || mode == "ID drift" {
						if envelope.Error.Code != uxv1.CodeSafety {
							t.Fatalf("identity refusal code=%s output=%s", envelope.Error.Code, &stdout)
						}
						wantReads := 1
						if mode == "ID drift" {
							wantReads = 2
						}
						f.mu.Lock()
						reads := f.issueReads
						f.mu.Unlock()
						if reads != wantReads {
							t.Fatalf("issue reads=%d want=%d", reads, wantReads)
						}
					}
					if mode == "alternate ID" && envelope.Data.Edit.Identity.IssueID != f.before.ID {
						t.Fatalf("receipt did not retain observed identity: %s", &stdout)
					}
					wantTotal := -1
					if mode == "quick action" || mode == "public title file" {
						wantTotal = 0
					}
					f.assertRequests(t, wantTotal, wantPuts)
					if strings.Contains(stdout.String(), "private new body") {
						t.Fatal("receipt exposed proposed private body")
					}
					t.Logf("CLI receipt: %s", stdout.Bytes())
					for _, record := range f.snapshot() {
						t.Logf("TLS provider request: %s %s authenticated=%t", record.method, record.path, record.authenticated)
						if strings.Contains(record.path, "unapproved") {
							t.Fatal("native CLI followed PUT redirect")
						}
					}
				})
			}
		})
	}
}
