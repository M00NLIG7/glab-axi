package product

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"gl-axi/internal/contract/uxv1"
)

func TestNativeHiddenVariableMutationEvidence(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation ACL boundary remains unavailable")
	}
	for _, operation := range []struct{ name, action, class string }{
		{"create", "set", "absent"}, {"rotate", "set", "hidden"}, {"delete", "delete", "hidden"},
	} {
		for _, mode := range []string{"", "same-value", "lost-response", "applied-error", "uncertain", "rejected", "post-project-drift", "post-read-failure", "unapplied-success", "unexpected-status", "truncated-response"} {
			if mode == "truncated-response" && operation.action == "delete" {
				continue
			}
			t.Run(operation.name+"/"+mode, func(t *testing.T) {
				f := newNativeVariableFixture(t, operation.class, mode, false)
				if mode == "same-value" && operation.class == "hidden" {
					f.state[0]["value"] = f.newValue
				}
				stdout, stderr, deps, _, _ := f.deps(t, false)
				exit := Run(context.Background(), f.args("secret", operation.action, operation.class, "json"), deps)
				f.assertConfidential(t, stdout.String(), stderr.String())
				wantExit, wantOutcome := 6, "ambiguous"
				switch mode {
				case "", "same-value":
					wantExit, wantOutcome = 0, "postcondition_observed"
				case "rejected":
					wantExit, wantOutcome = 4, "rejected"
				case "unapplied-success":
					if operation.name == "rotate" {
						wantExit, wantOutcome = 0, "postcondition_observed"
					}
				}
				var envelope struct {
					Data  variableMutationOutput `json:"data"`
					Error *struct {
						Code      uxv1.Code              `json:"code"`
						Retryable bool                   `json:"retryable"`
						Receipt   variableMutationOutput `json:"receipt"`
					} `json:"error"`
				}
				if exit != wantExit || json.Unmarshal(stdout.Bytes(), &envelope) != nil {
					t.Fatalf("mutation exit=%d want=%d or invalid receipt", exit, wantExit)
				}
				receipt := envelope.Data.Variable
				if wantExit != 0 {
					if envelope.Error == nil || envelope.Error.Retryable {
						t.Fatal("missing non-retryable error receipt")
					}
					wantCode := uxv1.CodeAmbiguousVariable
					if mode == "rejected" {
						wantCode = uxv1.CodeForbidden
					}
					if envelope.Error.Code != wantCode {
						t.Fatal("uncertain write misreported as a definite rejection")
					}
					receipt = envelope.Error.Receipt.Variable
				}
				acknowledged := mode == "" || mode == "same-value" || mode == "post-project-drift" || mode == "post-read-failure" || mode == "unapplied-success"
				if receipt.Action != operation.action || receipt.Outcome != wantOutcome || receipt.ProviderAcknowledged != acknowledged || receipt.ValueVerification != "unavailable_hidden" || receipt.AtomicPrecondition || !receipt.MutationAttempted {
					t.Fatal("hidden mutation overstated acknowledgment or value verification")
				}
				if receipt.ProjectID != 101 || receipt.ProjectURL != f.web+"/group/project" || receipt.Key != "KEY" || receipt.EnvironmentScope != "production" {
					t.Fatal("receipt identity changed")
				}
				wantReconciliation := "metadata_observed"
				if operation.action == "delete" {
					wantReconciliation = "absence_observed"
				}
				if mode == "post-project-drift" || mode == "post-read-failure" || (mode == "rejected" || mode == "uncertain" || mode == "unapplied-success") && operation.name != "rotate" {
					wantReconciliation = "not_observed"
				}
				if receipt.Reconciliation != wantReconciliation || (receipt.State != nil) != (wantReconciliation == "metadata_observed") {
					t.Fatal("receipt confused metadata, absence, and unavailable evidence")
				}
				f.mu.Lock()
				writes, reads := f.writes, f.inventoryReads
				f.mu.Unlock()
				if writes != 1 || reads != 3 {
					t.Fatal("mutation or bounded reconciliation was skipped or replayed")
				}
			})
		}
	}
}

func TestNativeHiddenVariableObservableGuards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation ACL boundary remains unavailable")
	}
	for _, guard := range []struct {
		field string
		value any
	}{
		{"key", "OTHER"}, {"environment_scope", "staging"}, {"variable_type", "file"},
		{"masked", false}, {"hidden", false}, {"protected", true}, {"raw", false},
	} {
		for _, phase := range []string{"initial", "metadata-drift", "post-metadata-drift"} {
			for _, action := range []string{"set", "delete"} {
				if phase == "post-metadata-drift" && action == "delete" {
					continue
				}
				t.Run(guard.field+"/"+phase+"/"+action, func(t *testing.T) {
					f := newNativeVariableFixture(t, "hidden", phase, false)
					f.driftField, f.driftValue = guard.field, guard.value
					if phase == "initial" {
						f.state[0][guard.field] = guard.value
					}
					stdout, stderr, deps, _, _ := f.deps(t, false)
					if Run(context.Background(), f.args("secret", action, "hidden", "json"), deps) == 0 {
						t.Fatal("hidden metadata drift was accepted")
					}
					f.assertConfidential(t, stdout.String(), stderr.String())
					f.mu.Lock()
					writes := f.writes
					f.mu.Unlock()
					wantWrites := 0
					if phase == "post-metadata-drift" {
						wantWrites = 1
						var envelope struct {
							Error struct {
								Code    uxv1.Code              `json:"code"`
								Receipt variableMutationOutput `json:"receipt"`
							} `json:"error"`
						}
						if json.Unmarshal(stdout.Bytes(), &envelope) != nil || envelope.Error.Code != uxv1.CodeAmbiguousVariable || envelope.Error.Receipt.Variable.Reconciliation != "not_observed" || !envelope.Error.Receipt.Variable.ProviderAcknowledged {
							t.Fatal("post-mutation drift misreported")
						}
					}
					if writes != wantWrites {
						t.Fatal("unsafe mutation or replay after observable drift")
					}
				})
			}
		}
	}
}

func TestNativeVariableProjectDriftPreventsHiddenMutation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation ACL boundary remains unavailable")
	}
	for _, action := range []string{"set", "delete"} {
		t.Run(action, func(t *testing.T) {
			f := newNativeVariableFixture(t, "hidden", "project-drift", false)
			stdout, stderr, deps, _, _ := f.deps(t, false)
			if Run(context.Background(), f.args("secret", action, "hidden", "json"), deps) != 9 {
				t.Fatal("project drift did not fail closed")
			}
			f.assertConfidential(t, stdout.String(), stderr.String())
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatal("changed project identity reached mutation")
			}
		})
	}
}

func TestNativeReadableVariableValueGuards(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation ACL boundary remains unavailable")
	}
	for _, class := range []string{"ordinary", "masked", "protected"} {
		for _, value := range []any{nil, "", "different-synthetic-value"} {
			t.Run(class, func(t *testing.T) {
				f := newNativeVariableFixture(t, class, "", false)
				f.state[0]["value"] = value
				group := "secret"
				if class == "ordinary" {
					group = "variable"
				}
				args := f.args(group, "delete", class, "json")
				if class == "protected" {
					f.state[0]["protected"] = true
					args = replaceVariableArg(args, "--expected-protected", "true")
				}
				stdout, stderr, deps, _, _ := f.deps(t, false)
				if Run(context.Background(), args, deps) != 6 {
					t.Fatal("unavailable or unequal unhidden value bypassed the precondition")
				}
				f.assertConfidential(t, stdout.String(), stderr.String())
				f.mu.Lock()
				writes := f.writes
				f.mu.Unlock()
				if writes != 0 {
					t.Fatal("failed private value guard reached mutation")
				}
			})
		}
	}
}
