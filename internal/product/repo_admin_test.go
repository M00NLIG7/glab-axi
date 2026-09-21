package product

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func adminTestProject(path string, id int64) adminProviderProject {
	return adminProviderProject{adminProject: adminProject{
		ID: id, Path: path, URL: "https://gitlab.example.invalid/" + path,
		Namespace: adminNamespace{ID: 21, FullPath: path[:strings.LastIndex(path, "/")], Kind: "group"}, UpdatedAt: "2026-09-01T12:00:00Z",
		adminSettings: adminSettings{Description: "old", Visibility: "private", DefaultBranch: "main", Issues: "enabled", Wiki: "private", PipelineRequired: true, DiscussionsRequired: true, MergeMethod: "merge", SquashOption: "default_off"},
	}, ImportStatus: "finished"}
}
func adminTestBody(v any) []byte {
	b, _ := json.Marshal(v)
	if p, ok := v.(adminProviderProject); ok {
		var fields map[string]any
		_ = json.Unmarshal(b, &fields)
		fields["name"] = p.Path[strings.LastIndex(p.Path, "/")+1:]
		b, _ = json.Marshal(fields)
	}
	return b
}
func adminTestArgs(action string) []string {
	args := []string{"repo", action, "-R", "team/sub/project", "--hostname", "gitlab.example.invalid", "--namespace-id", "21", "--namespace-kind", "group", "--expected-user-id", "7", "--expected-username", "tester", "--allow-project-admin", "--format", "json"}
	if action != "edit" {
		args = append(args, "--visibility", "private")
	}
	if action == "fork" {
		args = append(args, "--destination", "team/sub/fork", "--expected-source-id", "101")
	}
	return args
}
func adminTestFile(t *testing.T, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func adminEditArgs(t *testing.T, before adminProviderProject, flags ...string) []string {
	return append(append(adminTestArgs("edit"), "--expected-state-file", adminTestFile(t, adminTestBody(before.adminProject)), "--accept-non-atomic"), flags...)
}

// The stateful fake consumes the same typed interface as the executable. It
// allows precise faults between observations and counts every intended write.
type adminTestState struct {
	before, after                    adminProviderProject
	writes, reads, users, namespaces int
	writeErr                         error
	writeBody                        []byte
	readErr                          error
	drift                            bool
	wrongUser, wrongNS               bool
	statuses                         []string
}

func (s *adminTestState) delegate() *fakeDelegate {
	return &fakeDelegate{doFunc: func(ctx context.Context, r glab.Request) (glab.Response, error, bool) {
		if err := ctx.Err(); err != nil {
			return glab.Response{}, err, true
		}
		response := glab.Response{UpstreamVersion: glab.SupportedVersion}
		switch r.Operation {
		case glab.OpAdminUser:
			s.users++
			user := adminAccount{ID: 7, Username: "tester"}
			if s.wrongUser {
				user.ID++
			}
			response.Body = adminTestBody(user)
		case glab.OpAdminNamespace:
			s.namespaces++
			ns := adminNamespace{ID: 21, FullPath: "team/sub", Kind: "group"}
			if s.wrongNS {
				ns.FullPath = "team"
			}
			response.Body = adminTestBody(ns)
		case glab.OpAdminProject:
			s.reads++
			if s.writes > 0 {
				if s.readErr != nil {
					return response, s.readErr, true
				}
				p := s.after
				if len(s.statuses) > 0 {
					p.ImportStatus = s.statuses[0]
					s.statuses = s.statuses[1:]
				}
				response.Body = adminTestBody(p)
			} else if r.Repo == s.before.Path {
				p := s.before
				if s.drift && s.reads > 1 {
					p.Description = "competing edit"
				}
				response.Body = adminTestBody(p)
			} else {
				err, _ := uxv1.NewHTTPRejection(404)
				return response, err, true
			}
		case glab.OpAdminCreate, glab.OpAdminEdit, glab.OpAdminFork:
			s.writes++
			response.Write = true
			response.Body = s.writeBody
			if response.Body == nil {
				response.Body = adminTestBody(s.after)
			}
			return response, s.writeErr, true
		default:
			return response, errors.New("unexpected operation"), true
		}
		return response, nil, true
	}}
}
func runAdminTest(t *testing.T, state *adminTestState, args []string) (int, adminReceipt, *fakeDelegate, string) {
	t.Helper()
	delegate := state.delegate()
	stdout, _, deps := productTestDeps(t, delegate)
	code := Run(context.Background(), args, deps)
	var envelope struct {
		Data  adminOutput `json:"data"`
		Error struct {
			Receipt adminOutput `json:"receipt"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	r := envelope.Data.Administration
	if code != 0 {
		r = envelope.Error.Receipt.Administration
	}
	return code, r, delegate, stdout.String()
}

func TestRepoAdminEditPrestateNoopDriftAndPostconditions(t *testing.T) {
	before := adminTestProject("team/sub/project", 101)
	for _, test := range []struct {
		name         string
		mutate       func(*adminTestState)
		description  string
		code, writes int
		outcome      string
	}{
		{"edit", nil, "new", 0, 1, "completed"},
		{"no-op", nil, "old", 0, 0, "unchanged"},
		{"drift", func(s *adminTestState) { s.drift = true }, "new", 6, 0, ""},
		{"wrong-user", func(s *adminTestState) { s.wrongUser = true }, "new", 9, 0, ""},
		{"wrong-namespace", func(s *adminTestState) { s.wrongNS = true }, "new", 9, 0, ""},
		{"uncertain-reconciled", func(s *adminTestState) { s.writeErr = errors.New("lost response") }, "new", 0, 1, "completed"},
		{"mismatching-postcondition", func(s *adminTestState) { s.after.Description = "other" }, "new", 6, 1, "ambiguous"},
		{"merge-default-drift", func(s *adminTestState) { s.after.PipelineRequired = false }, "new", 6, 1, "ambiguous"},
		{"canonical-read-failed", func(s *adminTestState) { s.readErr = errors.New("unavailable") }, "new", 6, 1, "ambiguous"},
		{"wrong-response-identity", func(s *adminTestState) { p := s.after; p.ID++; s.writeBody = adminTestBody(p) }, "new", 6, 1, "ambiguous"},
		{"wrong-response-host", func(s *adminTestState) {
			p := s.after
			p.URL = "https://other.invalid/team/sub/project"
			s.writeBody = adminTestBody(p)
		}, "new", 6, 1, "ambiguous"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &adminTestState{before: before, after: before}
			state.after.Description = test.description
			if test.mutate != nil {
				test.mutate(state)
			}
			args := adminEditArgs(t, before, "--description-file", adminTestFile(t, []byte(test.description)))
			code, r, delegate, out := runAdminTest(t, state, args)
			if code != test.code || state.writes != test.writes || r.Outcome != test.outcome {
				t.Fatalf("exit=%d writes=%d receipt=%+v output=%s", code, state.writes, r, out)
			}
			if test.writes == 1 {
				var payload map[string]any
				if err := json.Unmarshal(delegate.inputBodies[0], &payload); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(payload, map[string]any{"description": test.description}) {
					t.Fatalf("unexpected payload %v", payload)
				}
				if r.Residual != repoAdminRace || !r.MutationAttempted || r.LocalEffects != "none" {
					t.Fatalf("receipt=%+v", r)
				}
				for _, request := range delegate.requests {
					if request.InputFile != "" {
						if _, err := os.Stat(request.InputFile); !os.IsNotExist(err) {
							t.Fatalf("input not cleaned up: %v", err)
						}
					}
				}
			}
		})
	}
}
func TestRepoAdminEditAllSettingsAndExpectedMismatch(t *testing.T) {
	before := adminTestProject("team/sub/project", 101)
	state := &adminTestState{before: before, after: before}
	state.after.Visibility = "internal"
	state.after.DefaultBranch = "next"
	state.after.Issues = "private"
	state.after.Wiki = "disabled"
	args := adminEditArgs(t, before, "--visibility", "internal", "--default-branch", "next", "--issues-access-level", "private", "--wiki-access-level", "disabled")
	code, r, delegate, out := runAdminTest(t, state, args)
	if code != 0 || r.Project.adminSettings != state.after.adminSettings {
		t.Fatalf("exit=%d %s", code, out)
	}
	var payload map[string]any
	_ = json.Unmarshal(delegate.inputBodies[0], &payload)
	if len(payload) != 4 || payload["issues_access_level"] != "private" || payload["wiki_access_level"] != "disabled" {
		t.Fatalf("payload=%v", payload)
	}
	state = &adminTestState{before: before, after: before}
	state.before.Visibility = "public"
	code, _, _, out = runAdminTest(t, state, args)
	if code != 6 || state.writes != 0 {
		t.Fatalf("exit=%d %s", code, out)
	}
}
func TestRepoAdminCreateIsExplicitAndNeverRetries(t *testing.T) {
	for _, test := range []struct {
		name    string
		mutate  func(*adminTestState)
		code    int
		outcome string
	}{
		{"created", nil, 0, "completed"},
		{"ambiguous", func(s *adminTestState) { s.writeErr = errors.New("lost") }, 6, "ambiguous"},
		{"rejected", func(s *adminTestState) {
			s.writeErr, _ = uxv1.NewHTTPRejection(422)
			s.readErr, _ = uxv1.NewHTTPRejection(404)
		}, 6, "ambiguous"},
		{"wrong-visibility", func(s *adminTestState) { s.after.Visibility = "public" }, 6, "ambiguous"},
		{"wrong-namespace", func(s *adminTestState) { s.after.Namespace.ID++ }, 6, "ambiguous"},
		{"wrong-project", func(s *adminTestState) { s.after.Path = "team/sub/other" }, 6, "ambiguous"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := &adminTestState{after: adminTestProject("team/sub/project", 102)}
			if test.mutate != nil {
				test.mutate(s)
			}
			code, r, d, out := runAdminTest(t, s, adminTestArgs("create"))
			if code != test.code || s.writes != 1 || r.Outcome != test.outcome {
				t.Fatalf("exit=%d writes=%d %s", code, s.writes, out)
			}
			var payload map[string]any
			_ = json.Unmarshal(d.inputBodies[0], &payload)
			want := map[string]any{"path": "project", "name": "project", "namespace_id": float64(21), "visibility": "private", "initialize_with_readme": false}
			if !reflect.DeepEqual(payload, want) {
				t.Fatalf("payload=%v", payload)
			}
		})
	}
	s := &adminTestState{before: adminTestProject("team/sub/project", 101)}
	code, _, _, out := runAdminTest(t, s, adminTestArgs("create"))
	if code != 6 || s.writes != 0 {
		t.Fatalf("existing destination: %d %s", code, out)
	}
}
func adminTestForkState() *adminTestState {
	p := adminTestProject("team/sub/fork", 102)
	p.ForkedFrom = &adminForkSource{101, "team/sub/project", "https://gitlab.example.invalid/team/sub/project"}
	return &adminTestState{before: adminTestProject("team/sub/project", 101), after: p}
}
func TestRepoAdminForkTruthfulStatesAndPolling(t *testing.T) {
	for _, test := range []struct {
		status, outcome string
		code            int
	}{
		{"none", "accepted", 0}, {"scheduled", "accepted", 0}, {"started", "in_progress", 0}, {"finished", "completed", 0}, {"failed", "failed", 8}, {"invented", "ambiguous", 6},
	} {
		t.Run(test.status, func(t *testing.T) {
			s := adminTestForkState()
			s.after.ImportStatus = test.status
			code, r, _, out := runAdminTest(t, s, adminTestArgs("fork"))
			wantStatus := test.status
			if wantStatus == "invented" {
				wantStatus = "unknown"
			}
			if code != test.code || r.Outcome != test.outcome || s.writes != 1 || r.ImportStatus != wantStatus {
				t.Fatalf("exit=%d %s", code, out)
			}
		})
	}
	t.Run("completion", func(t *testing.T) {
		s := adminTestForkState()
		s.statuses = []string{"started", "finished"}
		code, r, _, out := runAdminTest(t, s, append(adminTestArgs("fork"), "--wait-seconds", "3"))
		if code != 0 || r.Outcome != "completed" || s.writes != 1 {
			t.Fatalf("exit=%d %s", code, out)
		}
	})
	t.Run("timeout", func(t *testing.T) {
		s := adminTestForkState()
		s.after.ImportStatus = "started"
		start := time.Now()
		code, r, _, out := runAdminTest(t, s, append(adminTestArgs("fork"), "--wait-seconds", "1"))
		if code != 0 || r.Outcome != "in_progress" || s.writes != 1 || time.Since(start) > 3*time.Second {
			t.Fatalf("exit=%d %s", code, out)
		}
	})
	for _, test := range []string{"missing-source", "wrong-source", "wrong-source-host", "wrong-source-path"} {
		t.Run(test, func(t *testing.T) {
			s := adminTestForkState()
			switch test {
			case "missing-source":
				s.after.ForkedFrom = nil
			case "wrong-source":
				s.after.ForkedFrom.ID++
			case "wrong-source-host":
				s.after.ForkedFrom.URL = "https://other.invalid/team/sub/project"
			case "wrong-source-path":
				s.after.ForkedFrom.Path = "other/project"
			}
			code, _, _, out := runAdminTest(t, s, adminTestArgs("fork"))
			if code != 6 || s.writes != 1 {
				t.Fatalf("%d %s", code, out)
			}
		})
	}
}

func TestRepoAdminMalformedFlagsNeverConstructDelegate(t *testing.T) {
	for _, action := range []string{"create", "edit", "fork"} {
		base := adminTestArgs(action)
		if action == "edit" {
			base = adminEditArgs(t, adminTestProject("team/sub/project", 101), "--visibility", "private")
		}
		for _, flag := range []string{"--source", "--push", "--clone", "--remote", "--template", "--only-allow-merge-if-pipeline-succeeds", "--yes", "--limit", "--visibility=unknown", "--namespace-id=0", "--hostname=wrong/host"} {
			t.Run(action+flag, func(t *testing.T) {
				_, _, deps := productTestDeps(t, nil)
				deps.NewDelegate = func() delegateClient { t.Fatal("constructed delegate for malformed input"); return nil }
				if code := Run(context.Background(), append(append([]string(nil), base...), flag), deps); code == 0 {
					t.Fatal("accepted invalid flag")
				}
			})
		}
		for _, flag := range []string{"--hostname", "--repo", "-R", "--allow-project-admin", "--expected-user-id", "--expected-username", "--namespace-kind", "--namespace-id", "--visibility"} {
			if flag == "--repo" || action == "edit" && flag == "--visibility" {
				continue
			}
			t.Run(action+"missing"+flag, func(t *testing.T) {
				args := []string{}
				for i := 0; i < len(base); i++ {
					if base[i] == flag {
						if flag != "--allow-project-admin" {
							i++
						}
						continue
					}
					args = append(args, base[i])
				}
				_, _, deps := productTestDeps(t, nil)
				deps.NewDelegate = func() delegateClient { t.Fatal("constructed delegate"); return nil }
				if Run(context.Background(), args, deps) == 0 {
					t.Fatal("missing guard accepted")
				}
			})
		}
	}
}
func TestRepoAdminPrivatePrestateRejectsUnclosedOrIncompleteEvidence(t *testing.T) {
	before := adminTestProject("team/sub/project", 101)
	for _, test := range []string{"unknown", "duplicate", "missing", "null", "wrong-url", "wrong-id", "too-large"} {
		t.Run(test, func(t *testing.T) {
			var body map[string]any
			_ = json.Unmarshal(adminTestBody(before.adminProject), &body)
			switch test {
			case "unknown":
				body["token"] = "not-a-token"
			case "missing":
				delete(body, "visibility")
			case "null":
				body["only_allow_merge_if_pipeline_succeeds"] = nil
			case "wrong-url":
				body["web_url"] = "https://other.invalid/team/sub/project"
			case "wrong-id":
				body["id"] = 0
			case "too-large":
				body["description"] = strings.Repeat("x", 17<<10)
			}
			data := adminTestBody(body)
			if test == "duplicate" {
				data = append([]byte(`{"id":101,`), data[1:]...)
			}
			args := append(adminTestArgs("edit"), "--expected-state-file", adminTestFile(t, data), "--accept-non-atomic", "--visibility", "private")
			s := &adminTestState{before: before}
			code, _, d, out := runAdminTest(t, s, args)
			if code == 0 || len(d.requests) != 0 {
				t.Fatalf("exit=%d requests=%v %s", code, d.requests, out)
			}
		})
	}
}
func TestRepoAdminBudgetsCancellationAndNoPagination(t *testing.T) {
	before := adminTestProject("team/sub/project", 101)
	t.Run("body", func(t *testing.T) {
		d := &fakeDelegate{doFunc: func(context.Context, glab.Request) (glab.Response, error, bool) {
			return glab.Response{Body: []byte(strings.Repeat(" ", limits.MaxJSONPageBytes+1))}, nil, true
		}}
		_, _, deps := productTestDeps(t, d)
		if Run(context.Background(), adminTestArgs("create"), deps) != 8 || len(d.requests) != 1 {
			t.Fatalf("requests=%v", d.requests)
		}
	})
	t.Run("canceled-before-write", func(t *testing.T) {
		s := &adminTestState{before: before}
		d := s.delegate()
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		_, _, deps := productTestDeps(t, d)
		if Run(ctx, adminTestArgs("create"), deps) != 130 || len(d.requests) != 0 {
			t.Fatalf("requests=%v", d.requests)
		}
	})
	t.Run("preflight-deadline", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
		defer cancel()
		d := &fakeDelegate{doFunc: func(ctx context.Context, _ glab.Request) (glab.Response, error, bool) {
			<-ctx.Done()
			return glab.Response{}, ctx.Err(), true
		}}
		_, _, deps := productTestDeps(t, d)
		start := time.Now()
		if Run(ctx, adminTestArgs("create"), deps) == 0 || time.Since(start) > time.Second {
			t.Fatal("unbounded preflight")
		}
	})
	t.Run("no-pagination", func(t *testing.T) {
		s := &adminTestState{after: before}
		code, _, d, out := runAdminTest(t, s, adminTestArgs("create"))
		if code != 0 {
			t.Fatal(out)
		}
		for _, r := range d.requests {
			if r.Page != 0 || r.PerPage != 0 {
				t.Fatal("unexpected search/pagination")
			}
		}
	})
}
