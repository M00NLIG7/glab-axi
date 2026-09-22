package product

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func issueWriteArgs(t *testing.T, action string) []string {
	t.Helper()
	args := []string{"issue", action}
	if action != "create" {
		args = append(args, "42", "--expected-issue-id", "1001")
	}
	args = append(args, "--repo", "group/project", "--hostname", "gitlab.com", "--expected-project-id", "101", "--expected-url", issueEditTestURL, "--format", "json")
	private := func(name, value string) string {
		path := filepath.Join(t.TempDir(), name)
		if err := os.WriteFile(path, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	switch action {
	case "create":
		for i, v := range args {
			if v == issueEditTestURL {
				args[i] = "https://gitlab.com/group/project"
			}
		}
		args = append(args, "--title-file", private("title", "new title\n"), "--description-file", private("description", "new body"))
	case "comment", "note":
		args = append(args, "--body-file", private("body", "new body"))
	case "close":
		args = append(args, "--expected-state", "opened")
	case "reopen":
		args = append(args, "--expected-state", "closed")
	}
	return append(args, "--auth-source", "native")
}

func issueWriteBody(state string) []byte {
	return []byte(`{"id":1001,"iid":42,"project_id":101,"title":"new title","description":"new body","issue_type":"issue","state":"` + state + `","web_url":"https://gitlab.com/group/project/-/issues/42","updated_at":"2026-08-15T12:00:00Z"}`)
}
func issueNoteBody() []byte {
	return []byte(`{"id":3001,"project_id":101,"noteable_id":1001,"noteable_iid":42,"noteable_type":"Issue","body":"new body","system":false,"internal":false}`)
}
func issueWriteDelegate(action string) *fakeDelegate {
	before, after := "opened", "closed"
	if action == "reopen" {
		before, after = "closed", "opened"
	}
	d := &fakeDelegate{responses: map[glab.Operation][]glab.Response{}, errors: map[glab.Operation][]error{}}
	response := func(body []byte) glab.Response {
		return glab.Response{Body: body, Write: true, UpstreamVersion: glab.SupportedVersion}
	}
	project := response([]byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`))
	d.responses[glab.OpIssueWriteProject] = []glab.Response{project, project}
	d.responses[glab.OpIssueWriteView] = []glab.Response{response(issueWriteBody(before)), response(issueWriteBody(before)), response(issueWriteBody(after))}
	d.responses[glab.OpIssueCreate] = []glab.Response{response(issueWriteBody("opened"))}
	d.responses[glab.OpIssueNoteCreate] = []glab.Response{response(issueNoteBody())}
	d.responses[glab.OpIssueState] = []glab.Response{response(issueWriteBody(after))}
	return d
}

func decodeIssueWriteEnvelope(t *testing.T, body []byte) (bool, uxv1.Code, issueWriteReceipt) {
	t.Helper()
	var e struct {
		OK    bool             `json:"ok"`
		Data  issueWriteOutput `json:"data"`
		Error struct {
			Code      uxv1.Code        `json:"code"`
			Receipt   issueWriteOutput `json:"receipt"`
			Retryable bool             `json:"retryable"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		t.Fatalf("%v: %s", err, body)
	}
	if e.Error.Retryable {
		t.Fatalf("write must not suggest blind retry: %s", body)
	}
	if e.OK {
		return e.OK, e.Error.Code, e.Data.Write
	}
	return e.OK, e.Error.Code, e.Error.Receipt.Write
}

func TestIssueWriteSuccessAndExactPayload(t *testing.T) {
	for _, action := range []string{"create", "comment", "note", "close", "reopen"} {
		t.Run(action, func(t *testing.T) {
			d := issueWriteDelegate(action)
			out, stderr, deps := issueWriteTestDeps(t, d)
			if code := Run(context.Background(), issueWriteArgs(t, action), deps); code != 0 || stderr.Len() != 0 {
				t.Fatalf("exit=%d %s %s", code, out, stderr)
			}
			ok, _, r := decodeIssueWriteEnvelope(t, out.Bytes())
			if !ok || r.MutationAttempts != 1 || r.MutationResponse != "accepted" || r.AtomicPrecondition || r.RetrySafe || len(r.RequestedSHA256) != 64 || r.Identity.IssueID != 1001 {
				t.Fatalf("receipt=%+v", r)
			}
			want := map[string]any{"body": "new body"}
			if action == "create" {
				want = map[string]any{"title": "new title", "description": "new body", "issue_type": "issue"}
			}
			if action == "close" || action == "reopen" {
				want = map[string]any{"state_event": action}
				if r.Outcome != "state_observed" || r.Postcondition != "read_back" {
					t.Fatalf("receipt=%+v", r)
				}
			}
			if len(d.inputBodies) != 1 {
				t.Fatalf("native payload count=%d", len(d.inputBodies))
			}
			var got map[string]any
			if err := json.Unmarshal(d.inputBodies[0], &got); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("payload=%v want=%v", got, want)
			}
			for _, req := range d.requests {
				if req.Host != "gitlab.com" || req.Repo != "group/project" || req.ProjectID != 101 {
					t.Fatalf("request=%+v", req)
				}
				if req.InputFile != "" {
					t.Fatal("native request unexpectedly persisted its JSON payload")
				}
			}
		})
	}
}

func TestIssueWriteAmbiguityNeverSearchesOrRetries(t *testing.T) {
	for _, action := range []string{"create", "comment", "close", "reopen"} {
		for _, failure := range []string{"timeout", "unframed rejection", "malformed", "wrong identity", "oversized", "missing identity", "duplicate identity", "wrong body", "non-UTF-8"} {
			t.Run(action+"/"+failure, func(t *testing.T) {
				d := issueWriteDelegate(action)
				op := glab.OpIssueCreate
				if action == "comment" {
					op = glab.OpIssueNoteCreate
				} else if action == "close" || action == "reopen" {
					op = glab.OpIssueState
				}
				switch failure {
				case "timeout":
					d.errors[op] = []error{uxv1.Wrap(uxv1.CodeUpstream, "controlled", context.DeadlineExceeded)}
				case "unframed rejection":
					d.errors[op] = []error{uxv1.NewError(uxv1.CodeForbidden, "controlled")}
				case "malformed":
					d.responses[op][0].Body = []byte("invalid")
				case "wrong identity":
					d.responses[op][0].Body = []byte(strings.ReplaceAll(string(d.responses[op][0].Body), `"project_id":101`, `"project_id":999`))
				case "oversized":
					d.responses[op][0].Body = []byte(strings.Repeat("x", limits.MaxJSONPageBytes+1))
				case "missing identity":
					d.responses[op][0].Body = []byte(`{}`)
				case "duplicate identity":
					d.responses[op][0].Body = []byte(strings.Replace(string(d.responses[op][0].Body), `{`, `{"id":999,`, 1))
				case "wrong body":
					if op == glab.OpIssueState {
						d.responses[op][0].Body = issueWriteBody("unexpected")
					} else {
						d.responses[op][0].Body = []byte(strings.ReplaceAll(string(d.responses[op][0].Body), "new body", "other body"))
					}
				case "non-UTF-8":
					d.responses[op][0].Body = []byte{255}
				}
				out, _, deps := issueWriteTestDeps(t, d)
				if code := Run(context.Background(), issueWriteArgs(t, action), deps); code != 6 {
					t.Fatalf("exit=%d %s", code, out)
				}
				ok, code, r := decodeIssueWriteEnvelope(t, out.Bytes())
				if ok || r.Outcome != "ambiguous" || r.MutationResponse != "unconfirmed" || r.MutationAttempts != 1 || (code != uxv1.CodeAmbiguousCreate && code != uxv1.CodeAmbiguousUpdate) || r.NoteID != 0 {
					t.Fatalf("receipt=%+v code=%s", r, code)
				}
				if countOperation(d.requests, op) != 1 || len(d.inputBodies) != 1 {
					t.Fatalf("retried: %+v", d.requests)
				}
				wantReads := 0
				if action == "comment" {
					wantReads = 2
				} else if action != "create" {
					wantReads = 3
				}
				if countOperation(d.requests, glab.OpIssueWriteView) != wantReads || len(d.requests) != 3+wantReads {
					t.Fatalf("unexpected reconciliation: %+v", d.requests)
				}
			})
		}
	}
}

func TestIssueWriteDefiniteRejection(t *testing.T) {
	for _, action := range []string{"create", "comment", "close", "reopen"} {
		for _, status := range []int{400, 401, 403, 404, 409, 422, 429} {
			t.Run(action+"/"+strconv.Itoa(status), func(t *testing.T) {
				d := issueWriteDelegate(action)
				op := glab.OpIssueState
				if action == "create" {
					op = glab.OpIssueCreate
				}
				if action == "comment" {
					op = glab.OpIssueNoteCreate
				}
				rejection, _ := uxv1.NewHTTPRejection(status)
				d.errors[op] = []error{rejection}
				out, _, deps := issueWriteTestDeps(t, d)
				if code := Run(context.Background(), issueWriteArgs(t, action), deps); code == 0 {
					t.Fatal("accepted rejection")
				}
				_, code, r := decodeIssueWriteEnvelope(t, out.Bytes())
				if code != rejection.Code || r.Outcome != "rejected" || r.MutationResponse != "rejected" || len(d.inputBodies) != 1 || countOperation(d.requests, glab.OpIssueWriteView) > 2 {
					t.Fatalf("receipt=%+v code=%s", r, code)
				}
			})
		}
	}
}

func TestIssueStateNoopAndDrift(t *testing.T) {
	for _, scenario := range []string{"noop", "expected mismatch", "adjacent drift", "post drift", "unreadable post", "wrong post identity"} {
		t.Run(scenario, func(t *testing.T) {
			d := issueWriteDelegate("close")
			args := issueWriteArgs(t, "close")
			wantExit, wantWrites := 6, 0
			switch scenario {
			case "noop":
				args = replaceIssueWriteArg(args, "--expected-state", "closed")
				for i := range d.responses[glab.OpIssueWriteView] {
					d.responses[glab.OpIssueWriteView][i].Body = issueWriteBody("closed")
				}
				wantExit = 0
			case "expected mismatch":
				args = replaceIssueWriteArg(args, "--expected-state", "closed")
			case "adjacent drift":
				d.responses[glab.OpIssueWriteView][1].Body = issueWriteBody("closed")
			case "post drift":
				d.responses[glab.OpIssueWriteView][2].Body = issueWriteBody("opened")
				wantWrites = 1
			case "unreadable post":
				d.errors[glab.OpIssueWriteView] = []error{nil, nil, errors.New("untrusted-provider-detail")}
				wantWrites = 1
			case "wrong post identity":
				d.responses[glab.OpIssueWriteView][2].Body = []byte(strings.ReplaceAll(string(issueWriteBody("closed")), `"id":1001`, `"id":1002`))
				wantWrites = 1
			}
			out, _, deps := issueWriteTestDeps(t, d)
			if code := Run(context.Background(), args, deps); code != wantExit {
				t.Fatalf("exit=%d want=%d %s", code, wantExit, out)
			}
			if len(d.inputBodies) != wantWrites || strings.Contains(out.String(), "untrusted-provider-detail") {
				t.Fatalf("writes=%d output=%s", len(d.inputBodies), out)
			}
			_, _, r := decodeIssueWriteEnvelope(t, out.Bytes())
			if scenario == "noop" && (r.Outcome != "unchanged" || r.MutationAttempts != 0 || r.Postcondition != "preflight") {
				t.Fatalf("receipt=%+v", r)
			}
			if scenario == "post drift" && (r.Outcome != "conflict" || r.MutationResponse != "accepted" || r.ObservedState != "opened") {
				t.Fatalf("receipt=%+v", r)
			}
		})
	}
}

func replaceIssueWriteArg(args []string, key, value string) []string {
	for i := range args {
		if args[i] == key {
			args[i+1] = value
			return args
		}
	}
	return append(args, key, value)
}

func TestIssueWriteRejectsInputBeforeChild(t *testing.T) {
	for _, action := range []string{"create", "comment", "close", "reopen"} {
		for _, invalid := range []string{"host", "repo", "url", "project ID", "issue ID", "iid", "inline body", "reason", "bundled comment", "duplicate", "file", "quick action", "empty", "bound", "symlink", "utf8"} {
			t.Run(action+"/"+invalid, func(t *testing.T) {
				args := issueWriteArgs(t, action)
				switch invalid {
				case "host":
					args = replaceIssueWriteArg(args, "--hostname", "https://gitlab.com")
				case "repo":
					args = replaceIssueWriteArg(args, "--repo", "group/../project")
				case "url":
					args = replaceIssueWriteArg(args, "--expected-url", "https://evil.example/group/project/-/issues/42")
				case "project ID":
					args = replaceIssueWriteArg(args, "--expected-project-id", "0101")
				case "issue ID":
					args = replaceIssueWriteArg(args, "--expected-issue-id", "0")
				case "iid":
					if action == "create" {
						args = append(args, "42")
					} else {
						args[2] = "042"
					}
				case "inline body":
					args = append(args, "--body", "untrusted content")
				case "reason":
					args = append(args, "--reason", "completed")
				case "bundled comment":
					args = append(args, "--comment", "text")
				case "duplicate":
					args = append(args, "--repo", "group/project")
				default:
					flag := "--body-file"
					if action == "create" {
						flag = "--description-file"
					}
					if action == "close" || action == "reopen" {
						args = append(args, flag, "invalid")
						break
					}
					path := ""
					for i := range args {
						if args[i] == flag {
							path = args[i+1]
						}
					}
					switch invalid {
					case "file":
						args = replaceIssueWriteArg(args, flag, "relative")
					case "quick action":
						if err := os.WriteFile(path, []byte("hello\r\n```\n \t/close\n```"), 0600); err != nil {
							t.Fatal(err)
						}
					case "empty":
						if action == "create" {
							flag = "--title-file"
							for i := range args {
								if args[i] == flag {
									path = args[i+1]
								}
							}
						}
						if err := os.WriteFile(path, []byte(" \n"), 0600); err != nil {
							t.Fatal(err)
						}
					case "bound":
						if err := os.WriteFile(path, []byte(strings.Repeat("x", limits.MaxDescriptionBytes+1)), 0600); err != nil {
							t.Fatal(err)
						}
					case "symlink":
						link := path + "-link"
						if err := os.Symlink(path, link); err != nil {
							t.Skip(err)
						}
						args = replaceIssueWriteArg(args, flag, link)
					case "utf8":
						if err := os.WriteFile(path, []byte{255}, 0600); err != nil {
							t.Fatal(err)
						}
					}
				}
				d := issueWriteDelegate(action)
				out, _, deps := issueWriteTestDeps(t, d)
				if code := Run(context.Background(), args, deps); code == 0 || len(d.requests) != 0 {
					t.Fatalf("exit=%d requests=%+v output=%s", code, d.requests, out)
				}
			})
		}
	}
}

func TestIssueWriteTargetMismatchAndCancellation(t *testing.T) {
	for _, scenario := range []string{"project ID", "project URL", "project path", "issue ID", "issue URL", "issue IID", "issue project", "project drift", "canceled preflight", "canceled write"} {
		t.Run(scenario, func(t *testing.T) {
			d := issueWriteDelegate("comment")
			switch scenario {
			case "project ID":
				d.responses[glab.OpIssueWriteProject][0].Body = []byte(`{"id":999}`)
			case "project URL":
				d.responses[glab.OpIssueWriteProject][0].Body = []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://other.example/group/project"}`)
			case "project path":
				d.responses[glab.OpIssueWriteProject][0].Body = []byte(`{"id":101,"path_with_namespace":"other/project","web_url":"https://gitlab.com/group/project"}`)
			case "project drift":
				d.responses[glab.OpIssueWriteProject][1].Body = []byte(`{"id":999}`)
			case "issue ID":
				d.responses[glab.OpIssueWriteView][0].Body = []byte(strings.ReplaceAll(string(issueWriteBody("opened")), `"id":1001`, `"id":1002`))
			case "issue IID":
				d.responses[glab.OpIssueWriteView][0].Body = []byte(strings.ReplaceAll(string(issueWriteBody("opened")), `"iid":42`, `"iid":43`))
			case "issue project":
				d.responses[glab.OpIssueWriteView][0].Body = []byte(strings.ReplaceAll(string(issueWriteBody("opened")), `"project_id":101`, `"project_id":102`))
			case "issue URL":
				d.responses[glab.OpIssueWriteView][0].Body = []byte(strings.ReplaceAll(string(issueWriteBody("opened")), "gitlab.com", "other.example"))
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if strings.HasPrefix(scenario, "canceled") {
				d.doFunc = func(ctx context.Context, r glab.Request) (glab.Response, error, bool) {
					if scenario == "canceled preflight" || r.Operation == glab.OpIssueNoteCreate {
						cancel()
						return glab.Response{Write: r.Operation == glab.OpIssueNoteCreate}, uxv1.NewError(uxv1.CodeCanceled, "canceled"), true
					}
					return glab.Response{}, nil, false
				}
			}
			out, _, deps := issueWriteTestDeps(t, d)
			if code := Run(ctx, issueWriteArgs(t, "comment"), deps); code == 0 {
				t.Fatal(out.String())
			}
			want := 0
			if scenario == "canceled write" {
				want = 1
				_, code, r := decodeIssueWriteEnvelope(t, out.Bytes())
				if code != uxv1.CodeAmbiguousCreate || r.Outcome != "ambiguous" {
					t.Fatal(out.String())
				}
			}
			if len(d.inputBodies) != want {
				t.Fatalf("writes=%d want=%d %s", len(d.inputBodies), want, out)
			}
		})
	}
}
