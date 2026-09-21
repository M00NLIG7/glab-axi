package product

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func TestPinnedRepoAdminConsumerContractExecutes(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "contracts", "repo-admin", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Schema   string   `json:"schema"`
		Common   []string `json:"common_argv"`
		Commands []struct {
			Argv          []string `json:"argv"`
			AllowedFields []string `json:"allowed_fields"`
		} `json:"commands"`
		MaxWrites int `json:"max_mutation_attempts"`
		MaxReads  int `json:"max_fork_postcondition_reads"`
		MaxWait   int `json:"max_wait_seconds"`
		MaxPage   int `json:"max_page_bytes"`
		MaxBytes  int `json:"max_operation_bytes"`
	}
	if err := json.Unmarshal(body, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Schema != "glab-axi/repo-admin-consumer-contract/v1" || contract.MaxWrites != 1 || contract.MaxReads != 10 || contract.MaxWait != 20 || contract.MaxPage != limits.MaxJSONPageBytes || contract.MaxBytes != limits.MaxOperationBytes || len(contract.Commands) != 3 {
		t.Fatalf("invalid consumer contract: %+v", contract)
	}
	for _, command := range contract.Commands {
		t.Run(strings.Join(command.Argv[:2], " "), func(t *testing.T) {
			before := adminTestProject("team/sub/project", 101)
			args := append(append([]string(nil), command.Argv...), contract.Common...)
			for i, arg := range args {
				if arg == "{snapshot}" {
					args[i] = adminTestFile(t, adminTestBody(before.adminProject))
				}
			}
			state := &adminTestState{after: before}
			switch command.Argv[1] {
			case "edit":
				state.before = before
				state.after.Visibility = "internal"
			case "fork":
				state = adminTestForkState()
			}
			code, receipt, delegate, out := runAdminTest(t, state, args)
			if code != 0 || receipt.Outcome != "completed" || state.writes != contract.MaxWrites {
				t.Fatalf("exit=%d %s", code, out)
			}
			var payload map[string]json.RawMessage
			_ = json.Unmarshal(delegate.inputBodies[0], &payload)
			allowed := map[string]bool{}
			for _, field := range command.AllowedFields {
				allowed[field] = true
			}
			for field := range payload {
				if !allowed[field] {
					t.Fatalf("uncontracted input field %s", field)
				}
			}
		})
	}
}

func TestRepoAdminSnapshotIsRoundTrippableAndBound(t *testing.T) {
	project := adminTestForkState().after
	for _, scenario := range []string{"valid", "wrong-project", "wrong-source-host", "missing-settings"} {
		t.Run(scenario, func(t *testing.T) {
			p := project
			if scenario == "wrong-project" {
				p.Path = "team/sub/other"
			}
			if scenario == "wrong-source-host" {
				from := *p.ForkedFrom
				from.URL = "https://other.invalid/team/sub/project"
				p.ForkedFrom = &from
			}
			body := adminTestBody(p)
			if scenario == "missing-settings" {
				var fields map[string]any
				_ = json.Unmarshal(body, &fields)
				delete(fields, "wiki_access_level")
				body = adminTestBody(fields)
			}
			d := &fakeDelegate{responses: map[glab.Operation][]glab.Response{glab.OpAdminProject: {{Body: body}}}}
			stdout, _, deps := productTestDeps(t, d)
			code := Run(context.Background(), []string{"repo", "view", "-R", "team/sub/fork", "--hostname", "gitlab.example.invalid", "--admin-snapshot", "--format", "json"}, deps)
			if scenario != "valid" {
				if code == 0 {
					t.Fatal("unsafe snapshot accepted")
				}
				return
			}
			if code != 0 {
				t.Fatal(stdout.String())
			}
			var envelope struct {
				Data struct {
					Snapshot json.RawMessage `json:"admin_snapshot"`
					Status   string          `json:"import_status"`
					Source   adminForkSource `json:"forked_from_project"`
				} `json:"data"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			got, err := decodeAdminProject(envelope.Data.Snapshot, true)
			if err != nil || got.adminProject != project.adminProject || envelope.Data.Status != "finished" || !reflect.DeepEqual(envelope.Data.Source, *project.ForkedFrom) {
				t.Fatalf("snapshot: %s err=%v", stdout.String(), err)
			}
		})
	}
}

func TestRepoAdminActualInvalidValuesRefuseBeforeChild(t *testing.T) {
	for flag, values := range map[string][]string{
		"--visibility":        {"PUBLIC", "", "secret", "enabled"},
		"--namespace-id":      {"0", "01", "-1", "1e3"},
		"--namespace-kind":    {"organization", "USER", "user"},
		"--expected-user-id":  {"0", "01", "-1"},
		"--expected-username": {"a/b", ".."},
		"--hostname":          {"https://gitlab.example.invalid", "user@host", "other/host"},
		"-R":                  {"project", "/team/sub/project", "team/../project"},
	} {
		for _, value := range values {
			t.Run(flag+value, func(t *testing.T) {
				args := adminTestArgs("create")
				for i, arg := range args {
					if arg == flag {
						args[i+1] = value
					}
				}
				_, _, deps := productTestDeps(t, nil)
				deps.NewDelegate = func() delegateClient { t.Fatal("child constructed"); return nil }
				if Run(context.Background(), args, deps) == 0 {
					t.Fatal("invalid guard accepted")
				}
			})
		}
	}
}

func TestRepoAdminPersonalNamespaceIsNotUserID(t *testing.T) {
	after := adminTestProject("tester/project", 102)
	after.Namespace = adminNamespace{ID: 21, FullPath: "tester", Kind: "user"}
	state := &adminTestState{after: after}
	delegate := state.delegate()
	original := delegate.doFunc
	delegate.doFunc = func(ctx context.Context, r glab.Request) (glab.Response, error, bool) {
		if r.Operation == glab.OpAdminNamespace {
			if r.ID != 21 {
				t.Fatal("user ID substituted for namespace ID")
			}
			return glab.Response{Body: adminTestBody(after.Namespace)}, nil, true
		}
		return original(ctx, r)
	}
	args := adminTestArgs("create")
	for i, arg := range args {
		if arg == "-R" {
			args[i+1] = "tester/project"
		}
		if arg == "--namespace-kind" {
			args[i+1] = "user"
		}
	}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), args, deps); code != 0 || state.writes != 1 {
		t.Fatalf("exit=%d %s", code, stdout.String())
	}
}

func TestRepoAdminAggregateBudgetStopsBeforeMutation(t *testing.T) {
	state := &adminTestState{after: adminTestProject("team/sub/project", 101)}
	delegate := state.delegate()
	original := delegate.doFunc
	delegate.doFunc = func(ctx context.Context, r glab.Request) (glab.Response, error, bool) {
		response, err, handled := original(ctx, r)
		// Even rejected requests count against the operation's read budget.
		response.Body = append(response.Body, []byte(strings.Repeat(" ", limits.MaxJSONPageBytes-1000))...)
		return response, err, handled
	}
	_, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), adminTestArgs("create"), deps); code != 8 || state.writes != 0 {
		t.Fatalf("exit=%d writes=%d", code, state.writes)
	}
}

func TestRepoAdminForkCancellationAndPollingCap(t *testing.T) {
	for _, scenario := range []string{"cancel", "deadline", "read-cap"} {
		t.Run(scenario, func(t *testing.T) {
			state := adminTestForkState()
			state.after.ImportStatus = "started"
			delegate := state.delegate()
			ctx := context.Background()
			cancel := func() {}
			if scenario == "cancel" {
				var stop context.CancelFunc
				ctx, stop = context.WithCancel(ctx)
				timer := time.AfterFunc(100*time.Millisecond, stop)
				cancel = func() { timer.Stop(); stop() }
			}
			if scenario == "deadline" {
				ctx, cancel = context.WithTimeout(ctx, 100*time.Millisecond)
			}
			defer cancel()
			stdout, _, deps := productTestDeps(t, delegate)
			start := time.Now()
			code := Run(ctx, append(adminTestArgs("fork"), "--wait-seconds", "20"), deps)
			want := 0
			if scenario == "cancel" {
				want = 130
			}
			if code != want || state.writes != 1 || time.Since(start) > 12*time.Second {
				t.Fatalf("exit=%d %s", code, stdout.String())
			}
			if scenario == "read-cap" && state.reads != 14 {
				t.Fatalf("reads=%d want 4 preflight + 10 postcondition", state.reads)
			}
			if !strings.Contains(stdout.String(), `"outcome":"in_progress"`) || !strings.Contains(stdout.String(), `"complete":false`) {
				t.Fatalf("lost pending receipt: %s", stdout.String())
			}
		})
	}
}
