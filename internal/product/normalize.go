package product

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"glab-axi/internal/contract/uxv1"
	"glab-axi/internal/gitlab"
	"glab-axi/internal/limits"
	"glab-axi/internal/redact"
	"glab-axi/internal/safeurl"
)

type upstreamUser struct {
	Username string `json:"username"`
}

type upstreamIssue struct {
	ID             int64        `json:"id"`
	IID            int64        `json:"iid"`
	Title          string       `json:"title"`
	Description    string       `json:"description"`
	State          string       `json:"state"`
	WebURL         string       `json:"web_url"`
	Author         upstreamUser `json:"author"`
	Labels         []string     `json:"labels"`
	Confidential   bool         `json:"confidential"`
	UserNotesCount int          `json:"user_notes_count"`
	CreatedAt      *time.Time   `json:"created_at"`
	UpdatedAt      *time.Time   `json:"updated_at"`
}

type upstreamPipeline struct {
	ID        int64        `json:"id"`
	Status    string       `json:"status"`
	Source    string       `json:"source"`
	Ref       string       `json:"ref"`
	SHA       string       `json:"sha"`
	WebURL    string       `json:"web_url"`
	User      upstreamUser `json:"user"`
	CreatedAt *time.Time   `json:"created_at"`
	UpdatedAt *time.Time   `json:"updated_at"`
}

type upstreamMR struct {
	ID                          int64             `json:"id"`
	IID                         int64             `json:"iid"`
	Title                       string            `json:"title"`
	Description                 string            `json:"description"`
	State                       string            `json:"state"`
	Draft                       bool              `json:"draft"`
	WebURL                      string            `json:"web_url"`
	SourceBranch                string            `json:"source_branch"`
	TargetBranch                string            `json:"target_branch"`
	SourceProjectID             int64             `json:"source_project_id"`
	TargetProjectID             int64             `json:"target_project_id"`
	SHA                         string            `json:"sha"`
	HasConflicts                bool              `json:"has_conflicts"`
	DetailedMergeStatus         string            `json:"detailed_merge_status"`
	MergeStatus                 string            `json:"merge_status"`
	Author                      upstreamUser      `json:"author"`
	Labels                      []string          `json:"labels"`
	HeadPipeline                *upstreamPipeline `json:"head_pipeline"`
	Squash                      bool              `json:"squash"`
	ShouldRemoveSourceBranch    bool              `json:"should_remove_source_branch"`
	ForceRemoveSourceBranch     bool              `json:"force_remove_source_branch"`
	MergeWhenPipelineSucceeds   bool              `json:"merge_when_pipeline_succeeds"`
	BlockingDiscussionsResolved bool              `json:"blocking_discussions_resolved"`
	MergeCommitSHA              string            `json:"merge_commit_sha"`
	SquashCommitSHA             string            `json:"squash_commit_sha"`
	MergedAt                    *time.Time        `json:"merged_at"`
	MergeUser                   *upstreamUser     `json:"merge_user"`
	MergedBy                    *upstreamUser     `json:"merged_by"`
	CreatedAt                   *time.Time        `json:"created_at"`
	UpdatedAt                   *time.Time        `json:"updated_at"`
}

type upstreamJob struct {
	ID                 int64             `json:"id"`
	Name               string            `json:"name"`
	Stage              string            `json:"stage"`
	Status             string            `json:"status"`
	AllowFailure       bool              `json:"allow_failure"`
	FailureReason      string            `json:"failure_reason"`
	WebURL             string            `json:"web_url"`
	FinishedAt         *time.Time        `json:"finished_at"`
	Pipeline           *upstreamPipeline `json:"pipeline"`
	DownstreamPipeline *upstreamPipeline `json:"downstream_pipeline"`
}

type upstreamChecks struct {
	upstreamPipeline
	Jobs []upstreamJob `json:"jobs"`
}

type upstreamReleaseAssetSource struct {
	Format string `json:"format"`
	URL    string `json:"url"`
}

type upstreamReleaseAssetLink struct {
	Name           string `json:"name"`
	DirectAssetURL string `json:"direct_asset_url"`
	LinkType       string `json:"link_type"`
}

type upstreamRelease struct {
	Name        string `json:"name"`
	TagName     string `json:"tag_name"`
	Description string `json:"description"`
	CreatedAt   string `json:"created_at"`
	ReleasedAt  string `json:"released_at"`
	Upcoming    bool   `json:"upcoming_release"`
	Assets      struct {
		Count   int                          `json:"count"`
		Sources []upstreamReleaseAssetSource `json:"sources"`
		Links   []upstreamReleaseAssetLink   `json:"links"`
	} `json:"assets"`
	Links struct {
		Self string `json:"self"`
	} `json:"_links"`
}

type upstreamRepo struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	PathWithNamespace string `json:"path_with_namespace"`
	Description       string `json:"description"`
	WebURL            string `json:"web_url"`
	DefaultBranch     string `json:"default_branch"`
	Visibility        string `json:"visibility"`
	Archived          bool   `json:"archived"`
	LastActivityAt    string `json:"last_activity_at"`
}

type upstreamLabel struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description"`
	Color             string `json:"color"`
	TextColor         string `json:"text_color"`
	OpenIssuesCount   int    `json:"open_issues_count"`
	ClosedIssuesCount int    `json:"closed_issues_count"`
	OpenMRsCount      int    `json:"open_merge_requests_count"`
}

type Issue struct {
	IID            int64      `json:"iid"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	State          string     `json:"state"`
	WebURL         string     `json:"web_url"`
	Author         string     `json:"author,omitempty"`
	Labels         []string   `json:"labels,omitempty"`
	Confidential   bool       `json:"confidential"`
	UserNotesCount int        `json:"user_notes_count"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
}

type MergeRequest struct {
	IID            int64      `json:"iid"`
	Title          string     `json:"title"`
	Description    string     `json:"description,omitempty"`
	State          string     `json:"state"`
	Draft          bool       `json:"draft"`
	WebURL         string     `json:"web_url"`
	SourceBranch   string     `json:"source_branch"`
	TargetBranch   string     `json:"target_branch"`
	HeadSHA        string     `json:"head_sha,omitempty"`
	HasConflicts   bool       `json:"has_conflicts"`
	MergeStatus    string     `json:"merge_status"`
	RawMergeStatus string     `json:"raw_merge_status,omitempty"`
	Author         string     `json:"author,omitempty"`
	Labels         []string   `json:"labels,omitempty"`
	HeadPipeline   *Pipeline  `json:"head_pipeline,omitempty"`
	CreatedAt      *time.Time `json:"created_at,omitempty"`
	UpdatedAt      *time.Time `json:"updated_at,omitempty"`
}

type Pipeline struct {
	ID        int64      `json:"id"`
	Status    string     `json:"status"`
	RawStatus string     `json:"raw_status,omitempty"`
	Source    string     `json:"source,omitempty"`
	Ref       string     `json:"ref,omitempty"`
	SHA       string     `json:"sha,omitempty"`
	WebURL    string     `json:"web_url,omitempty"`
	User      string     `json:"user,omitempty"`
	CreatedAt *time.Time `json:"created_at,omitempty"`
	UpdatedAt *time.Time `json:"updated_at,omitempty"`
}

type Job struct {
	ID            int64      `json:"id"`
	Name          string     `json:"name"`
	Stage         string     `json:"stage"`
	Status        string     `json:"status"`
	RawStatus     string     `json:"raw_status,omitempty"`
	AllowFailure  bool       `json:"allow_failure"`
	FailureReason string     `json:"failure_reason,omitempty"`
	WebURL        string     `json:"web_url,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty"`
}

type ReleaseAsset struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Format   string `json:"format,omitempty"`
	LinkType string `json:"link_type,omitempty"`
	URL      string `json:"url"`
}

type Release struct {
	Name        string         `json:"name"`
	Tag         string         `json:"tag"`
	Description string         `json:"description,omitempty"`
	WebURL      string         `json:"web_url,omitempty"`
	CreatedAt   string         `json:"created_at,omitempty"`
	ReleasedAt  string         `json:"released_at,omitempty"`
	Upcoming    bool           `json:"upcoming"`
	AssetsCount int            `json:"assets_count"`
	Assets      []ReleaseAsset `json:"assets,omitempty"`
}

type Repository struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	PathWithNamespace string `json:"path_with_namespace"`
	Description       string `json:"description,omitempty"`
	WebURL            string `json:"web_url"`
	DefaultBranch     string `json:"default_branch,omitempty"`
	Visibility        string `json:"visibility,omitempty"`
	Archived          bool   `json:"archived"`
	LastActivityAt    string `json:"last_activity_at,omitempty"`
}

type Label struct {
	ID                int64  `json:"id"`
	Name              string `json:"name"`
	Description       string `json:"description,omitempty"`
	Color             string `json:"color,omitempty"`
	TextColor         string `json:"text_color,omitempty"`
	OpenIssuesCount   int    `json:"open_issues_count"`
	ClosedIssuesCount int    `json:"closed_issues_count"`
	OpenMRsCount      int    `json:"open_merge_requests_count"`
}

func normalizeIssues(body []byte, host, repo string, includeDescription bool) ([]Issue, bool, error) {
	var source []upstreamIssue
	if err := decodeStrict(body, &source); err != nil {
		return nil, false, err
	}
	out := make([]Issue, 0, len(source))
	truncated := false
	for _, item := range source {
		normalized, cut, err := normalizeIssue(item, host, repo, includeDescription)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || cut
		out = append(out, normalized)
	}
	return out, truncated, nil
}

func normalizeIssueObject(body []byte, host, repo string) (Issue, bool, error) {
	var source upstreamIssue
	if err := decodeStrict(body, &source); err != nil {
		return Issue{}, false, err
	}
	return normalizeIssue(source, host, repo, true)
}

func normalizeIssue(item upstreamIssue, host, repo string, includeDescription bool) (Issue, bool, error) {
	if item.ID < 1 || item.IID < 1 {
		return Issue{}, false, malformed("issue identity")
	}
	title, cut, err := boundedText(item.Title, "issue title", 4096, true)
	if err != nil {
		return Issue{}, false, err
	}
	web, err := authorityRepoURL(item.WebURL, host, repo, true)
	if err != nil {
		return Issue{}, false, err
	}
	out := Issue{IID: item.IID, Title: title, State: boundedEnum(item.State), WebURL: web, Author: boundedIdentity(item.Author.Username), Labels: boundedLabels(item.Labels), Confidential: item.Confidential, UserNotesCount: max(item.UserNotesCount, 0), CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if includeDescription {
		out.Description, cut, err = boundedText(item.Description, "issue description", limits.MaxDescriptionBytes, false)
		if err != nil {
			return Issue{}, false, err
		}
	}
	return out, cut, nil
}

func normalizeMRs(body []byte, host, repo string, includeDescription bool) ([]MergeRequest, bool, error) {
	var source []upstreamMR
	if err := decodeStrict(body, &source); err != nil {
		return nil, false, err
	}
	out := make([]MergeRequest, 0, len(source))
	truncated := false
	for _, item := range source {
		normalized, cut, err := normalizeMR(item, host, repo, includeDescription)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || cut
		out = append(out, normalized)
	}
	return out, truncated, nil
}

func normalizeMRObject(body []byte, host, repo string) (MergeRequest, bool, error) {
	var source upstreamMR
	if err := decodeStrict(body, &source); err != nil {
		return MergeRequest{}, false, err
	}
	return normalizeMR(source, host, repo, true)
}

func normalizeMR(item upstreamMR, host, repo string, includeDescription bool) (MergeRequest, bool, error) {
	if item.ID < 1 || item.IID < 1 {
		return MergeRequest{}, false, malformed("merge request identity")
	}
	title, cut, err := boundedText(item.Title, "merge request title", 4096, true)
	if err != nil {
		return MergeRequest{}, false, err
	}
	web, err := authorityRepoURL(item.WebURL, host, repo, true)
	if err != nil {
		return MergeRequest{}, false, err
	}
	if err := validBranch(item.SourceBranch); err != nil {
		return MergeRequest{}, false, err
	}
	if err := validBranch(item.TargetBranch); err != nil {
		return MergeRequest{}, false, err
	}
	mergeRaw := item.DetailedMergeStatus
	if mergeRaw == "" {
		mergeRaw = item.MergeStatus
	}
	mergeStatus := gitlab.NormalizeMergeStatus(mergeRaw, item.HasConflicts)
	out := MergeRequest{IID: item.IID, Title: title, State: boundedEnum(item.State), Draft: item.Draft, WebURL: web, SourceBranch: item.SourceBranch, TargetBranch: item.TargetBranch, HeadSHA: validSHAOrEmpty(item.SHA), HasConflicts: item.HasConflicts, MergeStatus: mergeStatus, Author: boundedIdentity(item.Author.Username), Labels: boundedLabels(item.Labels), CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if mergeRaw != "" && mergeStatus == "checking" && mergeRaw != "checking" {
		out.RawMergeStatus = boundedEnum(mergeRaw)
	}
	if item.HeadPipeline != nil {
		pipeline, err := normalizePipeline(*item.HeadPipeline, host, repo)
		if err != nil {
			return MergeRequest{}, false, err
		}
		out.HeadPipeline = &pipeline
	}
	if includeDescription {
		out.Description, cut, err = boundedText(item.Description, "merge request description", limits.MaxDescriptionBytes, false)
		if err != nil {
			return MergeRequest{}, false, err
		}
	}
	return out, cut, nil
}

func normalizePipelines(body []byte, host, repo string) ([]Pipeline, error) {
	var source []upstreamPipeline
	if err := decodeStrict(body, &source); err != nil {
		return nil, err
	}
	out := make([]Pipeline, 0, len(source))
	for _, item := range source {
		normalized, err := normalizePipeline(item, host, repo)
		if err != nil {
			return nil, err
		}
		out = append(out, normalized)
	}
	return out, nil
}

func normalizePipelineObject(body []byte, host, repo string) (Pipeline, error) {
	var source upstreamPipeline
	if err := decodeStrict(body, &source); err != nil {
		return Pipeline{}, err
	}
	return normalizePipeline(source, host, repo)
}

func normalizePipeline(item upstreamPipeline, host, repo string) (Pipeline, error) {
	if item.ID < 1 {
		return Pipeline{}, malformed("pipeline identity")
	}
	web, err := authorityRepoURL(item.WebURL, host, repo, false)
	if err != nil {
		return Pipeline{}, err
	}
	status, known := normalizePipelineStatus(item.Status)
	out := Pipeline{ID: item.ID, Status: status, Source: boundedEnum(item.Source), Ref: boundedIdentity(item.Ref), SHA: validSHAOrEmpty(item.SHA), WebURL: web, User: boundedIdentity(item.User.Username), CreatedAt: item.CreatedAt, UpdatedAt: item.UpdatedAt}
	if !known {
		out.RawStatus = boundedEnum(item.Status)
	}
	return out, nil
}

func normalizeJobs(body []byte, host, repo string) ([]Job, error) {
	var source []upstreamJob
	if err := decodeStrict(body, &source); err != nil {
		return nil, err
	}
	if len(source) > limits.MaxJobs {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned too many jobs")
	}
	return normalizeJobValues(source, host, repo)
}

func normalizeJobObject(body []byte, host, repo string) (Job, error) {
	var source upstreamJob
	if err := decodeStrict(body, &source); err != nil {
		return Job{}, err
	}
	jobs, err := normalizeJobValues([]upstreamJob{source}, host, repo)
	if err != nil {
		return Job{}, err
	}
	return jobs[0], nil
}

func normalizeJobValues(source []upstreamJob, host, repo string) ([]Job, error) {
	native := make([]gitlab.Job, 0, len(source))
	for _, item := range source {
		native = append(native, gitlab.Job{ID: item.ID, Name: item.Name, Stage: item.Stage, Status: item.Status, AllowFailure: item.AllowFailure, FinishedAt: item.FinishedAt})
	}
	normalized, err := gitlab.NormalizeJobs(native)
	if err != nil {
		return nil, err
	}
	out := make([]Job, 0, len(source))
	for index, item := range source {
		web, err := authorityRepoURL(item.WebURL, host, repo, false)
		if err != nil {
			return nil, err
		}
		out = append(out, Job{ID: normalized[index].ID, Name: normalized[index].Name, Stage: normalized[index].Stage, Status: normalized[index].Status, RawStatus: normalized[index].RawStatus, AllowFailure: item.AllowFailure, FailureReason: boundedEnum(item.FailureReason), WebURL: web, FinishedAt: normalized[index].FinishedAt})
	}
	return out, nil
}

func normalizeChecks(body []byte, host, repo string) (Pipeline, []Job, error) {
	var source upstreamChecks
	if err := decodeStrict(body, &source); err != nil {
		return Pipeline{}, nil, err
	}
	pipeline, err := normalizePipeline(source.upstreamPipeline, host, repo)
	if err != nil {
		return Pipeline{}, nil, err
	}
	if len(source.Jobs) > limits.MaxJobs {
		return Pipeline{}, nil, uxv1.NewError(uxv1.CodeUpstream, "official glab returned too many jobs")
	}
	jobs, err := normalizeJobValues(source.Jobs, host, repo)
	return pipeline, jobs, err
}

func normalizeReleases(body []byte, host, repo string, includeDescription bool) ([]Release, bool, error) {
	var source []upstreamRelease
	if err := decodeStrict(body, &source); err != nil {
		return nil, false, err
	}
	out := make([]Release, 0, len(source))
	truncated := false
	for _, item := range source {
		normalized, cut, err := normalizeRelease(item, host, repo, includeDescription)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || cut
		out = append(out, normalized)
	}
	return out, truncated, nil
}

func normalizeReleaseObject(body []byte, host, repo string) (Release, bool, error) {
	var source upstreamRelease
	if err := decodeStrict(body, &source); err != nil {
		return Release{}, false, err
	}
	return normalizeRelease(source, host, repo, true)
}

func normalizeRelease(item upstreamRelease, host, repo string, includeDescription bool) (Release, bool, error) {
	name, cut, err := boundedText(item.Name, "release name", 4096, true)
	if err != nil {
		return Release{}, false, err
	}
	tag, _, err := boundedText(item.TagName, "release tag", 512, true)
	if err != nil {
		return Release{}, false, err
	}
	web, err := authorityRepoURL(item.Links.Self, host, repo, false)
	if err != nil {
		return Release{}, false, err
	}
	assets, assetsCut, err := normalizeReleaseAssets(item, host, repo)
	if err != nil {
		return Release{}, false, err
	}
	out := Release{Name: name, Tag: tag, WebURL: web, CreatedAt: boundedIdentity(item.CreatedAt), ReleasedAt: boundedIdentity(item.ReleasedAt), Upcoming: item.Upcoming, AssetsCount: item.Assets.Count, Assets: assets}
	if out.AssetsCount < len(assets) {
		out.AssetsCount = len(assets)
	}
	if out.AssetsCount < 0 || out.AssetsCount > 1000 {
		return Release{}, false, malformed("release asset count")
	}
	if includeDescription {
		var descriptionCut bool
		out.Description, descriptionCut, err = boundedText(item.Description, "release description", limits.MaxDescriptionBytes, false)
		cut = cut || descriptionCut
		if err != nil {
			return Release{}, false, err
		}
	}
	return out, cut || assetsCut, nil
}

func normalizeReleaseAssets(item upstreamRelease, host, repo string) ([]ReleaseAsset, bool, error) {
	const maxAssets = 100
	assets := make([]ReleaseAsset, 0, min(maxAssets, len(item.Assets.Sources)+len(item.Assets.Links)))
	truncated := false
	appendAsset := func(asset ReleaseAsset) {
		if len(assets) == maxAssets {
			truncated = true
			return
		}
		assets = append(assets, asset)
	}
	for _, source := range item.Assets.Sources {
		format := boundedEnum(source.Format)
		if format == "unknown" {
			return nil, false, malformed("release source format")
		}
		assetURL, err := authorityRepoURL(source.URL, host, repo, true)
		if err != nil {
			return nil, false, err
		}
		appendAsset(ReleaseAsset{Name: "source_" + format, Kind: "source", Format: format, URL: assetURL})
	}
	for _, link := range item.Assets.Links {
		name, cut, err := boundedText(link.Name, "release asset name", 4096, true)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || cut
		if link.DirectAssetURL == "" {
			truncated = true
			continue
		}
		assetURL, err := authorityRepoURL(link.DirectAssetURL, host, repo, true)
		if err != nil {
			return nil, false, err
		}
		appendAsset(ReleaseAsset{Name: name, Kind: "link", LinkType: boundedEnum(link.LinkType), URL: assetURL})
	}
	return assets, truncated, nil
}

func normalizeRepos(body []byte, host string) ([]Repository, bool, error) {
	var source []upstreamRepo
	if err := decodeStrict(body, &source); err != nil {
		return nil, false, err
	}
	out := make([]Repository, 0, len(source))
	truncated := false
	for _, item := range source {
		normalized, cut, err := normalizeRepo(item, host)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || cut
		out = append(out, normalized)
	}
	return out, truncated, nil
}

func normalizeRepoObject(body []byte, host string) (Repository, bool, error) {
	var source upstreamRepo
	if err := decodeStrict(body, &source); err != nil {
		return Repository{}, false, err
	}
	return normalizeRepo(source, host)
}

func normalizeRepo(item upstreamRepo, host string) (Repository, bool, error) {
	if item.ID < 1 || safeurl.ValidateProject(item.PathWithNamespace) != nil {
		return Repository{}, false, malformed("repository identity")
	}
	name, cut, err := boundedText(item.Name, "repository name", 4096, true)
	if err != nil {
		return Repository{}, false, err
	}
	description, descriptionCut, err := boundedText(item.Description, "repository description", limits.MaxDescriptionBytes, false)
	if err != nil {
		return Repository{}, false, err
	}
	web, err := authorityRepoURL(item.WebURL, host, item.PathWithNamespace, true)
	if err != nil {
		return Repository{}, false, err
	}
	return Repository{ID: item.ID, Name: name, PathWithNamespace: item.PathWithNamespace, Description: description, WebURL: web, DefaultBranch: boundedIdentity(item.DefaultBranch), Visibility: boundedEnum(item.Visibility), Archived: item.Archived, LastActivityAt: boundedIdentity(item.LastActivityAt)}, cut || descriptionCut, nil
}

func normalizeLabels(body []byte) ([]Label, bool, error) {
	var source []upstreamLabel
	if err := decodeStrict(body, &source); err != nil {
		return nil, false, err
	}
	out := make([]Label, 0, len(source))
	truncated := false
	for _, item := range source {
		if item.ID < 1 {
			return nil, false, malformed("label identity")
		}
		name, cut, err := boundedText(item.Name, "label name", 4096, true)
		if err != nil {
			return nil, false, err
		}
		description, descriptionCut, err := boundedText(item.Description, "label description", 16<<10, false)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || cut || descriptionCut
		out = append(out, Label{ID: item.ID, Name: name, Description: description, Color: boundedEnum(item.Color), TextColor: boundedEnum(item.TextColor), OpenIssuesCount: max(item.OpenIssuesCount, 0), ClosedIssuesCount: max(item.ClosedIssuesCount, 0), OpenMRsCount: max(item.OpenMRsCount, 0)})
	}
	return out, truncated, nil
}

func normalizeSearch(body []byte, scope, host, repo string) ([]map[string]any, bool, error) {
	var source []map[string]any
	if err := decodeStrict(body, &source); err != nil {
		return nil, false, err
	}
	out := make([]map[string]any, 0, len(source))
	truncated := false
	for _, item := range source {
		normalized := map[string]any{"scope": scope}
		for _, field := range []string{"id", "iid", "project_id", "startline"} {
			if value, ok := positiveJSONNumber(item[field]); ok {
				normalized[field] = value
			}
		}
		for _, field := range []string{"title", "name", "path_with_namespace", "filename", "path", "ref", "sha"} {
			if value, ok := item[field].(string); ok && value != "" {
				bounded, cut, err := boundedText(value, "search field", 4096, field != "path")
				if err != nil {
					return nil, false, err
				}
				truncated = truncated || cut
				normalized[field] = bounded
			}
		}
		if value, ok := item["data"].(string); ok && value != "" {
			bounded, cut, err := boundedText(value, "search excerpt", 16<<10, false)
			if err != nil {
				return nil, false, err
			}
			truncated = truncated || cut
			normalized["excerpt"] = bounded
		}
		for _, field := range []string{"web_url", "url"} {
			if value, ok := item[field].(string); ok && value != "" {
				var web string
				var err error
				if scope == "repos" {
					project, ok := normalized["path_with_namespace"].(string)
					if !ok || safeurl.ValidateProject(project) != nil {
						return nil, false, malformed("search repository identity")
					}
					web, err = authorityRepoURL(value, host, project, true)
				} else {
					web, err = authorityRepoURL(value, host, repo, true)
				}
				if err != nil {
					return nil, false, err
				}
				normalized["web_url"] = web
				break
			}
		}
		if len(normalized) == 1 {
			return nil, false, malformed("search result")
		}
		out = append(out, normalized)
	}
	return out, truncated, nil
}

func decodeStrict(body []byte, destination any) error {
	if len(body) == 0 || !utf8.Valid(body) {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return uxv1.Wrap(uxv1.CodeUpstream, "official glab returned malformed JSON", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned trailing data")
	}
	return nil
}

func authorityURL(raw, host string, required bool) (string, error) {
	if raw == "" && !required {
		return "", nil
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || strings.Contains(parsed.EscapedPath(), "\\") || !strings.EqualFold(parsed.Host, host) {
		return "", uxv1.NewError(uxv1.CodeSafety, "official glab returned a URL outside the selected GitLab authority")
	}
	return parsed.String(), nil
}

func authorityRepoURL(raw, host, repo string, required bool) (string, error) {
	value, err := authorityURL(raw, host, required)
	if err != nil || value == "" {
		return value, err
	}
	parsed, _ := url.Parse(value)
	path := strings.TrimSuffix(parsed.EscapedPath(), "/")
	if marker := strings.Index(path, "/-/"); marker >= 0 {
		path = path[:marker]
	}
	escapedRepo := strings.TrimPrefix((&url.URL{Path: repo}).EscapedPath(), "/")
	if path != "/"+escapedRepo && !strings.HasSuffix(path, "/"+escapedRepo) {
		return "", uxv1.NewError(uxv1.CodeSafety, "official glab returned a URL for a different repository")
	}
	return value, nil
}

func boundedText(value, label string, maxBytes int, required bool) (string, bool, error) {
	if required && value == "" || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return "", false, malformed(label)
	}
	if maxBytes < 0 || len(value) <= maxBytes {
		return value, false, nil
	}
	marker := "…[truncated]"
	cut := maxBytes - len(marker)
	if cut < 0 {
		cut = 0
	}
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut] + marker, true, nil
}

func boundedIdentity(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 1024 || !utf8.ValidString(value) || strings.ContainsRune(value, '\x00') {
		return ""
	}
	return value
}

func boundedEnum(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 128 || !utf8.ValidString(value) || strings.ContainsFunc(value, unicode.IsControl) {
		return "unknown"
	}
	return value
}

func boundedLabels(labels []string) []string {
	if len(labels) > 100 {
		labels = labels[:100]
	}
	out := make([]string, 0, len(labels))
	for _, label := range labels {
		if value := boundedIdentity(label); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func validBranch(value string) error {
	if value == "" || len(value) > limits.MaxBranchBytes || !utf8.ValidString(value) || strings.ContainsAny(value, "\x00\r\n") {
		return malformed("merge request branch")
	}
	return nil
}

func validSHAOrEmpty(value string) string {
	if value == "" {
		return ""
	}
	if len(value) != 40 && len(value) != 64 {
		return ""
	}
	if _, err := hex.DecodeString(value); err != nil {
		return ""
	}
	return strings.ToLower(value)
}

func normalizePipelineStatus(raw string) (string, bool) {
	switch strings.ToLower(raw) {
	case "success":
		return "success", true
	case "failed":
		return "failed", true
	case "canceled":
		return "canceled", true
	case "skipped":
		return "skipped", true
	case "created", "waiting_for_resource", "preparing", "pending", "running", "scheduled", "manual":
		return "running", true
	default:
		return "running", false
	}
}

func positiveJSONNumber(value any) (json.Number, bool) {
	number, ok := value.(json.Number)
	if !ok {
		return "", false
	}
	integer, err := number.Int64()
	return number, err == nil && integer > 0
}

func malformed(field string) error {
	return uxv1.NewError(uxv1.CodeUpstream, "official glab returned invalid "+field)
}

func redactedTrace(body []byte) (string, bool) {
	text := strings.ToValidUTF8(string(body), "�")
	redactor := redact.New()
	if len(text) <= limits.MaxTraceBytes {
		return redactor.String(text), false
	}
	tail := text[len(text)-limits.MaxTraceBytes:]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return "[trace tail truncated]\n" + redactor.String(tail), true
}
