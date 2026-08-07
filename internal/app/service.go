// Package app contains provider-neutral use cases over the typed GitLab client.
package app

import (
	"context"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"glab-axi/internal/config"
	"glab-axi/internal/contract/v1"
	"glab-axi/internal/gitlab"
	"glab-axi/internal/limits"
	"glab-axi/internal/safeurl"
)

type Service struct {
	Client   *gitlab.Client
	Host     config.ResolvedHost
	Project  string
	LocalSHA string
}

type MRResult struct {
	MR     gitlab.MergeRequest `json:"mr"`
	Action string              `json:"action,omitempty"`
}

type CIResult struct {
	MR   gitlab.MergeRequest `json:"mr"`
	Jobs []gitlab.CompatJob  `json:"jobs"`
}

func (s *Service) AuthStatus(ctx context.Context, verifyProject bool) error {
	if _, err := s.Client.AuthenticatedUser(ctx); err != nil {
		return err
	}
	if verifyProject {
		_, err := s.verifiedProject(ctx)
		return err
	}
	return nil
}

func (s *Service) List(ctx context.Context, source, target string) ([]gitlab.MergeRequest, error) {
	if err := safeurl.ValidateBranch(source); err != nil {
		return nil, err
	}
	if target != "" {
		if err := safeurl.ValidateBranch(target); err != nil {
			return nil, err
		}
	}
	project, err := s.verifiedProject(ctx)
	if err != nil {
		return nil, err
	}
	items, err := s.Client.ListOpenMergeRequests(ctx, s.Project, source, target)
	if err != nil {
		return nil, err
	}
	for i := range items {
		if err := s.validateMR(&items[i], project, source, target); err != nil {
			return nil, err
		}
	}
	if len(items) > 1 {
		return nil, v1.NewError(v1.CodeConflict, "multiple matching open merge requests exist")
	}
	return items, nil
}

func (s *Service) Ensure(ctx context.Context, source, target, title, description string) (MRResult, error) {
	if err := validateMRInput(source, target, title, description); err != nil {
		return MRResult{}, err
	}
	items, err := s.List(ctx, source, target)
	if err != nil {
		return MRResult{}, err
	}
	if len(items) == 1 {
		if items[0].Title == title && items[0].Description == description {
			return MRResult{MR: items[0], Action: "unchanged"}, nil
		}
		updated, err := s.Update(ctx, items[0].IID, title, description)
		if err != nil {
			return MRResult{}, err
		}
		updated.Action = "updated"
		return updated, nil
	}
	return s.Create(ctx, source, target, title, description)
}

func (s *Service) Create(ctx context.Context, source, target, title, description string) (MRResult, error) {
	if err := validateMRInput(source, target, title, description); err != nil {
		return MRResult{}, err
	}
	// This lookup is intentionally immediately adjacent to POST. The caller may
	// already have looked up the MR; the second check closes that race window.
	existing, err := s.List(ctx, source, target)
	if err != nil {
		return MRResult{}, err
	}
	if len(existing) == 1 {
		if existing[0].Title == title && existing[0].Description == description {
			return MRResult{MR: existing[0], Action: "replayed"}, nil
		}
		return MRResult{}, v1.NewError(v1.CodeConflict, "a matching merge request already exists with different content")
	}
	project, err := s.verifiedProject(ctx)
	if err != nil {
		return MRResult{}, err
	}
	created, createErr := s.Client.CreateMergeRequest(ctx, s.Project, gitlab.CreateMergeRequest{SourceBranch: source, TargetBranch: target, Title: title, Description: description})
	if createErr == nil {
		if err := s.validateMR(&created, project, source, target); err != nil {
			return MRResult{}, err
		}
		if created.Title != title || created.Description != description {
			return MRResult{}, v1.NewError(v1.CodeSafety, "GitLab returned a merge request with mismatched content")
		}
		return MRResult{MR: created, Action: "created"}, nil
	}
	if !reconcileCreate(createErr) {
		return MRResult{}, createErr
	}
	replayed, lookupErr := s.List(ctx, source, target)
	if lookupErr != nil {
		return MRResult{}, ambiguous(v1.CodeAmbiguousCreate, "merge request create result is ambiguous", lookupErr)
	}
	if len(replayed) == 1 && replayed[0].Title == title && replayed[0].Description == description {
		return MRResult{MR: replayed[0], Action: "replayed"}, nil
	}
	if len(replayed) == 1 {
		return MRResult{}, v1.NewError(v1.CodeConflict, "a concurrently created merge request has different content")
	}
	return MRResult{}, ambiguous(v1.CodeAmbiguousCreate, "merge request create result is ambiguous", createErr)
}

func (s *Service) Update(ctx context.Context, iid int64, title, description string) (MRResult, error) {
	if iid < 1 || len(title) == 0 || len(title) > limits.MaxTitleBytes || len(description) > limits.MaxDescriptionBytes || !utf8.ValidString(title) || !utf8.ValidString(description) || strings.ContainsAny(title, "\x00\r\n") || strings.ContainsRune(description, '\x00') {
		return MRResult{}, v1.NewError(v1.CodeValidation, "invalid merge request update")
	}
	project, err := s.verifiedProject(ctx)
	if err != nil {
		return MRResult{}, err
	}
	current, err := s.Client.MergeRequest(ctx, s.Project, iid)
	if err != nil {
		return MRResult{}, err
	}
	if err := s.validateMR(&current, project, "", ""); err != nil {
		return MRResult{}, err
	}
	if current.Title == title && current.Description == description {
		return MRResult{MR: current, Action: "unchanged"}, nil
	}
	updated, updateErr := s.Client.UpdateMergeRequest(ctx, s.Project, iid, gitlab.UpdateMergeRequest{Title: title, Description: description})
	if updateErr == nil {
		if err := s.validateMR(&updated, project, "", ""); err != nil {
			return MRResult{}, err
		}
		if updated.Title != title || updated.Description != description {
			return MRResult{}, v1.NewError(v1.CodeSafety, "GitLab returned a merge request with mismatched updated content")
		}
		return MRResult{MR: updated, Action: "updated"}, nil
	}
	var typed *v1.Error
	if !errors.As(updateErr, &typed) || !typed.Ambiguous {
		return MRResult{}, updateErr
	}
	after, readErr := s.Client.MergeRequest(ctx, s.Project, iid)
	if readErr == nil && s.validateMR(&after, project, "", "") == nil && after.Title == title && after.Description == description {
		return MRResult{MR: after, Action: "replayed"}, nil
	}
	return MRResult{}, ambiguous(v1.CodeAmbiguousUpdate, "merge request update result is ambiguous", updateErr)
}

func (s *Service) View(ctx context.Context, iid int64) (gitlab.MergeRequest, error) {
	project, err := s.verifiedProject(ctx)
	if err != nil {
		return gitlab.MergeRequest{}, err
	}
	mr, err := s.Client.MergeRequest(ctx, s.Project, iid)
	if err != nil {
		return gitlab.MergeRequest{}, err
	}
	if err := s.validateMR(&mr, project, "", ""); err != nil {
		return gitlab.MergeRequest{}, err
	}
	return mr, nil
}

func (s *Service) CIStatus(ctx context.Context, iid int64) (CIResult, error) {
	mr, err := s.View(ctx, iid)
	if err != nil {
		return CIResult{}, err
	}
	if mr.HeadPipeline == nil || mr.HeadPipeline.ID < 1 {
		return CIResult{MR: mr, Jobs: []gitlab.CompatJob{}}, nil
	}
	if !gitlab.ValidSHA(mr.SHA) || !gitlab.ValidSHA(mr.HeadPipeline.SHA) || !strings.EqualFold(mr.SHA, mr.HeadPipeline.SHA) || s.LocalSHA != "" && !strings.EqualFold(mr.SHA, s.LocalSHA) {
		return CIResult{MR: mr, Jobs: stalePipelineJobs()}, nil
	}
	jobs, err := s.Jobs(ctx, mr.HeadPipeline.ID)
	if err != nil {
		return CIResult{}, err
	}
	for _, raw := range jobs.raw {
		if raw.Pipeline != nil && (raw.Pipeline.ID != mr.HeadPipeline.ID || raw.Pipeline.SHA != "" && !strings.EqualFold(raw.Pipeline.SHA, mr.SHA)) {
			return CIResult{MR: mr, Jobs: stalePipelineJobs()}, nil
		}
	}
	return CIResult{MR: mr, Jobs: jobs.normalized}, nil
}

type JobsResult struct {
	raw        []gitlab.Job
	normalized []gitlab.CompatJob
}

func (s *Service) Jobs(ctx context.Context, pipelineID int64) (JobsResult, error) {
	if _, err := s.verifiedProject(ctx); err != nil {
		return JobsResult{}, err
	}
	raw, err := s.Client.PipelineJobs(ctx, s.Project, pipelineID)
	if err != nil {
		return JobsResult{}, err
	}
	normalized, err := gitlab.NormalizeJobs(raw)
	if err != nil {
		return JobsResult{}, err
	}
	return JobsResult{raw: raw, normalized: normalized}, nil
}

func (s *Service) NormalizedJobs(ctx context.Context, pipelineID int64) ([]gitlab.CompatJob, error) {
	result, err := s.Jobs(ctx, pipelineID)
	return result.normalized, err
}

func (s *Service) Trace(ctx context.Context, jobID int64) ([]byte, bool, error) {
	if _, err := s.verifiedProject(ctx); err != nil {
		return nil, false, err
	}
	return s.Client.JobTrace(ctx, s.Project, jobID)
}

func (s *Service) verifiedProject(ctx context.Context) (gitlab.Project, error) {
	if err := safeurl.ValidateProject(s.Project); err != nil {
		return gitlab.Project{}, err
	}
	project, err := s.Client.Project(ctx, s.Project)
	if err != nil {
		return gitlab.Project{}, err
	}
	if project.PathWithNamespace != s.Project {
		return gitlab.Project{}, v1.NewError(v1.CodeSafety, "GitLab project identity does not match the configured repository")
	}
	if err := s.Host.Authority.ValidateProjectWebURL(project.WebURL, s.Project); err != nil {
		return gitlab.Project{}, err
	}
	return project, nil
}

func (s *Service) validateMR(mr *gitlab.MergeRequest, project gitlab.Project, source, target string) error {
	if mr == nil || mr.IID < 1 || mr.SourceProjectID != project.ID || mr.TargetProjectID != project.ID {
		return v1.NewError(v1.CodeSafety, "fork or mismatched-project merge request refused")
	}
	if len(mr.Title) > limits.MaxTitleBytes || len(mr.Description) > limits.MaxDescriptionBytes || !utf8.ValidString(mr.Title) || !utf8.ValidString(mr.Description) || strings.ContainsAny(mr.Title, "\x00\r\n") || strings.ContainsRune(mr.Description, '\x00') {
		return v1.NewError(v1.CodeUpstream, "GitLab returned invalid merge request content")
	}
	switch mr.State {
	case "opened", "open", "closed", "merged", "locked":
	default:
		return v1.NewError(v1.CodeUpstream, "GitLab returned an unknown merge request state")
	}
	if mr.SHA != "" && !gitlab.ValidSHA(mr.SHA) {
		return v1.NewError(v1.CodeUpstream, "GitLab returned an invalid merge request SHA")
	}
	if mr.HeadPipeline != nil {
		if mr.HeadPipeline.ID < 1 || mr.HeadPipeline.SHA != "" && !gitlab.ValidSHA(mr.HeadPipeline.SHA) || len(mr.HeadPipeline.Status) > 128 || strings.ContainsFunc(mr.HeadPipeline.Status, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
			return v1.NewError(v1.CodeUpstream, "GitLab returned invalid head pipeline metadata")
		}
	}
	if err := s.Host.Authority.ValidateMRWebURL(mr.WebURL, s.Project, mr.IID); err != nil {
		return err
	}
	if source != "" && mr.SourceBranch != source {
		return v1.NewError(v1.CodeSafety, "GitLab returned a mismatched source branch")
	}
	if target != "" && mr.TargetBranch != target {
		return v1.NewError(v1.CodeSafety, "GitLab returned a mismatched target branch")
	}
	if mr.SourceBranch != "" {
		if err := safeurl.ValidateBranch(mr.SourceBranch); err != nil {
			return v1.NewError(v1.CodeSafety, "GitLab returned an invalid source branch")
		}
	}
	if mr.TargetBranch != "" {
		if err := safeurl.ValidateBranch(mr.TargetBranch); err != nil {
			return v1.NewError(v1.CodeSafety, "GitLab returned an invalid target branch")
		}
	}
	mr.DetailedMergeStatus = gitlab.NormalizeMergeStatus(firstNonempty(mr.DetailedMergeStatus, mr.MergeStatus), mr.HasConflicts)
	mr.MergeStatus = mr.DetailedMergeStatus
	return nil
}

func validateMRInput(source, target, title, description string) error {
	if err := safeurl.ValidateBranch(source); err != nil {
		return err
	}
	if err := safeurl.ValidateBranch(target); err != nil {
		return err
	}
	if title == "" || len(title) > limits.MaxTitleBytes || len(description) > limits.MaxDescriptionBytes || !utf8.ValidString(title) || !utf8.ValidString(description) || strings.ContainsAny(title, "\x00\r\n") || strings.ContainsRune(description, '\x00') {
		return v1.NewError(v1.CodeValidation, "merge request title or description exceeds the input contract")
	}
	return nil
}

func reconcileCreate(err error) bool {
	var typed *v1.Error
	return errors.As(err, &typed) && (typed.Ambiguous || typed.StatusCode == 409 || typed.StatusCode == 422)
}

func ambiguous(code v1.Code, message string, cause error) *v1.Error {
	err := v1.Wrap(code, message, cause)
	err.Ambiguous = true
	return err
}

func stalePipelineJobs() []gitlab.CompatJob {
	return []gitlab.CompatJob{{ID: 9_223_372_036_854_775_000, Name: "glab-axi/stale-pipeline", Stage: "safety", Status: "running", RawStatus: "stale_pipeline"}}
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return "checking"
}

func WithTimeout(parent context.Context, write bool) (context.Context, context.CancelFunc) {
	if write {
		return context.WithTimeout(parent, limits.WriteOperation)
	}
	return context.WithTimeout(parent, limits.ShortOperation)
}
