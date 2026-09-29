package product

import (
	"context"

	"gl-axi/internal/delegate/glab"
)

func fetchSelectedIssues(ctx context.Context, client delegateClient, target Target, parsed Parsed) ([]Issue, listState, error) {
	selection, _ := parseReadSelection(parsed)
	request := glab.Request{Operation: glab.OpIssueList, Host: target.Host, Repo: target.Repo, Filters: listFilters(parsed)}
	return fetchList(ctx, client, request, parsed.Limit, func(body []byte) ([]Issue, bool, error) {
		var source []upstreamIssue
		if err := decodeStrict(body, &source); err != nil {
			return nil, false, err
		}
		items := make([]Issue, 0, len(source))
		truncated := false
		for _, item := range source {
			out, cut, err := selection.issue(item, target, 0)
			if err != nil {
				return nil, false, err
			}
			items = append(items, out)
			truncated = truncated || cut
		}
		return items, truncated, nil
	})
}

func fetchSelectedMRs(ctx context.Context, client delegateClient, target Target, parsed Parsed) ([]MergeRequest, listState, error) {
	selection, _ := parseReadSelection(parsed)
	request := glab.Request{Operation: glab.OpMRList, Host: target.Host, Repo: target.Repo, Filters: listFilters(parsed)}
	return fetchList(ctx, client, request, parsed.Limit, func(body []byte) ([]MergeRequest, bool, error) {
		var source []upstreamMR
		if err := decodeStrict(body, &source); err != nil {
			return nil, false, err
		}
		items := make([]MergeRequest, 0, len(source))
		truncated := false
		for _, item := range source {
			out, cut, err := selection.mr(item, target, 0, request.Filters)
			if err != nil {
				return nil, false, err
			}
			items = append(items, out)
			truncated = truncated || cut
		}
		return items, truncated, nil
	})
}
