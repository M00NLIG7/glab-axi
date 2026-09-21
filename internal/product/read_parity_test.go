package product

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

type readParityContract struct {
	Schema    string `json:"schema"`
	Reference struct {
		Commit string `json:"commit"`
	} `json:"reference"`
	Accepted [][]string `json:"accepted"`
	Rejected [][]string `json:"rejected"`
}

func loadReadParityContract(t *testing.T) readParityContract {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "read-parity", "gh-axi-2bffd9a5b60ded64d6c9851683b27a480173a7ee.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract readParityContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Schema != "glab-axi/read-parity-contract/v1" || contract.Reference.Commit != "2bffd9a5b60ded64d6c9851683b27a480173a7ee" {
		t.Fatal("unexpected reference contract")
	}
	return contract
}

func readParityArgs(args []string) []string {
	out := append([]string(nil), args...)
	for _, flag := range []struct{ name, value string }{{"--repo", "group/project"}, {"--hostname", "gitlab.com"}, {"--format", "json"}} {
		present := false
		for _, arg := range args {
			present = present || arg == flag.name || strings.HasPrefix(arg, flag.name+"=")
		}
		if !present {
			out = append(out, flag.name, flag.value)
		}
	}
	return out
}

func readParityObject(group, description string, iid int) map[string]any {
	resource := "issues"
	if group == "mr" {
		resource = "merge_requests"
	}
	return map[string]any{
		"id": iid + 1000, "iid": iid, "title": "example", "description": description, "state": "opened",
		"web_url":       fmt.Sprintf("https://gitlab.com/group/project/-/%s/%d", resource, iid),
		"source_branch": "feature/topic", "target_branch": "main", "draft": false,
		"author": map[string]any{"username": "alice"}, "labels": []string{"triage", "needs review"},
		"created_at": "2026-09-01T12:00:00Z", "updated_at": "2026-09-02T12:00:00Z",
		"sha": strings.Repeat("a", 40), "base_sha": strings.Repeat("b", 40), "merge_status": "can_be_merged",
	}
}

func readParityBody(t *testing.T, group, action, description string) []byte {
	t.Helper()
	var body any = readParityObject(group, description, 42)
	if action == "list" {
		body = []any{body}
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestPinnedReadParityConsumerContract(t *testing.T) {
	contract := loadReadParityContract(t)
	for _, args := range contract.Accepted {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			delegate := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
				return glab.Response{Body: readParityBody(t, args[0], args[1], strings.Repeat("é", 100)), UpstreamVersion: glab.SupportedVersion}, nil, true
			}}
			stdout, stderr, deps := productTestDeps(t, delegate)
			code := Run(context.Background(), readParityArgs(args), deps)
			if code != 0 || stderr.Len() != 0 || len(delegate.requests) != 1 {
				t.Fatalf("exit=%d calls=%d stdout=%s stderr=%s", code, len(delegate.requests), stdout, stderr)
			}
			parsed, err := Parse(readParityArgs(args))
			if err != nil {
				t.Fatal(err)
			}
			if args[1] == "list" && !reflect.DeepEqual(delegate.requests[0].Filters, listFilters(*parsed.Command)) {
				t.Fatal("filters lost before delegate")
			}
		})
	}
	for _, args := range contract.Rejected {
		t.Run("reject "+strings.Join(args, " "), func(t *testing.T) {
			var previous string
			for attempt := 0; attempt < 2; attempt++ {
				stdout, stderr, deps := productTestDeps(t, &fakeDelegate{})
				deps.NewDelegate = func() delegateClient { t.Fatal("invalid input constructed delegate"); return nil }
				code := Run(context.Background(), readParityArgs(args), deps)
				if code != 2 || stderr.Len() != 0 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, stdout, stderr)
				}
				if attempt > 0 && stdout.String() != previous {
					t.Fatal("nondeterministic validation error")
				}
				previous = stdout.String()
			}
		})
	}
}

func TestReadSelectionPreservesDefaultFieldsAndByteBounds(t *testing.T) {
	for _, group := range []string{"issue", "mr"} {
		for _, action := range []string{"list", "view"} {
			for _, limit := range []int{0, 1, 2, 5, 16, 32, 131072} {
				t.Run(fmt.Sprintf("%s/%s/%d", group, action, limit), func(t *testing.T) {
					delegate := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
						return glab.Response{Body: readParityBody(t, group, action, strings.Repeat("界", 50000))}, nil, true
					}}
					stdout, _, deps := productTestDeps(t, delegate)
					args := []string{group, action}
					if action == "view" {
						args = append(args, "42")
					} else {
						args = append(args, "--fields=description")
					}
					args = append(args, "--body-limit="+strconv.Itoa(limit))
					if code := Run(context.Background(), readParityArgs(args), deps); code != 0 {
						t.Fatalf("exit=%d %s", code, stdout)
					}
					var envelope struct {
						Data map[string]json.RawMessage `json:"data"`
						Meta uxv1.Meta                  `json:"meta"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					key := group
					if action == "list" {
						key = "issues"
						if group == "mr" {
							key = "mrs"
						}
					}
					var item map[string]any
					if action == "list" {
						var items []map[string]any
						if err := json.Unmarshal(envelope.Data[key], &items); err != nil {
							t.Fatal(err)
						}
						item = items[0]
					} else if err := json.Unmarshal(envelope.Data[key], &item); err != nil {
						t.Fatal(err)
					}
					description, _ := item["description"].(string)
					if len(description) > limit || !utf8.ValidString(description) || item["iid"] != float64(42) || item["web_url"] == nil || item["state"] != "opened" || item["author"] != "alice" || item["labels"] == nil || item["created_at"] == nil || item["updated_at"] == nil {
						t.Fatalf("selection failed: %#v", item)
					}
					if !envelope.Meta.Complete || !envelope.Meta.Truncated || envelope.Meta.Reason != "field_limit" {
						t.Fatalf("metadata=%#v", envelope.Meta)
					}
				})
			}
		}
	}
}

func TestReadSelectionValidatesIdentityBeforeRendering(t *testing.T) {
	for _, group := range []string{"issue", "mr"} {
		for _, mutation := range []string{"host", "project", "nested-project", "kind", "iid", "url-iid", "query", "branch"} {
			if mutation == "branch" && group != "mr" {
				continue
			}
			t.Run(group+"/"+mutation, func(t *testing.T) {
				item := readParityObject(group, "text", 42)
				web := item["web_url"].(string)
				switch mutation {
				case "host":
					item["web_url"] = strings.Replace(web, "gitlab.com", "evil.invalid", 1)
				case "project":
					item["web_url"] = strings.Replace(web, "group/project", "group/other", 1)
				case "nested-project":
					item["web_url"] = strings.Replace(web, "group/project", "other/group/project", 1)
				case "kind":
					item["web_url"] = strings.Replace(web, "/-/", "/-/other/", 1)
				case "iid":
					item = readParityObject(group, "text", 43)
				case "url-iid":
					item["web_url"] = strings.TrimSuffix(web, "42") + "43"
				case "query":
					item["web_url"] = web + "?x=private"
				case "branch":
					item["source_branch"] = "another"
				}
				action := "view"
				var source any = item
				args := []string{group, action, "42"}
				if mutation == "branch" {
					action = "list"
					source = []any{item}
					args = []string{group, action, "--source-branch=feature/topic", "--fields=author"}
				}
				body, _ := json.Marshal(source)
				delegate := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
					return glab.Response{Body: body}, nil, true
				}}
				stdout, _, deps := productTestDeps(t, delegate)
				if code := Run(context.Background(), readParityArgs(args), deps); code != 9 {
					t.Fatalf("exit=%d %s", code, stdout)
				}
			})
		}
	}
}

func TestReadSelectionAddsFieldsWithoutSuppressingDefaults(t *testing.T) {
	for _, group := range []string{"issue", "mr"} {
		t.Run(group, func(t *testing.T) {
			source := readParityObject(group, "body", 42)
			source["merge_status"] = "future_status"
			source["head_pipeline"] = map[string]any{"id": 7, "status": "success", "web_url": "https://gitlab.com/group/project/-/pipelines/7"}
			body, err := json.Marshal([]any{source})
			if err != nil {
				t.Fatal(err)
			}
			fields := []string{"", "author", "description", "description,author,labels,created_at,updated_at"}
			if group == "mr" {
				fields = append(fields, "base_sha,head_sha,head_pipeline,raw_merge_status")
			}
			var defaults map[string]any
			for _, field := range fields {
				delegate := &fakeDelegate{doFunc: func(context.Context, glab.Request) (glab.Response, error, bool) {
					return glab.Response{Body: body}, nil, true
				}}
				stdout, _, deps := productTestDeps(t, delegate)
				args := []string{group, "list"}
				if field != "" {
					args = append(args, "--fields="+field)
				}
				if code := Run(context.Background(), readParityArgs(args), deps); code != 0 {
					t.Fatalf("exit=%d output=%s", code, stdout)
				}
				var envelope struct {
					Data map[string][]map[string]any `json:"data"`
				}
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				key := "issues"
				if group == "mr" {
					key = "mrs"
				}
				items := envelope.Data[key]
				if len(items) != 1 {
					t.Fatalf("items=%#v", items)
				}
				item := items[0]
				if strings.Contains(field, "description") {
					if item["description"] != "body" {
						t.Fatalf("description not added: %#v", item)
					}
					delete(item, "description")
				}
				if field == "" {
					defaults = item
				} else if !reflect.DeepEqual(item, defaults) {
					t.Fatalf("fields=%q changed defaults: got=%#v want=%#v", field, item, defaults)
				}
			}
		})
	}
}

func TestReadDefaultsPreserveExistingResourceShape(t *testing.T) {
	for _, group := range []string{"issue", "mr"} {
		for _, action := range []string{"list", "view"} {
			body, _ := json.Marshal(readParityObject(group, "body", 42))
			args := []string{group, action}
			if action == "view" {
				args = append(args, "42")
			}
			parsed, err := Parse(readParityArgs(args))
			if err != nil {
				t.Fatal(err)
			}
			selection, err := parseReadSelection(*parsed.Command)
			if err != nil {
				t.Fatal(err)
			}
			target := Target{Host: "gitlab.com", Repo: "group/project"}
			if group == "issue" {
				var source upstreamIssue
				if err := json.Unmarshal(body, &source); err != nil {
					t.Fatal(err)
				}
				old, _, err := normalizeIssue(source, target.Host, target.Repo, action == "view")
				if err != nil {
					t.Fatal(err)
				}
				got, _, err := selection.issue(source, target, 0)
				if err != nil || !reflect.DeepEqual(old, got) {
					t.Fatalf("default issue shape changed: old=%#v new=%#v err=%v", old, got, err)
				}
			} else {
				var source upstreamMR
				if err := json.Unmarshal(body, &source); err != nil {
					t.Fatal(err)
				}
				old, _, err := normalizeMR(source, target.Host, target.Repo, action == "view")
				if err != nil {
					t.Fatal(err)
				}
				got, _, err := selection.mr(source, target, 0, glab.ListFilters{})
				if err != nil || !reflect.DeepEqual(old, got) {
					t.Fatalf("default MR shape changed: old=%#v new=%#v err=%v", old, got, err)
				}
			}
		}
	}
}

func TestReadFiltersFailClosedOnPaginationError(t *testing.T) {
	for _, oversized := range []bool{false, true} {
		delegate := &fakeDelegate{doFunc: func(_ context.Context, request glab.Request) (glab.Response, error, bool) {
			if request.Page == 2 {
				return glab.Response{}, uxv1.NewError(uxv1.CodeRateLimited, "rate limited"), true
			}
			count := 100
			if oversized {
				count++
			}
			items := make([]any, count)
			for i := range items {
				items[i] = readParityObject("issue", "body", i+1)
			}
			body, _ := json.Marshal(items)
			return glab.Response{Body: body}, nil, true
		}}
		stdout, _, deps := productTestDeps(t, delegate)
		code := Run(context.Background(), readParityArgs([]string{"issue", "list", "--state=all", "--limit=100"}), deps)
		wantCode, wantRequests := 7, 2
		if oversized {
			wantCode, wantRequests = 8, 1
		}
		if code != wantCode || len(delegate.requests) != wantRequests || strings.Contains(stdout.String(), `"issues"`) || !strings.Contains(stdout.String(), `"complete":false`) {
			t.Fatalf("exit=%d requests=%d output=%s", code, len(delegate.requests), stdout)
		}
	}
}

func TestReadFiltersSurvivePaginationAndBoundaries(t *testing.T) {
	for _, scenario := range []struct {
		name               string
		pages, limit, last int
		complete           bool
		reason             string
	}{
		{"exact-limit", 2, 100, 0, true, ""}, {"display-limit", 2, 100, 1, false, "display_limit"}, {"hard-pages", 10, 1000, 100, false, "hard_page_limit"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			delegate := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
				if r.Filters.State != "all" || !reflect.DeepEqual(r.Filters.Labels, []string{"triage", "needs review"}) || r.Filters.SourceBranch != "feature/topic" || r.Filters.TargetBranch != "main" || r.Page < 1 || r.PerPage != 100 {
					t.Fatalf("request=%#v", r)
				}
				n := 100
				if r.Page == scenario.pages {
					n = scenario.last
				}
				items := make([]any, n)
				for i := range items {
					items[i] = readParityObject("mr", "body", (r.Page-1)*100+i+1)
				}
				body, _ := json.Marshal(items)
				return glab.Response{Body: body}, nil, true
			}}
			stdout, _, deps := productTestDeps(t, delegate)
			args := readParityArgs([]string{"mr", "list", "--state=all", "--label=triage", "--label=needs review", "--source-branch=feature/topic", "--target-branch=main", "--limit=" + strconv.Itoa(scenario.limit)})
			if code := Run(context.Background(), args, deps); code != 0 {
				t.Fatalf("exit=%d %s", code, stdout)
			}
			var envelope struct {
				Meta uxv1.Meta `json:"meta"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			if len(delegate.requests) != scenario.pages || envelope.Meta.Count != scenario.limit || envelope.Meta.Complete != scenario.complete || envelope.Meta.Reason != scenario.reason {
				t.Fatalf("calls=%d meta=%#v", len(delegate.requests), envelope.Meta)
			}
		})
	}
}
