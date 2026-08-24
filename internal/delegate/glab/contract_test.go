package glab

import (
	"crypto/sha256"
	"encoding/hex"
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
		{Request{Operation: OpEnsureCreate, Host: "gitlab.com", Repo: "group/project", InputFile: input}, []string{"api", "--method", "POST", "--hostname", "gitlab.com", "projects/group%2Fproject/merge_requests", "--input", input, "--header", "Content-Type: application/json"}},
		{Request{Operation: OpEnsureUpdate, Host: "gitlab.com", Repo: "group/project", IID: 12, InputFile: input}, []string{"api", "--method", "PUT", "--hostname", "gitlab.com", "projects/group%2Fproject/merge_requests/12", "--input", input, "--header", "Content-Type: application/json"}},
		{Request{Operation: OpMergeProject, Host: "gitlab.com", Repo: "group/subgroup/project"}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fsubgroup%2Fproject"}},
		{Request{Operation: OpMergeMRView, Host: "gitlab.com", Repo: "group/subgroup/project", IID: 42}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fsubgroup%2Fproject/merge_requests/42?with_merge_status_recheck=true"}},
		{Request{Operation: OpMergeJobList, Host: "gitlab.com", Repo: "group/subgroup/project", PipelineID: 77, Page: 2, PerPage: 100}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fsubgroup%2Fproject/pipelines/77/jobs?include_retried=false&page=2&per_page=100"}},
		{Request{Operation: OpMergeBridgeList, Host: "gitlab.com", Repo: "group/subgroup/project", PipelineID: 77, Page: 1, PerPage: 100}, []string{"api", "--method", "GET", "--hostname", "gitlab.com", "projects/group%2Fsubgroup%2Fproject/pipelines/77/bridges?include_retried=false&page=1&per_page=100"}},
		{Request{Operation: OpMRMerge, Host: "gitlab.com", Repo: "group/subgroup/project", IID: 42, InputFile: input}, []string{"api", "--method", "PUT", "--hostname", "gitlab.com", "projects/group%2Fsubgroup%2Fproject/merge_requests/42/merge", "--input", input, "--header", "Content-Type: application/json"}},
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
			Name                  string   `json:"name"`
			Argv                  []string `json:"argv"`
			ResponseNormalization struct {
				CanonicalSourceHead    string `json:"canonical_source_head"`
				OfficialClientHead     string `json:"official_client_source_head"`
				DualValuePolicy        string `json:"dual_value_policy"`
				MissingMalformedPolicy string `json:"missing_or_malformed_policy"`
			} `json:"response_normalization"`
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
		if operation.Name == string(OpMRView) {
			normalization := operation.ResponseNormalization
			if normalization.CanonicalSourceHead != "sha" || normalization.OfficialClientHead != "diff_refs.head_sha" || normalization.DualValuePolicy != "require-exact-match" || normalization.MissingMalformedPolicy != "refuse" {
				t.Fatalf("mr-view response normalization is not pinned: %#v", normalization)
			}
		}
		declared[operation.Name] = true
	}
	for _, operation := range []Operation{OpIssueList, OpIssueView, OpMRList, OpMRView, OpMRDiff, OpMRChecks, OpPipelineList, OpPipelineView, OpJobList, OpJobView, OpJobTrace, OpReleaseList, OpReleaseView, OpRepoList, OpRepoView, OpLabelList, OpSearch, OpEnsureProject, OpEnsureList, OpEnsureCreate, OpEnsureUpdate, OpMergeProject, OpMergeMRView, OpMergeJobList, OpMergeBridgeList, OpMRMerge} {
		if !declared[string(operation)] {
			t.Fatalf("operation %q has no pinned fixture", operation)
		}
	}
}

func TestPinnedEvidenceFilesMatchCapabilityManifest(t *testing.T) {
	root := filepath.Join("..", "..", "..", "contracts", "official-glab", "v1.112.0")
	manifestData, err := os.ReadFile(filepath.Join(root, "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Evidence struct {
			Help        string `json:"help_sha256"`
			Checksums   string `json:"upstream_checksums_sha256"`
			AuthStorage string `json:"auth_storage_source_sha256"`
		} `json:"evidence"`
	}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		t.Fatal(err)
	}
	for _, contract := range []struct {
		name string
		want string
	}{
		{"help.txt", manifest.Evidence.Help},
		{"upstream-checksums.txt", manifest.Evidence.Checksums},
		{"auth-storage-source.go.txt", manifest.Evidence.AuthStorage},
	} {
		data, err := os.ReadFile(filepath.Join(root, contract.name))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != contract.want {
			t.Fatalf("%s digest=%s want=%s", contract.name, got, contract.want)
		}
	}
}

func TestRequestBuilderRejectsInjectionBeforeExecution(t *testing.T) {
	for _, request := range []Request{
		{Operation: OpIssueView, Host: "gitlab.com", Repo: "-R", IID: 1},
		{Operation: OpReleaseView, Host: "gitlab.com", Repo: "group/project", Tag: "v1\n--web"},
		{Operation: OpReleaseView, Host: "gitlab.com", Repo: "group/project", Tag: "--web"},
		{Operation: OpSearch, Host: "gitlab.com", Repo: "group/project", Scope: "issues", Query: "", Page: 1, PerPage: 30},
		{Operation: OpEnsureCreate, Host: "gitlab.com", Repo: "group/project", InputFile: "relative.json"},
		{Operation: OpMergeMRView, Host: "gitlab.com", Repo: "group/project", IID: 0},
		{Operation: OpMergeJobList, Host: "gitlab.com", Repo: "group/project", PipelineID: 7, Page: 11, PerPage: 100},
		{Operation: OpMergeBridgeList, Host: "gitlab.com", Repo: "group/project", PipelineID: -1, Page: 1, PerPage: 100},
		{Operation: OpMRMerge, Host: "gitlab.com", Repo: "group/project", IID: 1, InputFile: "relative.json"},
	} {
		if _, err := build(request); err == nil {
			t.Fatalf("unsafe request was accepted: %#v", request)
		}
	}
}
