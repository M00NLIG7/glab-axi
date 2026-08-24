package product

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/gitlab"
	"gl-axi/internal/limits"
	"gl-axi/internal/safeurl"
)

const mergePageSize = 100

type mergeProject struct {
	ID                                        int64  `json:"id"`
	PathWithNamespace                         string `json:"path_with_namespace"`
	WebURL                                    string `json:"web_url"`
	OnlyAllowMergeIfPipelineSucceeds          bool   `json:"only_allow_merge_if_pipeline_succeeds"`
	OnlyAllowMergeIfAllDiscussionsAreResolved bool   `json:"only_allow_merge_if_all_discussions_are_resolved"`
}

type mergeInput struct {
	SHA                      string `json:"sha"`
	Squash                   bool   `json:"squash"`
	ShouldRemoveSourceBranch bool   `json:"should_remove_source_branch"`
	AutoMerge                bool   `json:"auto_merge"`
}

type mergePipelineResult struct {
	ID     int64  `json:"id"`
	SHA    string `json:"sha"`
	Status string `json:"status"`
}

type mergeResult struct {
	Action          string              `json:"action"`
	IID             int64               `json:"iid"`
	WebURL          string              `json:"web_url"`
	SourceBranch    string              `json:"source_branch"`
	TargetBranch    string              `json:"target_branch"`
	SourceHeadSHA   string              `json:"source_head_sha"`
	MergeCommitSHA  string              `json:"merge_commit_sha,omitempty"`
	SquashCommitSHA string              `json:"squash_commit_sha"`
	ResultCommitSHA string              `json:"result_commit_sha"`
	Pipeline        mergePipelineResult `json:"pipeline"`
	Authority       string              `json:"authority"`
}

type mergeOutput struct {
	Merge mergeResult `json:"merge"`
}

type mergeSnapshot struct {
	Pipeline mergePipelineResult
	Checks   int
}

type mergeReadBudget struct {
	bytes int
}

func (b *mergeReadBudget) add(body []byte) error {
	if len(body) > limits.MaxJSONPageBytes {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab merge response exceeded the JSON page limit")
	}
	b.bytes += len(body)
	if b.bytes > limits.MaxOperationBytes {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab merge data exceeded the operation limit")
	}
	return nil
}

// validateParsedCommand keeps every merge identity and authority denial ahead
// of target discovery, credential resolution, executable lookup, and child work.
func validateParsedCommand(parsed Parsed) error {
	if strings.Join(parsed.Definition.Path, " ") != "mr merge" {
		return nil
	}
	if _, err := mergeIID(parsed); err != nil {
		return err
	}
	if err := safeurl.ValidateHost(parsed.Values["--hostname"]); err != nil {
		return uxv1.Wrap(uxv1.CodeValidation, "invalid GitLab hostname", err)
	}
	if err := safeurl.ValidateProject(parsed.Values["--repo"]); err != nil {
		return uxv1.Wrap(uxv1.CodeValidation, "invalid repository target", err)
	}
	if !validMergeSHA(parsed.Values["--expected-head"]) {
		return uxv1.NewError(uxv1.CodeValidation, "--expected-head must be a lowercase 40- or 64-hex SHA")
	}
	switch parsed.Values["--authority"] {
	case "captain-explicit", "standing-yolo-green":
	default:
		return uxv1.NewError(uxv1.CodeValidation, "--authority must be captain-explicit or standing-yolo-green")
	}
	iid, _ := mergeIID(parsed)
	if got, want := parsed.Values["--expected-url"], canonicalMRURL(parsed.Values["--hostname"], parsed.Values["--repo"], iid); got != want {
		return uxv1.NewError(uxv1.CodeSafety, "--expected-url does not exactly match the selected GitLab merge request")
	}
	return nil
}

func mergeIID(parsed Parsed) (int64, error) {
	if len(parsed.Positionals) != 1 {
		return 0, uxv1.NewError(uxv1.CodeValidation, "merge request IID is required")
	}
	raw := parsed.Positionals[0]
	iid, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || iid < 1 || strconv.FormatInt(iid, 10) != raw {
		return 0, uxv1.NewError(uxv1.CodeValidation, "merge request IID must be a canonical positive integer")
	}
	return iid, nil
}

func canonicalMRURL(host, repo string, iid int64) string {
	return (&url.URL{Scheme: "https", Host: host, Path: "/" + repo + "/-/merge_requests/" + strconv.FormatInt(iid, 10)}).String()
}

func canonicalProjectURL(host, repo string) string {
	return (&url.URL{Scheme: "https", Host: host, Path: "/" + repo}).String()
}

func validMergeSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	if err != nil {
		return false
	}
	for _, item := range decoded {
		if item != 0 {
			return true
		}
	}
	return false
}

func executeMRMerge(ctx context.Context, client delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	iid, err := mergeIID(parsed)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	expectedURL := parsed.Values["--expected-url"]
	expectedHead := parsed.Values["--expected-head"]
	authority := parsed.Values["--authority"]
	budget := &mergeReadBudget{}
	preflightCtx, cancelPreflight := context.WithTimeout(ctx, limits.MergePreflightOperation)
	defer cancelPreflight()

	project, err := loadMergeProject(preflightCtx, client, target, &meta, budget)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	record, err := loadMergeMR(preflightCtx, client, target, iid, &meta, budget)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if err := validateMergeMRIdentity(record, target, project.ID, iid, expectedURL, expectedHead); err != nil {
		return commandOutput{meta: meta}, err
	}

	preMerged := record.State == "merged"
	if preMerged {
		if err := validateMergedPostcondition(record, target, project.ID, iid, expectedURL, expectedHead, nil); err != nil {
			return commandOutput{meta: meta}, err
		}
	} else if err := validateOpenMergeMR(record, expectedHead); err != nil {
		return commandOutput{meta: meta}, err
	}

	snapshot, err := loadMergeSnapshot(preflightCtx, client, target, record, expectedHead, &meta, budget)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	meta.Count = snapshot.Checks
	if preMerged {
		if err := validateMergedPostcondition(record, target, project.ID, iid, expectedURL, expectedHead, &snapshot); err != nil {
			return commandOutput{meta: meta}, err
		}
		return mergeSuccess(record, snapshot, authority, "already_merged", meta), nil
	}

	// The second MR read is unconditional and immediately adjacent to the only
	// possible mutation. GitLab's sha field remains the final provider guard.
	record, err = loadMergeMR(preflightCtx, client, target, iid, &meta, budget)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if err := validateMergeMRIdentity(record, target, project.ID, iid, expectedURL, expectedHead); err != nil {
		return commandOutput{meta: meta}, err
	}
	if record.State == "merged" {
		if err := validateMergedPostcondition(record, target, project.ID, iid, expectedURL, expectedHead, &snapshot); err != nil {
			return commandOutput{meta: meta}, err
		}
		return mergeSuccess(record, snapshot, authority, "already_merged", meta), nil
	}
	if err := validateOpenMergeMR(record, expectedHead); err != nil {
		return commandOutput{meta: meta}, err
	}
	if err := validateSnapshotPipeline(record.HeadPipeline, snapshot.Pipeline, target); err != nil {
		return commandOutput{meta: meta}, err
	}
	cancelPreflight()
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.Canceled) {
			return commandOutput{meta: meta}, uxv1.Wrap(uxv1.CodeCanceled, "merge request merge was canceled before mutation", ctxErr)
		}
		return commandOutput{meta: meta}, uxv1.Wrap(uxv1.CodeUpstream, "merge request merge timed out before mutation", ctxErr)
	}

	input, cleanup, err := writePrivateJSON(mergeInput{
		SHA: expectedHead, Squash: true, ShouldRemoveSourceBranch: false, AutoMerge: false,
	})
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	defer cleanup()

	mutationCtx, cancelMutation := context.WithTimeout(ctx, limits.MergeMutationOperation)
	response, writeErr := client.Do(mutationCtx, glab.Request{
		Operation: glab.OpMRMerge, Host: target.Host, Repo: target.Repo, IID: iid, InputFile: input,
	})
	cancelMutation()
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	if budgetErr := budget.add(response.Body); budgetErr != nil && writeErr == nil {
		writeErr = budgetErr
	}
	if writeErr == nil {
		var merged upstreamMR
		if decodeErr := decodeStrict(response.Body, &merged); decodeErr != nil {
			writeErr = decodeErr
		} else if validateErr := validateMergedPostcondition(merged, target, project.ID, iid, expectedURL, expectedHead, &snapshot); validateErr != nil {
			writeErr = validateErr
		} else {
			return mergeSuccess(merged, snapshot, authority, "merged", meta), nil
		}
	}

	return reconcileMRMerge(ctx, client, target, project.ID, iid, expectedURL, expectedHead, authority, snapshot, meta, budget, writeErr)
}

func loadMergeProject(ctx context.Context, client delegateClient, target Target, meta *uxv1.Meta, budget *mergeReadBudget) (mergeProject, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpMergeProject, Host: target.Host, Repo: target.Repo})
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	if err != nil {
		return mergeProject{}, err
	}
	if err := budget.add(response.Body); err != nil {
		return mergeProject{}, err
	}
	var project mergeProject
	if err := decodeStrict(response.Body, &project); err != nil {
		return mergeProject{}, err
	}
	if project.ID < 1 || project.PathWithNamespace != target.Repo || project.WebURL != canonicalProjectURL(target.Host, target.Repo) {
		return mergeProject{}, uxv1.NewError(uxv1.CodeSafety, "official glab returned a different merge project identity")
	}
	if !project.OnlyAllowMergeIfPipelineSucceeds || !project.OnlyAllowMergeIfAllDiscussionsAreResolved {
		return mergeProject{}, uxv1.NewError(uxv1.CodeSafety, "project must enforce successful pipelines and resolved discussions before merge")
	}
	return project, nil
}

func loadMergeMR(ctx context.Context, client delegateClient, target Target, iid int64, meta *uxv1.Meta, budget *mergeReadBudget) (upstreamMR, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpMergeMRView, Host: target.Host, Repo: target.Repo, IID: iid})
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	if err != nil {
		return upstreamMR{}, err
	}
	if err := budget.add(response.Body); err != nil {
		return upstreamMR{}, err
	}
	var record upstreamMR
	if err := decodeStrict(response.Body, &record); err != nil {
		return upstreamMR{}, err
	}
	return record, nil
}

func validateMergeMRIdentity(record upstreamMR, target Target, projectID, iid int64, expectedURL, expectedHead string) error {
	if record.ID < 1 || record.IID != iid || record.SourceProjectID != projectID || record.TargetProjectID != projectID {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned a different merge request identity")
	}
	if record.WebURL != expectedURL || record.WebURL != canonicalMRURL(target.Host, target.Repo, iid) {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned a different merge request URL")
	}
	if record.SHA != expectedHead || !validMergeSHA(record.SHA) {
		return uxv1.NewError(uxv1.CodeConflict, "merge request source head does not match --expected-head")
	}
	if err := safeurl.ValidateBranch(record.SourceBranch); err != nil {
		return uxv1.Wrap(uxv1.CodeSafety, "official glab returned an invalid source branch", err)
	}
	if err := safeurl.ValidateBranch(record.TargetBranch); err != nil {
		return uxv1.Wrap(uxv1.CodeSafety, "official glab returned an invalid target branch", err)
	}
	if record.SourceBranch == record.TargetBranch {
		return uxv1.NewError(uxv1.CodeSafety, "merge request source and target branches must differ")
	}
	if record.State != "opened" && record.State != "merged" {
		return uxv1.NewError(uxv1.CodeConflict, "merge request is neither open nor merged")
	}
	return nil
}

func validateOpenMergeMR(record upstreamMR, expectedHead string) error {
	if record.State != "opened" {
		return uxv1.NewError(uxv1.CodeConflict, "merge request is not open")
	}
	if record.SHA != expectedHead {
		return uxv1.NewError(uxv1.CodeConflict, "merge request source head changed before merge")
	}
	if record.Draft {
		return uxv1.NewError(uxv1.CodeConflict, "draft merge request cannot be merged")
	}
	if record.HasConflicts || gitlab.NormalizeMergeStatus(firstNonempty(record.DetailedMergeStatus, record.MergeStatus), record.HasConflicts) != "can_be_merged" {
		return uxv1.NewError(uxv1.CodeConflict, "merge request is not currently mergeable")
	}
	if !record.BlockingDiscussionsResolved {
		return uxv1.NewError(uxv1.CodeConflict, "merge request has unresolved blocking discussions")
	}
	if record.MergeWhenPipelineSucceeds {
		return uxv1.NewError(uxv1.CodeConflict, "merge request already has auto-merge enabled")
	}
	if record.ShouldRemoveSourceBranch {
		return uxv1.NewError(uxv1.CodeSafety, "source-branch removal is outside the guarded merge contract")
	}
	return nil
}

func loadMergeSnapshot(ctx context.Context, client delegateClient, target Target, record upstreamMR, expectedHead string, meta *uxv1.Meta, budget *mergeReadBudget) (mergeSnapshot, error) {
	pipeline, err := mergePipeline(record.HeadPipeline, target, expectedHead)
	if err != nil {
		return mergeSnapshot{}, err
	}
	snapshot := mergeSnapshot{Pipeline: pipeline}
	seen := make(map[string]bool)
	for _, group := range []struct {
		operation glab.Operation
		kind      string
	}{
		{operation: glab.OpMergeJobList, kind: "job"},
		{operation: glab.OpMergeBridgeList, kind: "bridge"},
	} {
		complete := false
		for page := 1; page <= limits.MaxPages; page++ {
			response, requestErr := client.Do(ctx, glab.Request{
				Operation: group.operation, Host: target.Host, Repo: target.Repo,
				PipelineID: pipeline.ID, Page: page, PerPage: mergePageSize,
			})
			if response.UpstreamVersion != "" {
				meta.UpstreamVersion = response.UpstreamVersion
			}
			if requestErr != nil {
				return mergeSnapshot{}, requestErr
			}
			if err := budget.add(response.Body); err != nil {
				return mergeSnapshot{}, err
			}
			if trimmed := bytes.TrimSpace(response.Body); len(trimmed) == 0 || trimmed[0] != '[' {
				return mergeSnapshot{}, uxv1.NewError(uxv1.CodeUpstream, "official glab returned malformed merge check pagination")
			}
			var items []upstreamJob
			if err := decodeStrict(response.Body, &items); err != nil {
				return mergeSnapshot{}, err
			}
			if len(items) > mergePageSize {
				return mergeSnapshot{}, uxv1.NewError(uxv1.CodeUpstream, "official glab returned too many merge checks in one page")
			}
			for _, item := range items {
				key := group.kind + ":" + strconv.FormatInt(item.ID, 10)
				if seen[key] {
					return mergeSnapshot{}, uxv1.NewError(uxv1.CodeSafety, "official glab returned duplicate merge checks")
				}
				seen[key] = true
				if err := validateGreenMergeCheck(item, group.kind, target, pipeline); err != nil {
					return mergeSnapshot{}, err
				}
				snapshot.Checks++
				if snapshot.Checks > limits.MaxJobs {
					return mergeSnapshot{}, uxv1.NewError(uxv1.CodeUpstream, "merge check count exceeded the operation limit")
				}
			}
			if len(items) < mergePageSize {
				complete = true
				break
			}
		}
		if !complete {
			return mergeSnapshot{}, uxv1.NewError(uxv1.CodeSafety, "merge check pagination was incomplete")
		}
	}
	return snapshot, nil
}

func mergePipeline(value *upstreamPipeline, target Target, expectedHead string) (mergePipelineResult, error) {
	if value == nil || value.ID < 1 || value.SHA != expectedHead || value.Status != "success" {
		return mergePipelineResult{}, uxv1.NewError(uxv1.CodeConflict, "merge request does not have an exact successful head pipeline")
	}
	expectedURL := canonicalProjectURL(target.Host, target.Repo) + "/-/pipelines/" + strconv.FormatInt(value.ID, 10)
	if value.WebURL != expectedURL {
		return mergePipelineResult{}, uxv1.NewError(uxv1.CodeSafety, "official glab returned a different head pipeline URL")
	}
	return mergePipelineResult{ID: value.ID, SHA: value.SHA, Status: "success"}, nil
}

func validateSnapshotPipeline(value *upstreamPipeline, expected mergePipelineResult, target Target) error {
	actual, err := mergePipeline(value, target, expected.SHA)
	if err != nil {
		return err
	}
	if actual != expected {
		return uxv1.NewError(uxv1.CodeConflict, "merge request head pipeline changed before merge")
	}
	return nil
}

func validateGreenMergeCheck(item upstreamJob, kind string, target Target, pipeline mergePipelineResult) error {
	if item.ID < 1 || !validMergeText(item.Name, true) || !validMergeText(item.Stage, false) {
		return uxv1.NewError(uxv1.CodeUpstream, "official glab returned invalid "+kind+" metadata")
	}
	if item.Pipeline == nil || item.Pipeline.ID != pipeline.ID || item.Pipeline.SHA != pipeline.SHA || item.Pipeline.Status != "success" {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned a "+kind+" outside the selected head pipeline")
	}
	expectedURL := canonicalProjectURL(target.Host, target.Repo) + "/-/jobs/" + strconv.FormatInt(item.ID, 10)
	if item.WebURL != expectedURL {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned a different "+kind+" URL")
	}
	if !greenMergeStatus(item.Status, item.AllowFailure) {
		return uxv1.NewError(uxv1.CodeConflict, "merge request has a non-green "+kind)
	}
	if item.DownstreamPipeline != nil {
		downstream := item.DownstreamPipeline
		if downstream.ID < 1 || !validMergeSHA(downstream.SHA) || !greenMergeStatus(downstream.Status, item.AllowFailure) {
			return uxv1.NewError(uxv1.CodeConflict, "merge request has a non-green downstream pipeline")
		}
	}
	return nil
}

func greenMergeStatus(status string, allowFailure bool) bool {
	switch status {
	case "success", "skipped":
		return true
	case "failed", "manual":
		return allowFailure
	default:
		return false
	}
}

func validMergeText(value string, required bool) bool {
	if required && value == "" || len(value) > 1024 || !utf8.ValidString(value) {
		return false
	}
	return !strings.ContainsFunc(value, func(r rune) bool {
		return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
	})
}

func validateMergedPostcondition(record upstreamMR, target Target, projectID, iid int64, expectedURL, expectedHead string, snapshot *mergeSnapshot) error {
	if err := validateMergeMRIdentity(record, target, projectID, iid, expectedURL, expectedHead); err != nil {
		return err
	}
	if record.State != "merged" || record.Draft || record.HasConflicts || !record.Squash || !record.BlockingDiscussionsResolved {
		return uxv1.NewError(uxv1.CodeConflict, "merged merge request does not satisfy the guarded squash postcondition")
	}
	if record.MergedAt == nil || record.MergedAt.IsZero() || !validMergeActor(record.MergeUser, record.MergedBy) {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned incomplete merge attribution")
	}
	if !validMergeSHA(record.SquashCommitSHA) {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned an invalid squash commit SHA")
	}
	if record.MergeCommitSHA != "" && !validMergeSHA(record.MergeCommitSHA) {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned an invalid merge commit SHA")
	}
	if record.MergeWhenPipelineSucceeds {
		return uxv1.NewError(uxv1.CodeConflict, "merge completed through auto-merge instead of the immediate contract")
	}
	if record.ShouldRemoveSourceBranch {
		return uxv1.NewError(uxv1.CodeSafety, "merged response indicates source-branch removal")
	}
	if snapshot != nil {
		if err := validateSnapshotPipeline(record.HeadPipeline, snapshot.Pipeline, target); err != nil {
			return err
		}
	}
	return nil
}

func validMergeActor(primary, legacy *upstreamUser) bool {
	valid := func(user *upstreamUser) bool {
		return user != nil && validMergeText(user.Username, true)
	}
	if !valid(primary) && !valid(legacy) {
		return false
	}
	return primary == nil || legacy == nil || primary.Username == legacy.Username
}

func mergeSuccess(record upstreamMR, snapshot mergeSnapshot, authority, action string, meta uxv1.Meta) commandOutput {
	resultCommit := record.MergeCommitSHA
	if resultCommit == "" {
		resultCommit = record.SquashCommitSHA
	}
	return commandOutput{data: mergeOutput{Merge: mergeResult{
		Action: action, IID: record.IID, WebURL: record.WebURL,
		SourceBranch: record.SourceBranch, TargetBranch: record.TargetBranch,
		SourceHeadSHA: record.SHA, MergeCommitSHA: record.MergeCommitSHA,
		SquashCommitSHA: record.SquashCommitSHA, ResultCommitSHA: resultCommit,
		Pipeline: snapshot.Pipeline, Authority: authority,
	}}, meta: meta}
}

func reconcileMRMerge(parent context.Context, client delegateClient, target Target, projectID, iid int64, expectedURL, expectedHead, authority string, snapshot mergeSnapshot, meta uxv1.Meta, budget *mergeReadBudget, writeErr error) (commandOutput, error) {
	if parent.Err() != nil {
		return commandOutput{meta: meta}, ambiguousMergeError(writeErr)
	}
	ctx, cancel := context.WithTimeout(parent, limits.MergeReconcileOperation)
	defer cancel()
	record, readErr := loadMergeMR(ctx, client, target, iid, &meta, budget)
	if readErr != nil {
		return commandOutput{meta: meta}, ambiguousMergeError(errors.Join(writeErr, readErr))
	}
	if identityErr := validateMergeMRIdentity(record, target, projectID, iid, expectedURL, expectedHead); identityErr != nil {
		return commandOutput{meta: meta}, ambiguousMergeError(errors.Join(writeErr, identityErr))
	}
	if record.State == "merged" {
		if postErr := validateMergedPostcondition(record, target, projectID, iid, expectedURL, expectedHead, &snapshot); postErr != nil {
			return commandOutput{meta: meta}, ambiguousMergeError(errors.Join(writeErr, postErr))
		}
		return mergeSuccess(record, snapshot, authority, "reconciled_merged", meta), nil
	}
	if record.State == "opened" {
		if classified := uxv1.AsError(writeErr); classified != nil {
			if rejection, ok := boundedMergeRejection(classified.StatusCode); ok {
				return commandOutput{meta: meta}, rejection
			}
		}
	}
	return commandOutput{meta: meta}, ambiguousMergeError(writeErr)
}

func boundedMergeRejection(status int) (*uxv1.Error, bool) {
	if status == 405 || status == 406 {
		return &uxv1.Error{
			Code: uxv1.CodeConflict, Message: fmt.Sprintf("GitLab refused to merge the merge request (HTTP %d)", status), StatusCode: status,
		}, true
	}
	return uxv1.NewHTTPRejection(status)
}

func ambiguousMergeError(cause error) error {
	return uxv1.Wrap(uxv1.CodeAmbiguousMerge, "merge outcome is ambiguous; reconciliation did not prove the exact guarded squash", cause)
}

func firstNonempty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
