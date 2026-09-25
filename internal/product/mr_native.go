package product

import (
	"context"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/privatefile"
	"gl-axi/internal/productnative"
)

const (
	mrOpNoteCreate glab.Operation = "mr-note-create"
	mrOpNoteView   glab.Operation = "mr-note-view"
)

// Only Do is shared with legacy ensure. The native feature adapter maps this
// closed MR operation set to the landed HTTP boundary, not to a glab child.
// This lets native ensure retain the shipped reconciliation algorithm.
type mrOperationClient interface {
	Do(context.Context, glab.Request) (glab.Response, error)
}

type nativeMROperations struct {
	client *productnative.Client
	target Target
}

func (c nativeMROperations) Do(ctx context.Context, request glab.Request) (glab.Response, error) {
	if request.Host != c.target.Host || request.Repo != c.target.Repo {
		return glab.Response{}, uxv1.NewError(uxv1.CodeSafety, "native MR operation changed its selected target")
	}
	base := "projects/" + url.PathEscape(c.target.Repo)
	r := productnative.Request{Method: http.MethodGet, Path: base, MaxBytes: limits.MaxJSONPageBytes}
	mrPath := func() (string, error) {
		if request.IID <= 0 {
			return "", uxv1.NewError(uxv1.CodeValidation, "merge request IID must be positive")
		}
		return base + "/merge_requests/" + strconv.FormatInt(request.IID, 10), nil
	}
	switch request.Operation {
	case glab.OpEnsureProject, glab.OpMRDiscussionsTargetProject:
	case glab.OpEnsureList:
		if request.Page < 1 || request.Page > limits.MaxPages || request.PerPage < 1 || request.PerPage > 100 || validBranch(request.Source) != nil || validBranch(request.Target) != nil {
			return glab.Response{}, uxv1.NewError(uxv1.CodeValidation, "invalid native MR branch lookup bounds")
		}
		r.Path += "/merge_requests"
		r.Query = url.Values{"state": {"opened"}, "source_branch": {request.Source}, "target_branch": {request.Target}, "page": {strconv.Itoa(request.Page)}, "per_page": {strconv.Itoa(request.PerPage)}}
	case glab.OpEnsureCreate:
		r.Method, r.Path = http.MethodPost, base+"/merge_requests"
	case glab.OpMRView, glab.OpEnsureUpdate, mrOpNoteCreate, mrOpNoteView:
		path, err := mrPath()
		if err != nil {
			return glab.Response{}, err
		}
		r.Path = path
		switch request.Operation {
		case glab.OpEnsureUpdate:
			r.Method = http.MethodPut
		case mrOpNoteCreate:
			r.Method, r.Path = http.MethodPost, path+"/notes"
		case mrOpNoteView:
			if request.ID < 1 {
				return glab.Response{}, uxv1.NewError(uxv1.CodeValidation, "note ID must be positive")
			}
			r.Path += "/notes/" + strconv.FormatInt(request.ID, 10)
		}
	default:
		return glab.Response{}, uxv1.NewError(uxv1.CodeSecurityBoundary, "operation is outside the native MR write contract")
	}
	write := r.Method != http.MethodGet
	if write {
		// Input is the descriptor-checked private JSON produced by the typed MR
		// handlers, never public arbitrary fields, JSON, headers or paths.
		body, err := privatefile.Read(request.InputFile, limits.MaxJSONPageBytes, false)
		if err != nil {
			return glab.Response{}, err
		}
		r.Body = []byte(body)
		r.Headers = http.Header{"Content-Type": {"application/json"}}
	}
	response, err := c.client.Do(ctx, r)
	out := glab.Response{Body: response.Body, Write: write}
	if err != nil {
		return out, err
	}
	media, _, mediaErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if mediaErr != nil || media != "application/json" {
		return out, uxv1.NewError(uxv1.CodeUpstream, "native MR response must be JSON")
	}
	if request.Operation == glab.OpMRView {
		out.Body, err = glab.NormalizeMRViewResponse(response.Body)
	}
	return out, err
}

func executeNativeMR(ctx context.Context, parsed Parsed, deps Dependencies, meta uxv1.Meta) (out commandOutput, err error) {
	meta.Backend, meta.UpstreamVersion = "native", ""
	out.meta = meta
	defer func() {
		if err != nil {
			classified := *uxv1.AsError(err)
			classified.Message = strings.ReplaceAll(classified.Message, "official glab", "GitLab")
			err = &classified
		}
	}()
	path := strings.Join(parsed.Definition.Path, " ")
	if path == "mr ensure" || path == "mr create-or-update" {
		if _, _, err = readMREnsureContent(parsed); err != nil {
			return out, err
		}
	} else if parsed.Values["--body-file"] != "" {
		if _, err = readMRNoteBody(parsed.Values["--body-file"]); err != nil {
			return out, err
		}
	}
	client, err := openNative(ctx, parsed, deps)
	if err != nil {
		return out, err
	}
	defer client.Close()
	host := client.Host()
	meta.Host = host.Name
	out.meta = meta
	target := Target{Host: host.Authority.Web.Host, Repo: parsed.Values["--repo"], webBase: host.Authority.Web.String()}
	if path != "mr ensure" && path != "mr create-or-update" {
		iid, _ := mergeIID(parsed)
		if parsed.Values["--expected-url"] != mrTargetURL(target, iid) {
			return out, uxv1.NewError(uxv1.CodeSafety, "--expected-url does not match the configured native web authority and selected MR")
		}
	}
	operations := nativeMROperations{client: client, target: target}
	if path == "mr ensure" || path == "mr create-or-update" {
		return executeMREnsure(ctx, operations, target, parsed, meta)
	}
	return executeMRWrite(ctx, operations, target, parsed, meta)
}

func mrTargetProjectURL(target Target) string {
	if target.webBase != "" {
		return strings.TrimSuffix(target.webBase, "/") + "/" + target.Repo
	}
	return canonicalProjectURL(target.Host, target.Repo)
}

func mrTargetURL(target Target, iid int64) string {
	return mrTargetProjectURL(target) + "/-/merge_requests/" + strconv.FormatInt(iid, 10)
}
