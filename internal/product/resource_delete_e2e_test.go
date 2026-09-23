package product

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/config"
)

// This is the real canonical/alias process path, not a mocked native client.
// Only the synthetic TLS CA, runtime token and isolated native config are used.
func TestResourceDeletionExecutableAliasesEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("persisted native config/self-managed mapping on Windows remains unproven")
	}
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			cases := deletionCases()
			for _, tag := range []struct{ name, value, api, web string }{
				{"release-parentheses", "v1.0(legacy)", "v1.0%28legacy%29", "v1.0(legacy)"},
				{"release-punctuation", "v1.0!$&'()+,;=@", "v1.0%21$&%27%28%29+%2C%3B=@", "v1.0!$&'()+,;=@"},
				{"release-escaped", "series/v1.0(legacy)#café", "series%2Fv1.0%28legacy%29%23caf%C3%A9", "series%2Fv1.0(legacy)%23caf%C3%A9"},
			} {
				item := deletionCases()[2]
				item.name, item.selector = tag.name, tag.value
				item.route = "projects/101/releases/" + tag.api
				item.webPath = "/group/project/-/releases/" + tag.web
				item.expected = deletionReplaceFlag(item.expected, "--acknowledge-catalog-unpublication", deletionTestWeb+item.webPath, false)
				item.body["tag_name"] = tag.value
				item.body["tag_path"] = "/gitlab/group/project/-/tags/" + tag.web
				cases = append(cases, item)
			}
			for _, item := range cases {
				t.Run(item.name, func(t *testing.T) {
					modes := []string{"success", "missing-confirmation", "missing-native", "pre-404", "pre-403", "drift", "delete-403", "lost-absence", "redirect-307", "redirect-308"}
					if item.group == "snippet" {
						modes = append(modes, "snippet-400-absent", "snippet-400-present", "snippet-400-unverified")
					} else {
						modes = append(modes, "delete-400")
					}
					if item.group == "pipeline" {
						item.body["status"] = "running"
						item.expected = deletionReplaceFlag(item.expected, "--expected-status", "running", false)
						modes = append(modes, "missing-child-acknowledgment", "wrong-child-acknowledgment", "child-canceled-parent-present")
					}
					if item.group == "release" {
						modes = append(modes, "missing-catalog-acknowledgment", "wrong-catalog-acknowledgment", "pre-tag-drift", "tag-drift", "malformed-delete")
						if item.name != "release" {
							modes = append(modes, "api-escaped-web-url", "wrong-url", "wrong-id")
						}
					}
					for _, mode := range modes {
						t.Run(mode, func(t *testing.T) {
							f := newDeletionFixture(t, item, mode)
							home := filepath.Dir(f.configPath)
							caPath := filepath.Join(home, "fixture-ca.pem")
							if err := os.WriteFile(caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: f.server.Certificate().Raw}), 0600); err != nil {
								t.Fatal(err)
							}
							cfg, err := config.Load(f.configPath)
							if err != nil {
								t.Fatal(err)
							}
							host := cfg.Hosts[deletionTestHost]
							host.CABundle = caPath
							cfg.Hosts[deletionTestHost] = host
							encoded, err := json.Marshal(cfg)
							if err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(f.configPath, encoded, 0600); err != nil {
								t.Fatal(err)
							}
							args := item.args()
							if mode == "api-escaped-web-url" {
								apiURL := deletionTestWeb + "/group/project/-/releases/" + strings.TrimPrefix(item.route, "projects/101/releases/")
								for _, flag := range []string{"--expected-url", item.confirmation, "--acknowledge-catalog-unpublication"} {
									args = deletionReplaceFlag(args, flag, apiURL, false)
								}
							}
							if mode == "missing-confirmation" {
								args = deletionReplaceFlag(args, item.confirmation, "", true)
							}
							if mode == "missing-native" {
								args = deletionReplaceFlag(args, "--auth-source", "", true)
							}
							if mode == "missing-child-acknowledgment" {
								args = deletionReplaceFlag(args, "--acknowledge-child-cancellation", "", true)
							}
							if mode == "wrong-child-acknowledgment" {
								args = deletionReplaceFlag(args, "--acknowledge-child-cancellation", deletionTestWeb+"/group/project/-/pipelines/89", false)
							}
							if mode == "missing-catalog-acknowledgment" {
								args = deletionReplaceFlag(args, "--acknowledge-catalog-unpublication", "", true)
							}
							if mode == "wrong-catalog-acknowledgment" {
								args = deletionReplaceFlag(args, "--acknowledge-catalog-unpublication", deletionTestWeb+"/group/project/-/releases/v2.0", false)
							}
							ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
							defer cancel()
							command := exec.CommandContext(ctx, binary, args...)
							command.Dir = home
							// No ambient profile, helper, proxy or real credential can be used.
							command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "PATH=" + home, "GL_AXI_CONFIG=" + f.configPath, "GL_AXI_TOKEN=" + f.token, "NO_PROXY=*"}
							var stdout, stderr bytes.Buffer
							command.Stdout, command.Stderr = &stdout, &stderr
							runErr := command.Run()
							var out deletionEnvelope
							if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
								t.Fatalf("invalid CLI envelope: %v", err)
							}
							if bytes.Contains(stdout.Bytes(), []byte(f.token)) || bytes.Contains(stderr.Bytes(), []byte(f.token)) {
								t.Fatal("synthetic credential leaked")
							}
							wantSuccess := mode == "success"
							if out.OK != wantSuccess || (runErr == nil) != wantSuccess {
								t.Fatalf("success=%v exit=%v code=%s", out.OK, runErr, out.Error.Code)
							}
							wantDeletes := 1
							switch mode {
							case "missing-confirmation", "missing-native", "missing-child-acknowledgment", "wrong-child-acknowledgment", "missing-catalog-acknowledgment", "wrong-catalog-acknowledgment", "pre-404", "pre-403", "pre-tag-drift", "drift", "api-escaped-web-url", "wrong-url", "wrong-id":
								wantDeletes = 0
							}
							f.mu.Lock()
							deletes, unexpected, credential, requests := f.deletes, f.forbiddenPaths, f.wrongCredential, len(f.requests)
							catalogState, catalogVersions, tagReads := f.catalogState, f.catalogVersions, f.tagReads
							reads, snippetDeleted := f.reads, f.snippetDeleted
							f.mu.Unlock()
							if deletes != wantDeletes || unexpected != 0 || credential || requests > 12 {
								t.Fatalf("writes=%d unexpected=%d credential=%v requests=%d", deletes, unexpected, credential, requests)
							}
							if strings.HasPrefix(mode, "snippet-400-") {
								r := out.Error.Receipt.Deletion
								wantPostcondition := "not_found"
								wantRequests := 6
								if !item.personal {
									wantRequests = 9
									if r.Scope != "project" {
										t.Fatalf("snippet scope=%s", r.Scope)
									}
								} else if r.Scope != "personal" {
									t.Fatalf("snippet scope=%s", r.Scope)
								}
								if mode != "snippet-400-absent" {
									wantRequests--
									if !item.personal {
										wantRequests--
									}
									wantPostcondition = "present"
									if mode == "snippet-400-unverified" {
										wantPostcondition = "unverified"
									}
								}
								if out.Error.Code != "ambiguous_delete" || out.Error.Retryable || r.Action != "ambiguous" || r.Acknowledged || !r.DeleteAttempted || r.DeleteStatus != 400 || r.Postcondition != wantPostcondition || r.URL != deletionTestWeb+item.webPath || reads != 3 || requests != wantRequests || snippetDeleted != (mode != "snippet-400-present") {
									t.Fatalf("snippet HTTP 400: code=%s receipt=%+v reads=%d requests=%d committed=%v", out.Error.Code, r, reads, requests, snippetDeleted)
								}
								if exit, ok := runErr.(*exec.ExitError); !ok || exit.ExitCode() != 6 {
									t.Fatalf("ambiguous snippet exit=%v", runErr)
								}
							}
							if mode == "delete-400" {
								r := out.Error.Receipt.Deletion
								if out.Error.Code != "validation_error" || out.Error.Retryable || r.Action != "rejected" || r.Acknowledged || !r.DeleteAttempted || r.DeleteStatus != 400 || r.Postcondition != "not_checked" || reads != 2 {
									t.Fatalf("definite rejection changed: code=%s receipt=%+v reads=%d", out.Error.Code, r, reads)
								}
							}
							if mode == "api-escaped-web-url" && (out.Error.Code != "safety_violation" || requests != 0) {
								t.Fatalf("noncanonical web URL accepted: code=%s requests=%d", out.Error.Code, requests)
							}
							if mode == "missing-confirmation" || mode == "missing-native" || mode == "missing-child-acknowledgment" || mode == "wrong-child-acknowledgment" || mode == "missing-catalog-acknowledgment" || mode == "wrong-catalog-acknowledgment" {
								if requests != 0 {
									t.Fatal("invalid selectors reached provider")
								}
							}
							if wantSuccess && (out.Data.Deletion.Action != "deleted" || out.Meta.Backend != "native") {
								t.Fatalf("false receipt: %+v", out.Data.Deletion)
							}
							if (mode == "lost-absence" || mode == "child-canceled-parent-present" || mode == "tag-drift" || mode == "malformed-delete") && (out.Error.Code != "ambiguous_delete" || out.Error.Retryable) {
								t.Fatal("absence converted unknown deletion into another outcome")
							}
							if item.group == "pipeline" && wantDeletes == 1 {
								receipt := out.Error.Receipt.Deletion
								if wantSuccess {
									receipt = out.Data.Deletion
								}
								assertPipelineChildCancellationReceipt(t, receipt, "unverified")
								if (wantSuccess || mode == "lost-absence" || mode == "child-canceled-parent-present") && f.childPipelineStatus != "canceled" {
									t.Fatal("synthetic child cancellation was not exercised")
								}
							}
							if item.group == "release" {
								receipt := out.Error.Receipt.Deletion
								if wantSuccess {
									receipt = out.Data.Deletion
									if receipt.TagPostcondition != "unchanged" || tagReads != 2 || receipt.URL != deletionTestWeb+item.webPath || receipt.Tag != item.selector || !receipt.Acknowledged || !receipt.DeleteAttempted || receipt.DeleteStatus != 200 || receipt.Postcondition != "not_found" || reads != 3 || requests != 11 {
										t.Fatalf("release identity, acknowledgment or retained tag lost: receipt=%+v reads=%d requests=%d tagReads=%d", receipt, reads, requests, tagReads)
									}
								}
								if wantDeletes == 1 {
									assertReleaseCatalogUnpublicationReceipt(t, receipt, "unverified")
								} else if mode == "drift" {
									assertReleaseCatalogUnpublicationReceipt(t, receipt, "not_attempted")
								}
								wantCatalogState, wantVersions := "published", 1
								if wantSuccess || mode == "lost-absence" || mode == "tag-drift" || mode == "malformed-delete" {
									wantCatalogState, wantVersions = "unpublished", 0
								}
								if catalogState != wantCatalogState || catalogVersions != wantVersions {
									t.Fatalf("catalog state=%s versions=%d", catalogState, catalogVersions)
								}
							}
							preserved, err := os.ReadFile(f.unrelated)
							if err != nil || string(preserved) != "preserve" {
								t.Fatal("unrelated caller file changed")
							}
						})
					}
				})
			}
		})
	}
}
