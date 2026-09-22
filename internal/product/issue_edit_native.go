package product

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/privatefile"
	"gl-axi/internal/productnative"
)

// This operation is intentionally not an executable official-glab operation.
const issueEditUpdateOperation = glab.OpIssueEditUpdate

// The edit state machine consumes only its four closed operations. Native
// authentication and HTTP policy belong exclusively to productnative.
type issueEditClient interface {
	Do(context.Context, glab.Request) (glab.Response, error)
}

type issueEditTarget struct {
	Target
	projectURL string
	issueURL   string
}

func executeIssueEdit(ctx context.Context, delegated issueEditClient, target Target, parsed Parsed, meta uxv1.Meta, deps Dependencies) (commandOutput, error) {
	// Private content is validated before resolving any native credential.
	requested, err := loadIssueEditRequested(parsed)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	iid, _ := issueEditIID(parsed)
	bound := issueEditTarget{Target: target, projectURL: canonicalProjectURL(target.Host, target.Repo), issueURL: canonicalIssueURL(target.Host, target.Repo, iid)}
	if parsed.Values["--auth-source"] != "native" {
		return executeIssueEditWithClient(ctx, delegated, bound, parsed, requested, meta)
	}
	client, err := openNative(ctx, parsed, deps)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	defer client.Close()
	host := client.Host()
	bound.Host = host.Name
	bound.projectURL = host.Authority.ExpectedProjectURL(target.Repo)
	bound.issueURL = bound.projectURL + "/-/issues/" + strconv.FormatInt(iid, 10)
	meta.Host, meta.Backend, meta.UpstreamVersion = host.Name, "native", ""
	if parsed.Values["--expected-url"] != bound.issueURL {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeSafety, "--expected-url does not match the configured native web authority and selected issue")
	}
	return executeIssueEditWithClient(ctx, issueEditNativeClient{client: client}, bound, parsed, requested, meta)
}

// Route selection is feature-owned. This adapter neither resolves credentials
// nor implements HTTP, retry, redirect, or fallback behavior.
type issueEditNativeClient struct{ client *productnative.Client }

func (c issueEditNativeClient) Do(ctx context.Context, input glab.Request) (glab.Response, error) {
	request := productnative.Request{Method: http.MethodGet, MaxBytes: limits.MaxJSONPageBytes}
	project := "projects/" + url.PathEscape(input.Repo)
	switch input.Operation {
	case glab.OpIssueEditProject:
		request.Path = project
	case glab.OpIssueEditView:
		request.Path = project + "/issues/" + strconv.FormatInt(input.IID, 10)
	case glab.OpIssueEditLabelList:
		request.Path = project + "/labels"
		request.Query = url.Values{"include_ancestor_groups": {"true"}, "page": {strconv.Itoa(input.Page)}, "per_page": {strconv.Itoa(input.PerPage)}}
	case issueEditUpdateOperation:
		if input.ID < 1 || input.IID < 1 {
			return glab.Response{}, uxv1.NewError(uxv1.CodeSafety, "issue mutation requires the validated numeric project and IID")
		}
		body, err := privatefile.Read(input.InputFile, limits.MaxOperationBytes, false)
		if err != nil {
			return glab.Response{}, err
		}
		request.Method = http.MethodPut
		request.Path = "projects/" + strconv.FormatInt(input.ID, 10) + "/issues/" + strconv.FormatInt(input.IID, 10)
		request.Headers = http.Header{"Content-Type": {"application/json"}}
		request.Body = []byte(body)
	default:
		return glab.Response{}, uxv1.NewError(uxv1.CodeUnsupported, "operation is outside the native issue-edit contract")
	}
	response, err := c.client.Do(ctx, request)
	return glab.Response{Body: response.Body, Write: request.Method == http.MethodPut}, err
}
