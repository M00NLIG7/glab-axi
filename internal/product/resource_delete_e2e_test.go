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
			for _, item := range deletionCases() {
				t.Run(item.name, func(t *testing.T) {
					modes := []string{"success", "missing-confirmation", "missing-native", "pre-404", "pre-403", "drift", "delete-403", "lost-absence", "redirect-307", "redirect-308"}
					if item.group == "pipeline" {
						item.body["status"] = "running"
						item.expected = deletionReplaceFlag(item.expected, "--expected-status", "running", false)
						modes = append(modes, "missing-child-acknowledgment", "wrong-child-acknowledgment", "child-canceled-parent-present")
					}
					if item.group == "release" {
						modes = append(modes, "missing-catalog-acknowledgment", "wrong-catalog-acknowledgment", "pre-tag-drift", "tag-drift", "malformed-delete")
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
							case "missing-confirmation", "missing-native", "missing-child-acknowledgment", "wrong-child-acknowledgment", "missing-catalog-acknowledgment", "wrong-catalog-acknowledgment", "pre-404", "pre-403", "pre-tag-drift", "drift":
								wantDeletes = 0
							}
							f.mu.Lock()
							deletes, unexpected, credential, requests := f.deletes, f.forbiddenPaths, f.wrongCredential, len(f.requests)
							catalogState, catalogVersions, tagReads := f.catalogState, f.catalogVersions, f.tagReads
							f.mu.Unlock()
							if deletes != wantDeletes || unexpected != 0 || credential || requests > 12 {
								t.Fatalf("writes=%d unexpected=%d credential=%v requests=%d", deletes, unexpected, credential, requests)
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
									if receipt.TagPostcondition != "unchanged" || tagReads != 2 {
										t.Fatal("release success did not verify the retained tag")
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
