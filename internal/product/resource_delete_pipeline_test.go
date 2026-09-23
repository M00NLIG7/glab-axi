package product

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"testing"

	runtimepkg "gl-axi/internal/runtime"
)

func assertPipelineChildCancellationReceipt(t *testing.T, r deletionTestReceipt, outcome string) {
	t.Helper()
	if r.ChildCancellation == nil || !r.ChildCancellation.Acknowledged || r.ChildCancellation.Outcome != outcome {
		t.Fatalf("child cancellation consent or uncertainty missing: %+v", r.ChildCancellation)
	}
	for _, effect := range []string{"delete_related_builds_logs_artifacts_triggers", "expire_pipeline_caches", "do_not_recursively_delete_child_pipelines", "may_cancel_surviving_child_pipelines"} {
		if !slices.Contains(r.IntendedEffects, effect) {
			t.Fatalf("missing pipeline effect %s: %v", effect, r.IntendedEffects)
		}
	}
}

func TestPipelineDeletionChildCancellationAcknowledgmentRefusals(t *testing.T) {
	for _, status := range []string{"running", "success"} {
		item := deletionCases()[1]
		item.body["status"] = status
		base := deletionReplaceFlag(item.args(), "--expected-status", status, false)
		withoutAck := deletionReplaceFlag(base, "--acknowledge-child-cancellation", "", true)
		for name, args := range map[string][]string{
			"missing":                     withoutAck,
			"sibling":                     deletionReplaceFlag(base, "--acknowledge-child-cancellation", deletionTestWeb+"/group/project/-/pipelines/89", false),
			"boolean":                     deletionReplaceFlag(base, "--acknowledge-child-cancellation", "true", false),
			"false":                       deletionReplaceFlag(base, "--acknowledge-child-cancellation", "false", false),
			"empty":                       deletionReplaceFlag(base, "--acknowledge-child-cancellation", "", false),
			"duplicate":                   append(append([]string(nil), base...), "--acknowledge-child-cancellation", deletionTestWeb+item.webPath),
			"broad-yes":                   append(append([]string(nil), withoutAck...), "--yes"),
			"missing-parent-confirmation": deletionReplaceFlag(base, item.confirmation, "", true),
			"wrong-parent-confirmation":   deletionReplaceFlag(base, item.confirmation, deletionTestWeb+"/group/project/-/pipelines/89", false),
		} {
			t.Run(status+"/"+name, func(t *testing.T) {
				f := newDeletionFixture(t, item, "success")
				code, out := f.run(context.Background(), args, true)
				if code == 0 || out.OK || out.Error.Retryable || f.deletes != 0 || len(f.requests) != 0 || f.lookup.Load() != 0 || f.keyring.calls.Load() != 0 || f.childPipelineStatus != "running" {
					t.Fatalf("exit=%d requests=%q writes=%d lookups=%d child=%s", code, f.requests, f.deletes, f.lookup.Load(), f.childPipelineStatus)
				}
				if name == "missing" || name == "sibling" {
					if out.Error.Code != "safety_violation" || !strings.Contains(out.Error.Message, "may cancel surviving child pipelines") || !strings.Contains(out.Error.Message, "snapshot cannot guarantee absence of child effects") {
						t.Fatalf("preflight refusal hid the additional effect: %+v", out.Error)
					}
				}
			})
		}
	}
}

func TestPipelineDeletionChildCancellationAcknowledgedOutcomes(t *testing.T) {
	for _, tc := range []struct {
		mode, action, parent, child, outcome string
		deletes                              int
	}{
		{"success", "deleted", "not_found", "canceled", "unverified", 1},
		{"drift", "not_applied", "not_checked", "running", "not_attempted", 0},
		{"lost-absence", "ambiguous", "not_found", "canceled", "unverified", 1},
		{"child-canceled-parent-present", "ambiguous", "present", "canceled", "unverified", 1},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			item := deletionCases()[1]
			item.body["status"] = "running"
			args := deletionReplaceFlag(item.args(), "--expected-status", "running", false)
			f := newDeletionFixture(t, item, tc.mode)
			code, out := f.run(context.Background(), args, true)
			wantSuccess := tc.action == "deleted"
			if (code == 0) != wantSuccess || out.OK != wantSuccess || out.Error.Retryable || f.deletes != tc.deletes || f.childPipelineStatus != tc.child {
				t.Fatalf("exit=%d writes=%d child=%s error=%+v", code, f.deletes, f.childPipelineStatus, out.Error)
			}
			r := out.Error.Receipt.Deletion
			if wantSuccess {
				r = out.Data.Deletion
			}
			if r.Action != tc.action || r.Postcondition != tc.parent || r.DeleteAttempted != (tc.deletes == 1) || r.Acknowledged != wantSuccess || r.URL != deletionTestWeb+item.webPath {
				t.Fatalf("misleading parent receipt: %+v", r)
			}
			assertPipelineChildCancellationReceipt(t, r, tc.outcome)
			if tc.action == "ambiguous" && (code != 6 || out.Error.Code != "ambiguous_delete") {
				t.Fatalf("lost uncertainty: exit=%d code=%s", code, out.Error.Code)
			}
			if tc.deletes == 1 && (f.reads != 3 || len(f.requests) > 9 || f.selections.Load() != 1) {
				t.Fatalf("unexpected reconciliation: reads=%d requests=%q selections=%d", f.reads, f.requests, f.selections.Load())
			}
			requests := len(f.requests)
			lookups := f.lookup.Load()
			code, out = f.run(context.Background(), deletionReplaceFlag(args, "--acknowledge-child-cancellation", "", true), true)
			if code == 0 || out.OK || out.Error.Code != "safety_violation" || len(f.requests) != requests || f.lookup.Load() != lookups || f.deletes != tc.deletes {
				t.Fatal("child cancellation acknowledgment was reused across invocations")
			}
		})
	}
}

func TestPipelineDeletionChildCancellationAcknowledgmentIsScoped(t *testing.T) {
	for _, item := range deletionCases() {
		if item.group == "pipeline" {
			continue
		}
		t.Run(item.name, func(t *testing.T) {
			f := newDeletionFixture(t, item, "success")
			args := append(item.args(), "--acknowledge-child-cancellation", deletionTestWeb+item.webPath)
			code, out := f.run(context.Background(), args, true)
			if code == 0 || out.OK || len(f.requests) != 0 || f.lookup.Load() != 0 {
				t.Fatal("pipeline acknowledgment authorized another resource")
			}
		})
	}
}

func TestPipelineDeletionHelpDisclosesChildCancellation(t *testing.T) {
	var stdout bytes.Buffer
	deps := Dependencies{Runtime: runtimepkg.Dependencies{Stdout: &stdout, Stderr: &bytes.Buffer{}, LookupEnv: func(string) (string, bool) { t.Fatal("help consulted credentials"); return "", false }}, NewDelegate: func() delegateClient { t.Fatal("help consulted official glab"); return nil }}
	if code := Run(context.Background(), []string{"pipeline", "delete", "--help"}, deps); code != 0 {
		t.Fatalf("help exit=%d", code)
	}
	for _, disclosure := range []string{"--acknowledge-child-cancellation URL", "--confirm-delete-pipeline URL", "may cancel surviving child pipelines", "even if parent deletion later fails", "Child pipelines are not recursively deleted", "regardless of observed status", "snapshot cannot guarantee absence of child effects", "receipts do not verify child cancellation"} {
		if !strings.Contains(stdout.String(), disclosure) {
			t.Fatalf("pipeline deletion help omitted %q", disclosure)
		}
	}
}
