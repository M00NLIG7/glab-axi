package gitlab

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"gl-axi/internal/contract/v1"
	"gl-axi/internal/limits"
)

func (c *Client) AuthenticatedUser(ctx context.Context) (User, error) {
	endpoint, err := c.host.Authority.Endpoint("user", nil)
	if err != nil {
		return User{}, err
	}
	var user User
	if _, _, err := c.getJSON(ctx, endpoint, &user); err != nil {
		return User{}, err
	}
	if user.ID < 1 || user.Username == "" {
		return User{}, v1.NewError(v1.CodeUpstream, "GitLab returned an invalid authenticated user")
	}
	return user, nil
}

func (c *Client) Project(ctx context.Context, project string) (Project, error) {
	endpoint, err := c.host.Authority.Endpoint("projects/"+url.PathEscape(project), nil)
	if err != nil {
		return Project{}, err
	}
	var result Project
	if _, _, err := c.getJSON(ctx, endpoint, &result); err != nil {
		return Project{}, err
	}
	if result.ID < 1 || result.PathWithNamespace == "" || result.WebURL == "" {
		return Project{}, v1.NewError(v1.CodeUpstream, "GitLab returned invalid project metadata")
	}
	return result, nil
}

func (c *Client) ListOpenMergeRequests(ctx context.Context, project, source, target string) ([]MergeRequest, error) {
	query := url.Values{
		"state":         {"opened"},
		"source_branch": {source},
		"per_page":      {"100"},
	}
	if target != "" {
		query.Set("target_branch", target)
	}
	endpoint, err := c.host.Authority.Endpoint("projects/"+url.PathEscape(project)+"/merge_requests", query)
	if err != nil {
		return nil, err
	}
	var all []MergeRequest
	totalBytes := 0
	seen := map[string]bool{}
	for page := 0; page < limits.MaxPages; page++ {
		if seen[endpoint.String()] {
			return nil, v1.NewError(v1.CodeUpstream, "GitLab pagination loop detected")
		}
		seen[endpoint.String()] = true
		var current []MergeRequest
		header, size, err := c.getJSON(ctx, endpoint, &current)
		if err != nil {
			return nil, err
		}
		for i := range current {
			c.sanitizeMergeRequest(&current[i])
		}
		totalBytes += size
		if totalBytes > limits.MaxOperationBytes {
			return nil, v1.NewError(v1.CodeUpstream, "GitLab paginated response exceeds the operation limit")
		}
		all = append(all, current...)
		if len(all) > limits.MaxJobs {
			return nil, v1.NewError(v1.CodeUpstream, "GitLab merge request count exceeds the operation limit")
		}
		next, err := nextPage(c.host.Authority, endpoint, header)
		if err != nil {
			return nil, err
		}
		if next == nil {
			return all, nil
		}
		endpoint = next
	}
	return nil, v1.NewError(v1.CodeUpstream, "GitLab pagination exceeds the page limit")
}

func (c *Client) MergeRequest(ctx context.Context, project string, iid int64) (MergeRequest, error) {
	if iid < 1 {
		return MergeRequest{}, v1.NewError(v1.CodeValidation, "merge request IID must be positive")
	}
	endpoint, err := c.host.Authority.Endpoint(fmt.Sprintf("projects/%s/merge_requests/%d", url.PathEscape(project), iid), nil)
	if err != nil {
		return MergeRequest{}, err
	}
	var result MergeRequest
	if _, _, err := c.getJSON(ctx, endpoint, &result); err != nil {
		return MergeRequest{}, err
	}
	c.sanitizeMergeRequest(&result)
	return result, nil
}

func (c *Client) CreateMergeRequest(ctx context.Context, project string, input CreateMergeRequest) (MergeRequest, error) {
	endpoint, err := c.host.Authority.Endpoint("projects/"+url.PathEscape(project)+"/merge_requests", nil)
	if err != nil {
		return MergeRequest{}, err
	}
	var result MergeRequest
	if err := c.sendJSON(ctx, http.MethodPost, endpoint, input, &result); err != nil {
		return MergeRequest{}, err
	}
	c.sanitizeMergeRequest(&result)
	return result, nil
}

func (c *Client) UpdateMergeRequest(ctx context.Context, project string, iid int64, input UpdateMergeRequest) (MergeRequest, error) {
	if iid < 1 {
		return MergeRequest{}, v1.NewError(v1.CodeValidation, "merge request IID must be positive")
	}
	endpoint, err := c.host.Authority.Endpoint(fmt.Sprintf("projects/%s/merge_requests/%d", url.PathEscape(project), iid), nil)
	if err != nil {
		return MergeRequest{}, err
	}
	var result MergeRequest
	if err := c.sendJSON(ctx, http.MethodPut, endpoint, input, &result); err != nil {
		return MergeRequest{}, err
	}
	c.sanitizeMergeRequest(&result)
	return result, nil
}

func (c *Client) PipelineJobs(ctx context.Context, project string, pipelineID int64) ([]Job, error) {
	if pipelineID < 1 {
		return nil, v1.NewError(v1.CodeValidation, "pipeline ID must be positive")
	}
	query := url.Values{"per_page": {"100"}, "include_retried": {"false"}}
	endpoint, err := c.host.Authority.Endpoint(fmt.Sprintf("projects/%s/pipelines/%d/jobs", url.PathEscape(project), pipelineID), query)
	if err != nil {
		return nil, err
	}
	var all []Job
	totalBytes := 0
	seen := map[string]bool{}
	for page := 0; page < limits.MaxPages; page++ {
		if seen[endpoint.String()] {
			return nil, v1.NewError(v1.CodeUpstream, "GitLab pagination loop detected")
		}
		seen[endpoint.String()] = true
		var current []Job
		header, size, err := c.getJSON(ctx, endpoint, &current)
		if err != nil {
			return nil, err
		}
		totalBytes += size
		if totalBytes > limits.MaxOperationBytes {
			return nil, v1.NewError(v1.CodeUpstream, "GitLab paginated response exceeds the operation limit")
		}
		for i := range current {
			current[i].Name = c.redactor.String(current[i].Name)
			current[i].Stage = c.redactor.String(current[i].Stage)
			current[i].Status = c.redactor.String(current[i].Status)
		}
		all = append(all, current...)
		if len(all) > limits.MaxJobs {
			return nil, v1.NewError(v1.CodeUpstream, "GitLab job count exceeds the operation limit")
		}
		next, err := nextPage(c.host.Authority, endpoint, header)
		if err != nil {
			return nil, err
		}
		if next == nil {
			return all, nil
		}
		endpoint = next
	}
	return nil, v1.NewError(v1.CodeUpstream, "GitLab pagination exceeds the page limit")
}

func (c *Client) JobTrace(ctx context.Context, project string, jobID int64) ([]byte, bool, error) {
	if jobID < 1 {
		return nil, false, v1.NewError(v1.CodeValidation, "job ID must be positive")
	}
	endpoint, err := c.host.Authority.Endpoint(fmt.Sprintf("projects/%s/jobs/%d/trace", url.PathEscape(project), jobID), nil)
	if err != nil {
		return nil, false, err
	}
	trace, truncated, err := c.getTrace(ctx, endpoint)
	if err != nil {
		return nil, truncated, err
	}
	text := c.redactor.String(strings.ToValidUTF8(string(trace), "�"))
	text = sanitizeTrace(text)
	marker := "[glab-axi: trace truncated to bounded tail]\n"
	if truncated {
		budget := limits.MaxTraceBytes - len(marker)
		if len(text) > budget {
			text = text[len(text)-budget:]
			text = strings.ToValidUTF8(text, "�")
		}
		text = marker + text
	}
	if len(text) > limits.MaxTraceBytes {
		text = strings.ToValidUTF8(text[len(text)-limits.MaxTraceBytes:], "�")
		truncated = true
	}
	return []byte(text), truncated, nil
}

func sanitizeTrace(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '\n', '\t':
			return r
		case '\r':
			return '\n'
		}
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return '�'
		}
		return r
	}, value)
}

func (c *Client) sanitizeMergeRequest(mr *MergeRequest) {
	if mr == nil {
		return
	}
	mr.State = c.redactor.String(mr.State)
	mr.DetailedMergeStatus = c.redactor.String(mr.DetailedMergeStatus)
	mr.MergeStatus = c.redactor.String(mr.MergeStatus)
	if mr.HeadPipeline != nil {
		mr.HeadPipeline.Status = c.redactor.String(mr.HeadPipeline.Status)
	}
}
