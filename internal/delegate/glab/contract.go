// Package glab delegates a closed set of typed operations to the pinned
// official GitLab CLI. It deliberately exposes no arbitrary argv runner.
package glab

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
	"gl-axi/internal/safeurl"
)

const (
	SupportedVersion = "1.112.0"
	SupportedBuild   = "816e3a52"
)

type Operation string

const (
	OpIssueList                  Operation = "issue-list"
	OpIssueView                  Operation = "issue-view"
	OpIssueEditProject           Operation = "issue-edit-project"
	OpIssueEditView              Operation = "issue-edit-view"
	OpIssueEditLabelList         Operation = "issue-edit-label-list"
	OpIssueEdit                  Operation = "issue-edit"
	OpMRList                     Operation = "mr-list"
	OpMRView                     Operation = "mr-view"
	OpMRDiff                     Operation = "mr-diff"
	OpMRChecks                   Operation = "mr-checks"
	OpMRDiscussions              Operation = "mr-discussions"
	OpMRDiscussionsTargetProject Operation = "mr-discussions-target-project"
	OpMRDiscussionsSourceProject Operation = "mr-discussions-source-project"
	OpPipelineList               Operation = "pipeline-list"
	OpPipelineView               Operation = "pipeline-view"
	OpJobList                    Operation = "job-list"
	OpJobView                    Operation = "job-view"
	OpJobTrace                   Operation = "job-trace"
	OpReleaseList                Operation = "release-list"
	OpReleaseView                Operation = "release-view"
	OpRepoList                   Operation = "repo-list"
	OpRepoView                   Operation = "repo-view"
	OpLabelList                  Operation = "label-list"
	OpSearch                     Operation = "search"
	OpEnsureProject              Operation = "mr-ensure-project"
	OpEnsureList                 Operation = "mr-ensure-list"
	OpEnsureCreate               Operation = "mr-ensure-create"
	OpEnsureUpdate               Operation = "mr-ensure-update"
	OpMergeProject               Operation = "mr-merge-project"
	OpMergeMRView                Operation = "mr-merge-view"
	OpMergeJobList               Operation = "mr-merge-job-list"
	OpMergeBridgeList            Operation = "mr-merge-bridge-list"
	OpMRMerge                    Operation = "mr-merge"
)

type Request struct {
	Operation  Operation
	Host       string
	Repo       string
	ID         int64
	IID        int64
	PipelineID int64
	Page       int
	PerPage    int
	Tag        string
	Scope      string
	Query      string
	Source     string
	Target     string
	InputFile  string
}

type invocation struct {
	args       []string
	host       string
	maxStdout  int
	write      bool
	outputKind outputKind
}

type outputKind int

const (
	outputJSON outputKind = iota
	outputText
)

func build(request Request) (invocation, error) {
	if err := validateHost(request.Host); err != nil {
		return invocation{}, err
	}
	if operationNeedsRepo(request.Operation) {
		if err := safeurl.ValidateProject(request.Repo); err != nil {
			return invocation{}, uxv1.Wrap(uxv1.CodeValidation, "invalid repository target", err)
		}
	}
	pageArgs := func() ([]string, error) {
		if request.Page < 1 || request.Page > limits.MaxPages || request.PerPage < 1 || request.PerPage > 100 {
			return nil, uxv1.NewError(uxv1.CodeValidation, "page must be 1..10 and per-page must be 1..100")
		}
		return []string{"--page", strconv.Itoa(request.Page), "--per-page", strconv.Itoa(request.PerPage)}, nil
	}
	repoArgs := func() []string { return []string{"-R", request.Repo} }
	apiPrefix := func() []string { return []string{"api", "--method", "GET", "--hostname", request.Host} }
	escapedRepo := url.PathEscape(request.Repo)
	jsonPage := func(args []string) invocation {
		return invocation{args: args, host: request.Host, maxStdout: limits.MaxJSONPageBytes, outputKind: outputJSON}
	}
	jsonObject := func(args []string) invocation {
		return invocation{args: args, host: request.Host, maxStdout: limits.MaxJSONPageBytes, outputKind: outputJSON}
	}

	switch request.Operation {
	case OpIssueList, OpMRList, OpPipelineList, OpReleaseList, OpLabelList:
		page, err := pageArgs()
		if err != nil {
			return invocation{}, err
		}
		base := map[Operation][]string{
			OpIssueList:    {"issue", "list", "--output", "json"},
			OpMRList:       {"mr", "list", "--output", "json"},
			OpPipelineList: {"ci", "list", "--output", "json"},
			OpReleaseList:  {"release", "list", "--output", "json"},
			OpLabelList:    {"label", "list", "--output", "json"},
		}[request.Operation]
		base = append(base, page...)
		base = append(base, repoArgs()...)
		return jsonPage(base), nil
	case OpRepoList:
		page, err := pageArgs()
		if err != nil {
			return invocation{}, err
		}
		return jsonPage(append([]string{"repo", "list", "--output", "json"}, page...)), nil
	case OpIssueView, OpMRView:
		id := request.IID
		if id < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "resource IID must be a positive integer")
		}
		group := "issue"
		if request.Operation == OpMRView {
			group = "mr"
		}
		args := []string{group, "view", strconv.FormatInt(id, 10), "--output", "json"}
		return jsonObject(append(args, repoArgs()...)), nil
	case OpIssueEditView:
		if request.IID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "issue IID must be a positive integer")
		}
		endpoint := fmt.Sprintf("projects/%s/issues/%d", escapedRepo, request.IID)
		return jsonObject(append(apiPrefix(), endpoint)), nil
	case OpIssueEditLabelList:
		if _, err := pageArgs(); err != nil {
			return invocation{}, err
		}
		query := url.Values{
			"include_ancestor_groups": {"true"},
			"page":                    {strconv.Itoa(request.Page)},
			"per_page":                {strconv.Itoa(request.PerPage)},
		}.Encode()
		endpoint := "projects/" + escapedRepo + "/labels?" + query
		return jsonPage(append(apiPrefix(), endpoint)), nil
	case OpMRDiff:
		if request.IID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "merge request IID must be a positive integer")
		}
		args := []string{"mr", "diff", strconv.FormatInt(request.IID, 10), "--color", "never"}
		return invocation{args: append(args, repoArgs()...), host: request.Host, maxStdout: limits.MaxOperationBytes, outputKind: outputText}, nil
	case OpMRChecks:
		if request.IID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "merge request IID must be a positive integer")
		}
		args := []string{"ci", "get", "--merge-request", strconv.FormatInt(request.IID, 10), "--output", "json"}
		return invocation{args: append(args, repoArgs()...), host: request.Host, maxStdout: limits.MaxOperationBytes, outputKind: outputJSON}, nil
	case OpMRDiscussions:
		if request.IID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "merge request IID must be a positive integer")
		}
		if _, err := pageArgs(); err != nil {
			return invocation{}, err
		}
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%d/discussions?page=%d&per_page=%d", escapedRepo, request.IID, request.Page, request.PerPage)
		return jsonPage(append(apiPrefix(), endpoint)), nil
	case OpMRDiscussionsTargetProject:
		return jsonObject(append(apiPrefix(), "projects/"+escapedRepo)), nil
	case OpMRDiscussionsSourceProject:
		if request.ID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "source project ID must be a positive integer")
		}
		return jsonObject(append(apiPrefix(), "projects/"+strconv.FormatInt(request.ID, 10))), nil
	case OpPipelineView:
		if request.ID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "pipeline ID must be a positive integer")
		}
		endpoint := fmt.Sprintf("projects/%s/pipelines/%d", escapedRepo, request.ID)
		return jsonObject(append(apiPrefix(), endpoint)), nil
	case OpJobList:
		if request.PipelineID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "pipeline ID must be a positive integer")
		}
		if _, err := pageArgs(); err != nil {
			return invocation{}, err
		}
		endpoint := fmt.Sprintf("projects/%s/pipelines/%d/jobs?page=%d&per_page=%d", escapedRepo, request.PipelineID, request.Page, request.PerPage)
		return jsonPage(append(apiPrefix(), endpoint)), nil
	case OpJobView, OpJobTrace:
		if request.ID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "job ID must be a positive integer")
		}
		endpoint := fmt.Sprintf("projects/%s/jobs/%d", escapedRepo, request.ID)
		if request.Operation == OpJobTrace {
			endpoint += "/trace"
			return invocation{args: append(apiPrefix(), endpoint), host: request.Host, maxStdout: limits.MaxOperationBytes, outputKind: outputText}, nil
		}
		return jsonObject(append(apiPrefix(), endpoint)), nil
	case OpReleaseView:
		args := []string{"release", "view"}
		if request.Tag != "" {
			if err := validateText(request.Tag, "release tag", 512); err != nil || strings.HasPrefix(request.Tag, "-") {
				return invocation{}, uxv1.NewError(uxv1.CodeValidation, "release tag is invalid")
			}
			args = append(args, request.Tag)
		}
		args = append(args, "--output", "json")
		return jsonObject(append(args, repoArgs()...)), nil
	case OpRepoView:
		return jsonObject([]string{"repo", "view", request.Repo, "--output", "json"}), nil
	case OpSearch:
		page, err := pageArgs()
		if err != nil {
			return invocation{}, err
		}
		_ = page
		if err := validateText(request.Query, "search query", 1024); err != nil {
			return invocation{}, err
		}
		scopeMap := map[string]string{"issues": "issues", "mrs": "merge_requests", "repos": "projects", "commits": "commits", "code": "blobs"}
		scope, ok := scopeMap[request.Scope]
		if !ok {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "search scope must be issues, mrs, repos, commits, or code")
		}
		if request.Scope != "repos" {
			if err := safeurl.ValidateProject(request.Repo); err != nil {
				return invocation{}, uxv1.Wrap(uxv1.CodeValidation, "invalid repository target", err)
			}
		}
		query := url.Values{"scope": {scope}, "search": {request.Query}, "page": {strconv.Itoa(request.Page)}, "per_page": {strconv.Itoa(request.PerPage)}}.Encode()
		endpoint := "search?" + query
		if request.Scope != "repos" {
			endpoint = "projects/" + escapedRepo + "/search?" + query
		}
		return jsonPage(append(apiPrefix(), endpoint)), nil
	case OpEnsureProject, OpMergeProject, OpIssueEditProject:
		return jsonObject(append(apiPrefix(), "projects/"+escapedRepo)), nil
	case OpMergeMRView:
		if request.IID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "merge request IID must be a positive integer")
		}
		endpoint := fmt.Sprintf("projects/%s/merge_requests/%d?with_merge_status_recheck=true", escapedRepo, request.IID)
		return jsonObject(append(apiPrefix(), endpoint)), nil
	case OpMergeJobList, OpMergeBridgeList:
		if request.PipelineID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "pipeline ID must be a positive integer")
		}
		if _, err := pageArgs(); err != nil {
			return invocation{}, err
		}
		resource := "jobs"
		if request.Operation == OpMergeBridgeList {
			resource = "bridges"
		}
		endpoint := fmt.Sprintf("projects/%s/pipelines/%d/%s?include_retried=false&page=%d&per_page=%d", escapedRepo, request.PipelineID, resource, request.Page, request.PerPage)
		return jsonPage(append(apiPrefix(), endpoint)), nil
	case OpEnsureList:
		if _, err := pageArgs(); err != nil {
			return invocation{}, err
		}
		if err := validateText(request.Source, "source branch", limits.MaxBranchBytes); err != nil {
			return invocation{}, err
		}
		if err := validateText(request.Target, "target branch", limits.MaxBranchBytes); err != nil {
			return invocation{}, err
		}
		query := url.Values{"state": {"opened"}, "source_branch": {request.Source}, "target_branch": {request.Target}, "page": {strconv.Itoa(request.Page)}, "per_page": {strconv.Itoa(request.PerPage)}}.Encode()
		endpoint := "projects/" + escapedRepo + "/merge_requests?" + query
		return jsonPage(append(apiPrefix(), endpoint)), nil
	case OpEnsureCreate, OpEnsureUpdate, OpMRMerge, OpIssueEdit:
		if err := validatePrivateInputPath(request.InputFile); err != nil {
			return invocation{}, err
		}
		method := "POST"
		endpoint := "projects/" + escapedRepo + "/merge_requests"
		switch request.Operation {
		case OpEnsureUpdate:
			if request.IID < 1 {
				return invocation{}, uxv1.NewError(uxv1.CodeValidation, "merge request IID must be a positive integer")
			}
			method = "PUT"
			endpoint += "/" + strconv.FormatInt(request.IID, 10)
		case OpMRMerge:
			if request.IID < 1 {
				return invocation{}, uxv1.NewError(uxv1.CodeValidation, "merge request IID must be a positive integer")
			}
			method = "PUT"
			endpoint += "/" + strconv.FormatInt(request.IID, 10) + "/merge"
		case OpIssueEdit:
			if request.IID < 1 {
				return invocation{}, uxv1.NewError(uxv1.CodeValidation, "issue IID must be a positive integer")
			}
			method = "PUT"
			endpoint = "projects/" + escapedRepo + "/issues/" + strconv.FormatInt(request.IID, 10)
		}
		args := []string{"api", "--method", method, "--hostname", request.Host, endpoint, "--input", request.InputFile, "--header", "Content-Type: application/json"}
		return invocation{args: args, host: request.Host, maxStdout: limits.MaxJSONPageBytes, write: true, outputKind: outputJSON}, nil
	default:
		return invocation{}, uxv1.NewError(uxv1.CodeUnsupported, "official-glab operation is not declared")
	}
}

func operationNeedsRepo(op Operation) bool {
	switch op {
	case OpRepoList, OpMRDiscussionsSourceProject:
		return false
	case OpSearch:
		return false // validated after the scope is known
	default:
		return true
	}
}

func validateHost(host string) error {
	if err := safeurl.ValidateHost(host); err != nil {
		return uxv1.Wrap(uxv1.CodeValidation, "invalid GitLab hostname", err)
	}
	return nil
}

func validateText(value, label string, max int) error {
	if value == "" || len(value) > max || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return uxv1.NewError(uxv1.CodeValidation, label+" is invalid")
	}
	return nil
}

func validatePrivateInputPath(path string) error {
	if path == "" || !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
		return uxv1.NewError(uxv1.CodeSafety, "private JSON input path must be absolute")
	}
	return nil
}
