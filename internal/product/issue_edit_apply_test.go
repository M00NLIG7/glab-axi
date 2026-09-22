package product

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func TestIssueEditSuccessfulSingleTypedMutation(t *testing.T) {
	for _, field := range []string{"title", "description", "clear description", "labels", "combined"} {
		t.Run(field, func(t *testing.T) {
			before, after := issueEditFixture(), issueEditFixture()
			after.UpdatedAt = timePointer(issueEditNextTime)
			var title, description *string
			var add, remove []string
			want := map[string]any{}
			if field == "title" || field == "combined" {
				after.Title = "new title"
				title = &after.Title
				want["title"] = after.Title
			}
			if field == "description" || field == "clear description" || field == "combined" {
				after.Description = "new body"
				if field == "clear description" {
					after.Description = ""
				}
				description = &after.Description
				want["description"] = after.Description
			}
			if field == "labels" || field == "combined" {
				add, remove = []string{"triage", "keep"}, []string{"bug", "unused"}
				after.Labels = []string{"keep", "triage"}
				want["add_labels"], want["remove_labels"] = "triage", "bug"
			}
			delegate := issueEditDelegate(before, before, issueEditCatalog())
			delegate.responses[glab.OpIssueEditUpdate] = []glab.Response{issueEditResponse(after)}
			delegate.responses[glab.OpIssueEditView] = append(delegate.responses[glab.OpIssueEditView], issueEditResponse(after))
			delegate.responses[glab.OpIssueEditLabelList] = append(delegate.responses[glab.OpIssueEditLabelList], issueEditResponse(issueEditCatalog()))
			stdout, _, deps := productTestDeps(t, delegate)
			if code := Run(context.Background(), issueEditArgs(t, title, description, add, remove, false, "json"), deps); code != 0 {
				t.Fatalf("exit=%d output=%s", code, stdout)
			}
			assertIssueEditOneMutation(t, delegate)
			var actual map[string]any
			if err := json.Unmarshal(delegate.inputBodies[0], &actual); err != nil || !reflect.DeepEqual(actual, want) {
				t.Fatalf("payload=%s want=%v error=%v", delegate.inputBodies[0], want, err)
			}
			var envelope struct {
				Data issueEditOutput `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			edit := envelope.Data.Edit
			if edit.Action != "updated" || edit.Outcome != "observed_applied" || edit.Concurrency != "best_effort" || edit.Warning != issueEditRaceWarning || edit.ResultingUpdatedAt != issueEditNextTime.Format(time.RFC3339Nano) {
				t.Fatalf("receipt=%#v", edit)
			}
			if countOperation(delegate.requests, glab.OpIssueEditView) != 3 {
				t.Fatal("missing canonical verification")
			}
		})
	}
}

func TestIssueEditReconciliationNeverRetries(t *testing.T) {
	for _, failure := range []string{"lost response", "timeout", "canceled transport", "400", "401", "403", "404", "409", "429", "500", "redirect", "malformed", "incomplete", "oversized"} {
		for _, observed := range []bool{true, false} {
			t.Run(failure+"/observed="+map[bool]string{true: "yes", false: "no"}[observed], func(t *testing.T) {
				before, after := issueEditFixture(), issueEditFixture()
				after.Title = "new title"
				after.UpdatedAt = timePointer(issueEditNextTime)
				delegate := issueEditDelegate(before, before, nil)
				canonical := before
				if observed {
					canonical = after
				}
				delegate.responses[glab.OpIssueEditView] = append(delegate.responses[glab.OpIssueEditView], issueEditResponse(canonical))
				switch failure {
				case "malformed":
					delegate.responses[glab.OpIssueEditUpdate] = []glab.Response{{Body: []byte(`{"id":`)}}
				case "incomplete":
					delegate.responses[glab.OpIssueEditUpdate] = []glab.Response{{Body: []byte(`{"id":1001}`)}}
				case "oversized":
					delegate.responses[glab.OpIssueEditUpdate] = []glab.Response{{Body: []byte(strings.Repeat(" ", limits.MaxJSONPageBytes+1))}}
				default:
					delegate.errors = map[glab.Operation][]error{glab.OpIssueEditUpdate: {errors.New("synthetic transport " + failure)}}
				}
				stdout, _, deps := productTestDeps(t, delegate)
				code := Run(context.Background(), issueEditArgs(t, stringPointer(after.Title), nil, nil, nil, false, "json"), deps)
				assertIssueEditOneMutation(t, delegate)
				if observed {
					if code != 0 || !strings.Contains(stdout.String(), `"action":"reconciled_update"`) {
						t.Fatalf("exit=%d output=%s", code, stdout)
					}
				} else {
					assertIssueEditAmbiguous(t, code, stdout.Bytes())
				}
			})
		}
	}
}

func TestIssueEditPostWriteDriftAndUnverifiableOutcomes(t *testing.T) {
	for _, phase := range []string{"response", "canonical"} {
		for _, drift := range []string{"id", "iid", "project", "host", "url", "state", "title", "description", "labels", "older timestamp", "missing timestamp"} {
			t.Run(phase+"/"+drift, func(t *testing.T) {
				before, after := issueEditFixture(), issueEditFixture()
				after.Title = "new title"
				after.UpdatedAt = timePointer(issueEditNextTime)
				bad := after
				switch drift {
				case "id":
					bad.ID++
				case "iid":
					bad.IID++
				case "project":
					bad.ProjectID++
				case "host":
					bad.WebURL = "https://other.example/group/project/-/issues/42"
				case "url":
					bad.WebURL += "?different=1"
				case "state":
					bad.State = "closed"
				case "title":
					bad.Title = "concurrent title"
				case "description":
					bad.Description = "concurrent description"
				case "labels":
					bad.Labels = []string{"keep", "bug", "concurrent"}
				case "older timestamp":
					bad.UpdatedAt = timePointer(issueEditTestTime.Add(-time.Second))
				case "missing timestamp":
					bad.UpdatedAt = timePointer(time.Time{})
				}
				delegate := issueEditDelegate(before, before, nil)
				response, canonical := after, after
				if phase == "response" {
					response = bad
				} else {
					canonical = bad
				}
				delegate.responses[glab.OpIssueEditUpdate] = []glab.Response{issueEditResponse(response)}
				delegate.responses[glab.OpIssueEditView] = append(delegate.responses[glab.OpIssueEditView], issueEditResponse(canonical))
				stdout, _, deps := productTestDeps(t, delegate)
				code := Run(context.Background(), issueEditArgs(t, stringPointer(after.Title), nil, nil, nil, false, "json"), deps)
				assertIssueEditOneMutation(t, delegate)
				assertIssueEditAmbiguous(t, code, stdout.Bytes())
			})
		}
	}
}

func TestIssueEditLabelIdentityDrift(t *testing.T) {
	for _, phase := range []string{"preflight", "postwrite"} {
		for _, drift := range []string{"renamed", "reused", "ambiguous"} {
			t.Run(phase+"/"+drift, func(t *testing.T) {
				before, after := issueEditFixture(), issueEditFixture()
				after.Labels = []string{"bug", "keep", "triage"}
				catalog := issueEditCatalog()
				switch drift {
				case "renamed":
					catalog[0].Name = "renamed"
				case "reused":
					catalog[0].ID = 99
				case "ambiguous":
					catalog = append(catalog, issueEditLabel{ID: 99, Name: "triage"})
				}
				delegate := issueEditDelegate(before, before, issueEditCatalog())
				if phase == "preflight" {
					delegate.responses[glab.OpIssueEditLabelList][1] = issueEditResponse(catalog)
				} else {
					delegate.responses[glab.OpIssueEditLabelList] = append(delegate.responses[glab.OpIssueEditLabelList], issueEditResponse(catalog))
					delegate.responses[glab.OpIssueEditUpdate] = []glab.Response{issueEditResponse(after)}
					delegate.responses[glab.OpIssueEditView] = append(delegate.responses[glab.OpIssueEditView], issueEditResponse(after))
				}
				stdout, _, deps := productTestDeps(t, delegate)
				code := Run(context.Background(), issueEditArgs(t, nil, nil, []string{"triage"}, nil, false, "json"), deps)
				if phase == "preflight" {
					if code == 0 {
						t.Fatal("drift accepted")
					}
					assertIssueEditNoMutation(t, delegate)
				} else {
					assertIssueEditOneMutation(t, delegate)
					assertIssueEditAmbiguous(t, code, stdout.Bytes())
				}
			})
		}
	}
}

func TestIssueEditReconciliationFailureAndCancellation(t *testing.T) {
	for _, mode := range []string{"read error", "malformed", "missing", "oversized", "cancel", "deadline", "read deadline"} {
		t.Run(mode, func(t *testing.T) {
			before, after := issueEditFixture(), issueEditFixture()
			after.Title = "new title"
			delegate := issueEditDelegate(before, before, nil)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "deadline" || mode == "read deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 100*time.Millisecond)
				defer stop()
			}
			delegate.doFunc = func(child context.Context, request glab.Request) (glab.Response, error, bool) {
				if request.Operation == glab.OpIssueEditUpdate {
					deadline, ok := child.Deadline()
					if !ok || time.Until(deadline) > limits.IssueEditMutation {
						t.Fatal("unbounded PUT")
					}
					if mode == "cancel" {
						cancel()
					}
					if mode == "deadline" {
						<-child.Done()
					}
					return issueEditResponse(after), nil, true
				}
				if request.Operation == glab.OpIssueEditView && countOperation(delegate.requests, glab.OpIssueEditView) == 3 {
					deadline, ok := child.Deadline()
					if !ok || time.Until(deadline) > limits.IssueEditReconcile {
						t.Fatal("unbounded reconciliation")
					}
					switch mode {
					case "read error":
						return glab.Response{}, errors.New("synthetic read failure"), true
					case "malformed":
						return glab.Response{Body: []byte(`{`)}, nil, true
					case "missing":
						return glab.Response{Body: []byte(`{}`)}, nil, true
					case "oversized":
						return glab.Response{Body: []byte(strings.Repeat(" ", limits.MaxJSONPageBytes+1))}, nil, true
					case "read deadline":
						<-child.Done()
						return issueEditResponse(after), nil, true
					default:
						t.Fatal("read after cancellation")
					}
				}
				return glab.Response{}, nil, false
			}
			stdout, _, deps := productTestDeps(t, delegate)
			code := Run(ctx, issueEditArgs(t, stringPointer(after.Title), nil, nil, nil, false, "json"), deps)
			assertIssueEditOneMutation(t, delegate)
			assertIssueEditAmbiguous(t, code, stdout.Bytes())
		})
	}
}

func assertIssueEditOneMutation(t *testing.T, delegate *fakeDelegate) {
	t.Helper()
	if countOperation(delegate.requests, glab.OpIssueEditUpdate) != 1 || len(delegate.inputBodies) != 1 || len(delegate.inputModes) != 1 || delegate.inputErr != nil || delegate.inputModes[0].Perm() != 0o600 {
		t.Fatalf("mutation boundary: %#v", delegate)
	}
	for _, request := range delegate.requests {
		if request.Operation == glab.OpIssueEditUpdate {
			if request.ID != 101 || request.IID != 42 || request.Host != "gitlab.com" || request.Repo != "group/project" {
				t.Fatalf("wrong mutation target: %#v", request)
			}
			if _, err := os.Stat(request.InputFile); !os.IsNotExist(err) {
				t.Fatal("private mutation input not removed")
			}
		}
	}
}

func assertIssueEditAmbiguous(t *testing.T, code int, output []byte) {
	t.Helper()
	var envelope struct {
		OK    bool `json:"ok"`
		Error struct {
			Code      uxv1.Code       `json:"code"`
			Retryable bool            `json:"retryable"`
			Receipt   issueEditOutput `json:"receipt"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output, &envelope); err != nil {
		t.Fatal(err)
	}
	edit := envelope.Error.Receipt.Edit
	if code == 0 || envelope.OK || envelope.Error.Code != uxv1.CodeAmbiguousUpdate || envelope.Error.Retryable || edit.Action != "ambiguous" || edit.Outcome != "unknown" || edit.ResultingUpdatedAt != "" || edit.Warning != issueEditRaceWarning || strings.Contains(string(output), `"data":`) {
		t.Fatalf("exit=%d output=%s", code, output)
	}
}
