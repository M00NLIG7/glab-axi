package glab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestPinnedRequestBuildersEmitOnlyDeclaredArgv(t *testing.T) {
	input := filepath.Join(t.TempDir(), "payload.json")
	requests := []struct {
		request Request
		want    []string
	}{
		{Request{Operation: OpIssueList, Host: "gitlab.com", Repo: "group/project", Page: 2, PerPage: 31}, []string{"issue", "list", "--output", "json", "--page", "2", "--per-page", "31", "-R", "group/project"}},
		{Request{Operation: OpIssueView, Host: "gitlab.com", Repo: "group/project", IID: 7}, []string{"issue", "view", "7", "--output", "json", "-R", "group/project"}},
		{Request{Operation: OpMRList, Host: "gitlab.com", Repo: "group/project", Page: 1, PerPage: 100}, []string{"mr", "list", "--output", "json", "--page", "1", "--per-page", "100", "-R", "group/project"}},
		{Request{Operation: OpMRView, Host: "gitlab.com", Repo: "group/project", IID: 8}, []string{"mr", "view", "8", "--output", "json", "-R", "group/project"}},
		{Request{Operation: OpMRDiff, Host: "gitlab.com", Repo: "group/project", IID: 8}, []string{"mr", "diff", "8", "--color", "never", "-R", "group/project"}},
		{Request{Operation: OpMRChecks, Host: "gitlab.com", Repo: "group/project", IID: 8}, []string{"ci", "get", "--merge-request", "8", "--output", "json", "-R", "group/project"}},
		{Request{Operation: OpPipelineList, Host: "gitlab.com", Repo: "group/project", Page: 1, PerPage: 30}, []string{"ci", "list", "--output", "json", "--page", "1", "--per-page", "30", "-R", "group/project"}},
		{Request{Operation: OpPipelineView, Host: "gitlab.com", Repo: "group/project", ID: 42}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fproject/pipelines/42"}},
		{Request{Operation: OpJobList, Host: "gitlab.com", Repo: "group/project", PipelineID: 42, Page: 1, PerPage: 30}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fproject/pipelines/42/jobs?page=1&per_page=30"}},
		{Request{Operation: OpJobView, Host: "gitlab.com", Repo: "group/project", ID: 99}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fproject/jobs/99"}},
		{Request{Operation: OpJobTrace, Host: "gitlab.com", Repo: "group/project", ID: 99}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fproject/jobs/99/trace"}},
		{Request{Operation: OpReleaseList, Host: "gitlab.com", Repo: "group/project", Page: 1, PerPage: 30}, []string{"release", "list", "--output", "json", "--page", "1", "--per-page", "30", "-R", "group/project"}},
		{Request{Operation: OpReleaseView, Host: "gitlab.com", Repo: "group/project", Tag: "v2.0.0"}, []string{"release", "view", "v2.0.0", "--output", "json", "-R", "group/project"}},
		{Request{Operation: OpRepoList, Host: "gitlab.com", Page: 1, PerPage: 30}, []string{"repo", "list", "--output", "json", "--page", "1", "--per-page", "30"}},
		{Request{Operation: OpRepoView, Host: "gitlab.com", Repo: "group/project"}, []string{"repo", "view", "group/project", "--output", "json"}},
		{Request{Operation: OpLabelList, Host: "gitlab.com", Repo: "group/project", Page: 1, PerPage: 30}, []string{"label", "list", "--output", "json", "--page", "1", "--per-page", "30", "-R", "group/project"}},
		{Request{Operation: OpSearch, Host: "gitlab.com", Repo: "group/project", Scope: "issues", Query: "needs review", Page: 1, PerPage: 30}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fproject/search?page=1&per_page=30&scope=issues&search=needs+review"}},
		{Request{Operation: OpEnsureProject, Host: "gitlab.com", Repo: "group/project"}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fproject"}},
		{Request{Operation: OpEnsureList, Host: "gitlab.com", Repo: "group/project", Source: "feature", Target: "main", Page: 1, PerPage: 100}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fproject/merge_requests?page=1&per_page=100&source_branch=feature&state=opened&target_branch=main"}},
		{Request{Operation: OpEnsureCreate, Host: "gitlab.com", Repo: "group/project", InputFile: input}, []string{"api", "--method", "POST", "--hostname", "gitlab.com", "projects/group%2Fproject/merge_requests", "--input", input}},
		{Request{Operation: OpEnsureUpdate, Host: "gitlab.com", Repo: "group/project", IID: 12, InputFile: input}, []string{"api", "--method", "PUT", "--hostname", "gitlab.com", "projects/group%2Fproject/merge_requests/12", "--input", input}},
	}
	for _, test := range requests {
		invocation, err := build(test.request)
		if err != nil {
			t.Fatalf("%s: %v", test.request.Operation, err)
		}
		if !reflect.DeepEqual(invocation.args, test.want) {
			t.Fatalf("%s argv\n got: %#v\nwant: %#v", test.request.Operation, invocation.args, test.want)
		}
	}
}

func TestCapabilityFixturePinsEveryExecutableOperation(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "official-glab", "v1.112.0", "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema  string `json:"schema"`
		Release struct {
			Version string `json:"version"`
		} `json:"release"`
		Operations []struct {
			Name string   `json:"name"`
			Argv []string `json:"argv"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "glab-axi/official-glab-contract/v1" || fixture.Release.Version != SupportedVersion {
		t.Fatalf("unexpected fixture identity: %#v", fixture)
	}
	declared := make(map[string]bool, len(fixture.Operations))
	for _, operation := range fixture.Operations {
		if operation.Name == "" || len(operation.Argv) == 0 || declared[operation.Name] {
			t.Fatalf("invalid operation fixture: %#v", operation)
		}
		declared[operation.Name] = true
	}
	for _, operation := range []Operation{OpIssueList, OpIssueView, OpMRList, OpMRView, OpMRDiff, OpMRChecks, OpPipelineList, OpPipelineView, OpJobList, OpJobView, OpJobTrace, OpReleaseList, OpReleaseView, OpRepoList, OpRepoView, OpLabelList, OpSearch, OpEnsureProject, OpEnsureList, OpEnsureCreate, OpEnsureUpdate} {
		if !declared[string(operation)] {
			t.Fatalf("operation %q has no pinned fixture", operation)
		}
	}
}

func TestRequestBuilderRejectsInjectionBeforeExecution(t *testing.T) {
	for _, request := range []Request{
		{Operation: OpIssueView, Host: "gitlab.com", Repo: "-R", IID: 1},
		{Operation: OpReleaseView, Host: "gitlab.com", Repo: "group/project", Tag: "v1\n--web"},
		{Operation: OpSearch, Host: "gitlab.com", Repo: "group/project", Scope: "issues", Query: "", Page: 1, PerPage: 30},
		{Operation: OpEnsureCreate, Host: "gitlab.com", Repo: "group/project", InputFile: "relative.json"},
	} {
		if _, err := build(request); err == nil {
			t.Fatalf("unsafe request was accepted: %#v", request)
		}
	}
}
