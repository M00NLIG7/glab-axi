package product

import (
	"context"
	"strconv"
	"strings"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

func fetchList[T any](ctx context.Context, client delegateClient, request glab.Request, limit int, normalize func([]byte) ([]T, bool, error)) ([]T, listState, error) {
	state := listState{}
	items := make([]T, 0, limit)
	fieldTruncated := false
	// GitLab page offsets depend on a stable per-page value. Changing the page
	// width between requests can overlap or skip resources.
	perPage := min(100, limit+1)
	for page := 1; page <= 10; page++ {
		request.Page, request.PerPage = page, perPage
		response, err := client.Do(ctx, request)
		if err != nil {
			state.count = min(len(items), limit)
			state.truncated = len(items) > 0
			state.reason = "pagination_failure"
			return nil, state, err
		}
		state.upstreamVersion = response.UpstreamVersion
		pageItems, pageTruncated, err := normalize(response.Body)
		if err != nil {
			state.count = min(len(items), limit)
			state.truncated = len(items) > 0
			state.reason = "provider_response"
			return nil, state, err
		}
		if len(pageItems) > perPage {
			state.count = min(len(items), limit)
			state.truncated = len(items) > 0
			state.reason = "provider_response"
			return nil, state, uxv1.NewError(uxv1.CodeUpstream, "official glab returned more items than requested")
		}
		fieldTruncated = fieldTruncated || pageTruncated
		items = append(items, pageItems...)
		if len(items) > limit {
			state.reason = "display_limit"
			state.truncated = true
			break
		}
		if len(pageItems) < perPage {
			state.complete = true
			break
		}
		if page == 10 {
			state.reason = "hard_page_limit"
			state.truncated = true
		}
	}
	if len(items) > limit {
		items = items[:limit]
		state.truncated = true
		state.complete = false
	}
	if fieldTruncated {
		state.truncated = true
		if state.reason == "" {
			state.reason = "field_limit"
		}
	}
	state.count = len(items)
	return items, state, nil
}

func fetchIssues(ctx context.Context, client delegateClient, target Target, limit int) ([]Issue, listState, error) {
	return fetchList(ctx, client, glab.Request{Operation: glab.OpIssueList, Host: target.Host, Repo: target.Repo}, limit, func(body []byte) ([]Issue, bool, error) {
		return normalizeIssues(body, target.Host, target.Repo, false)
	})
}

func fetchMRs(ctx context.Context, client delegateClient, target Target, limit int) ([]MergeRequest, listState, error) {
	return fetchList(ctx, client, glab.Request{Operation: glab.OpMRList, Host: target.Host, Repo: target.Repo}, limit, func(body []byte) ([]MergeRequest, bool, error) {
		return normalizeMRs(body, target.Host, target.Repo, false)
	})
}

func fetchPipelines(ctx context.Context, client delegateClient, target Target, limit int) ([]Pipeline, listState, error) {
	return fetchList(ctx, client, glab.Request{Operation: glab.OpPipelineList, Host: target.Host, Repo: target.Repo}, limit, func(body []byte) ([]Pipeline, bool, error) {
		items, err := normalizePipelines(body, target.Host, target.Repo)
		return items, false, err
	})
}

func fetchJobs(ctx context.Context, client delegateClient, target Target, parsed Parsed) ([]Job, listState, error) {
	pipelineID, err := strconv.ParseInt(parsed.Values["--pipeline-id"], 10, 64)
	if err != nil || pipelineID < 1 {
		return nil, listState{}, uxv1.NewError(uxv1.CodeValidation, "--pipeline-id must be a positive integer")
	}
	return fetchList(ctx, client, glab.Request{Operation: glab.OpJobList, Host: target.Host, Repo: target.Repo, PipelineID: pipelineID}, parsed.Limit, func(body []byte) ([]Job, bool, error) {
		items, err := normalizeJobs(body, target.Host, target.Repo)
		return items, false, err
	})
}

func fetchReleases(ctx context.Context, client delegateClient, target Target, limit int) ([]Release, listState, error) {
	return fetchList(ctx, client, glab.Request{Operation: glab.OpReleaseList, Host: target.Host, Repo: target.Repo}, limit, func(body []byte) ([]Release, bool, error) {
		return normalizeReleases(body, target.Host, target.Repo, false)
	})
}

func fetchRepos(ctx context.Context, client delegateClient, target Target, limit int) ([]Repository, listState, error) {
	return fetchList(ctx, client, glab.Request{Operation: glab.OpRepoList, Host: target.Host}, limit, func(body []byte) ([]Repository, bool, error) {
		return normalizeRepos(body, target.Host)
	})
}

func fetchLabels(ctx context.Context, client delegateClient, target Target, limit int) ([]Label, listState, error) {
	return fetchList(ctx, client, glab.Request{Operation: glab.OpLabelList, Host: target.Host, Repo: target.Repo}, limit, func(body []byte) ([]Label, bool, error) {
		return normalizeLabels(body)
	})
}

func fetchSearch(ctx context.Context, client delegateClient, target Target, parsed Parsed) ([]map[string]any, listState, error) {
	scope := parsed.Definition.Path[1]
	query := parsed.Positionals[0]
	return fetchList(ctx, client, glab.Request{Operation: glab.OpSearch, Host: target.Host, Repo: target.Repo, Scope: scope, Query: query}, parsed.Limit, func(body []byte) ([]map[string]any, bool, error) {
		return normalizeSearch(body, scope, target.Host, target.Repo)
	})
}

func executeIssueView(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	iid, err := positivePosition(parsed, "issue IID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpIssueView, Host: target.Host, Repo: target.Repo, IID: iid})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	issue, truncated, err := normalizeIssueObject(response.Body, target.Host, target.Repo)
	meta.Truncated, meta.Complete = truncated, true
	if truncated {
		meta.Reason = "field_limit"
	}
	return commandOutput{data: map[string]any{"issue": issue}, meta: meta}, err
}

func executeMRView(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	iid, err := positivePosition(parsed, "merge request IID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpMRView, Host: target.Host, Repo: target.Repo, IID: iid})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	mr, truncated, err := normalizeMRObject(response.Body, target.Host, target.Repo)
	meta.Truncated, meta.Complete = truncated, true
	if truncated {
		meta.Reason = "field_limit"
	}
	return commandOutput{data: map[string]any{"mr": mr}, meta: meta}, err
}

func executeMRChecks(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	iid, err := positivePosition(parsed, "merge request IID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpMRChecks, Host: target.Host, Repo: target.Repo, IID: iid})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	pipeline, jobs, err := normalizeChecks(response.Body, target.Host, target.Repo)
	meta.Count = len(jobs)
	return commandOutput{data: map[string]any{"mr_iid": iid, "pipeline": pipeline, "jobs": jobs}, meta: meta}, err
}

func executeMRDiff(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	iid, err := positivePosition(parsed, "merge request IID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpMRDiff, Host: target.Host, Repo: target.Repo, IID: iid})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	diff, truncated, err := boundedDiff(response.Body)
	meta.Truncated = truncated
	if truncated {
		meta.Complete, meta.Reason = false, "diff_limit"
	}
	return commandOutput{data: map[string]any{"mr_iid": iid, "diff": diff}, meta: meta}, err
}

func executePipelineView(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	id, err := positivePosition(parsed, "pipeline ID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpPipelineView, Host: target.Host, Repo: target.Repo, ID: id})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	pipeline, err := normalizePipelineObject(response.Body, target.Host, target.Repo)
	return commandOutput{data: map[string]any{"pipeline": pipeline}, meta: meta}, err
}

func executeJobView(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	id, err := positivePosition(parsed, "job ID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpJobView, Host: target.Host, Repo: target.Repo, ID: id})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	job, err := normalizeJobObject(response.Body, target.Host, target.Repo)
	return commandOutput{data: map[string]any{"job": job}, meta: meta}, err
}

func executeJobTrace(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	id, err := positivePosition(parsed, "job ID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpJobTrace, Host: target.Host, Repo: target.Repo, ID: id})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	trace, truncated := redactedTrace(response.Body)
	meta.Truncated = truncated
	if truncated {
		meta.Complete, meta.Reason = false, "trace_tail_limit"
	}
	return commandOutput{data: map[string]any{"job_id": id, "trace": trace}, meta: meta}, nil
}

func executeReleaseView(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	tag := ""
	if len(parsed.Positionals) == 1 {
		tag = parsed.Positionals[0]
	}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpReleaseView, Host: target.Host, Repo: target.Repo, Tag: tag})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	release, truncated, err := normalizeReleaseObject(response.Body, target.Host, target.Repo)
	meta.Truncated = truncated
	if truncated {
		meta.Reason = "field_limit"
	}
	return commandOutput{data: map[string]any{"release": release}, meta: meta}, err
}

func executeRepoView(ctx context.Context, client delegateClient, target Target, _ Parsed, meta uxv1.Meta) (commandOutput, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpRepoView, Host: target.Host, Repo: target.Repo})
	meta.UpstreamVersion = response.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	repository, truncated, err := normalizeRepoObject(response.Body, target.Host)
	meta.Truncated = truncated
	if truncated {
		meta.Reason = "field_limit"
	}
	return commandOutput{data: map[string]any{"repository": repository}, meta: meta}, err
}

func executeDashboard(ctx context.Context, client delegateClient, target Target, _ Parsed, meta uxv1.Meta) (commandOutput, error) {
	repoResponse, err := client.Do(ctx, glab.Request{Operation: glab.OpRepoView, Host: target.Host, Repo: target.Repo})
	meta.UpstreamVersion = repoResponse.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	repository, repoTruncated, err := normalizeRepoObject(repoResponse.Body, target.Host)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	issues, issueState, err := fetchIssues(ctx, client, target, 5)
	if err != nil {
		return commandOutput{meta: mergeMeta(meta, issueState)}, err
	}
	mrs, mrState, err := fetchMRs(ctx, client, target, 5)
	if err != nil {
		return commandOutput{meta: mergeMeta(meta, mrState)}, err
	}
	pipelines, pipelineState, err := fetchPipelines(ctx, client, target, 5)
	if err != nil {
		return commandOutput{meta: mergeMeta(meta, pipelineState)}, err
	}
	meta.Complete = issueState.complete && mrState.complete && pipelineState.complete
	meta.Truncated = repoTruncated || issueState.truncated || mrState.truncated || pipelineState.truncated
	meta.Count = len(issues) + len(mrs) + len(pipelines)
	if meta.Truncated {
		meta.Reason = "dashboard_preview_limit"
	}
	return commandOutput{data: map[string]any{"repository": repository, "open_issues": issues, "open_mrs": mrs, "latest_pipelines": pipelines}, meta: meta}, nil
}

func commandScope(parsed Parsed) string {
	if len(parsed.Definition.Path) < 2 {
		return ""
	}
	return strings.ToLower(parsed.Definition.Path[1])
}
