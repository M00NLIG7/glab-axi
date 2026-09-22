package product

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

type ensureProject struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}

type ensureResult struct {
	MR       MergeRequest        `json:"mr"`
	Action   string              `json:"action"`
	Creation *mrCreationMetadata `json:"creation,omitempty"`
}

func executeMREnsure(ctx context.Context, client mrOperationClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	source, targetBranch := parsed.Values["--source"], parsed.Values["--target"]
	title, description, err := readMREnsureContent(parsed)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	selection, err := parseMRCreationMetadata(parsed)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	project, version, err := loadEnsureProject(ctx, client, target)
	meta.UpstreamVersion = version
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	matches, version, err := loadEnsureMatches(ctx, client, target, project.ID, source, targetBranch)
	if version != "" {
		meta.UpstreamVersion = version
	}
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if len(matches) > 1 {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "multiple open merge requests match the exact source and target branches")
	}
	if len(matches) == 1 {
		return ensureExisting(ctx, client, target, project.ID, matches[0], source, targetBranch, title, description, meta, selection)
	}

	// Recheck immediately before POST. A competing creator becomes an update or
	// replay rather than a duplicate write.
	matches, version, err = loadEnsureMatches(ctx, client, target, project.ID, source, targetBranch)
	if version != "" {
		meta.UpstreamVersion = version
	}
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if len(matches) > 1 {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "multiple open merge requests match the exact source and target branches")
	}
	if len(matches) == 1 {
		return ensureExisting(ctx, client, target, project.ID, matches[0], source, targetBranch, title, description, meta, selection)
	}

	payload := map[string]any{"source_branch": source, "target_branch": targetBranch, "title": title, "description": description}
	selection.addInput(payload)
	input, cleanup, err := writePrivateJSON(payload)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	defer cleanup()
	response, writeErr := client.Do(ctx, glab.Request{Operation: glab.OpEnsureCreate, Host: target.Host, Repo: target.Repo, InputFile: input})
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	if writeErr == nil {
		created, record, validateErr := decodeAndValidateEnsureMR(response.Body, target, project.ID, source, targetBranch)
		if validateErr == nil && created.Title == title && created.Description == description && selection.matches(record) {
			return commandOutput{data: ensureResult{MR: created, Action: "created", Creation: selection}, meta: meta}, nil
		}
		if validateErr != nil {
			writeErr = validateErr
		} else {
			writeErr = errors.New("created merge request did not preserve requested content")
		}
	}
	return reconcileEnsure(ctx, client, target, project.ID, source, targetBranch, title, description, meta, uxv1.CodeAmbiguousCreate, "reconciled_create", writeErr, selection)
}

func ensureExisting(ctx context.Context, client mrOperationClient, target Target, projectID int64, record upstreamMR, source, targetBranch, title, description string, meta uxv1.Meta, selection *mrCreationMetadata) (commandOutput, error) {
	if !selection.matches(record) {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "existing merge request metadata differs; creation selection never replaces existing metadata")
	}
	normalized, _, err := normalizeMR(record, target.Host, target.Repo, true)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if record.Title == title && record.Description == description {
		return commandOutput{data: ensureResult{MR: normalized, Action: "unchanged", Creation: selection}, meta: meta}, nil
	}
	if selection != nil {
		// Selection is creation-only, including when another creator wins the
		// branch-pair race. Do not combine it with an unguarded metadata update.
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "existing merge request content differs; creation metadata selection requires a complete no-op match")
	}
	input, cleanup, err := writePrivateJSON(map[string]any{"title": title, "description": description})
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	defer cleanup()
	response, writeErr := client.Do(ctx, glab.Request{Operation: glab.OpEnsureUpdate, Host: target.Host, Repo: target.Repo, IID: record.IID, InputFile: input})
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	if writeErr == nil {
		updated, _, validateErr := decodeAndValidateEnsureMR(response.Body, target, projectID, source, targetBranch)
		if validateErr == nil && updated.IID == record.IID && updated.Title == title && updated.Description == description {
			return commandOutput{data: ensureResult{MR: updated, Action: "updated"}, meta: meta}, nil
		}
		if validateErr != nil {
			writeErr = validateErr
		} else {
			writeErr = errors.New("updated merge request did not preserve requested content")
		}
	}
	return reconcileEnsureUpdate(ctx, client, target, projectID, record, source, targetBranch, title, description, meta, writeErr)
}

func reconcileEnsureUpdate(ctx context.Context, client mrOperationClient, target Target, projectID int64, expected upstreamMR, source, targetBranch, title, description string, meta uxv1.Meta, cause error) (commandOutput, error) {
	reconcileCtx, cancel := context.WithTimeout(ctx, limits.EnsureReconcileOperation)
	defer cancel()
	response, readErr := client.Do(reconcileCtx, glab.Request{Operation: glab.OpMRView, Host: target.Host, Repo: target.Repo, IID: expected.IID})
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	if readErr == nil {
		canonical, record, validateErr := decodeAndValidateEnsureMR(response.Body, target, projectID, source, targetBranch)
		if validateErr == nil {
			validateErr = validateEnsureUpdateIdentity(record, expected)
		}
		if validateErr == nil && canonical.Title == title && canonical.Description == description {
			return commandOutput{data: ensureResult{MR: canonical, Action: "reconciled_update"}, meta: meta}, nil
		}
		if validateErr != nil {
			readErr = validateErr
		} else {
			readErr = errors.New("canonical merge request does not have the requested updated content")
		}
	}
	message := "merge request write outcome is ambiguous; reconciliation did not prove the requested state"
	return commandOutput{meta: meta}, uxv1.Wrap(uxv1.CodeAmbiguousUpdate, message, errors.Join(cause, readErr))
}

func validateEnsureUpdateIdentity(actual, expected upstreamMR) error {
	if actual.ID != expected.ID || actual.IID != expected.IID || actual.WebURL != expected.WebURL || !validMergeSHA(expected.SHA) || actual.SHA != expected.SHA {
		return uxv1.NewError(uxv1.CodeSafety, "canonical merge request identity changed during update reconciliation")
	}
	return nil
}

func reconcileEnsure(ctx context.Context, client mrOperationClient, target Target, projectID int64, source, targetBranch, title, description string, meta uxv1.Meta, code uxv1.Code, action string, cause error, selection *mrCreationMetadata) (commandOutput, error) {
	matches, version, err := loadEnsureMatches(ctx, client, target, projectID, source, targetBranch)
	if version != "" {
		meta.UpstreamVersion = version
	}
	if err == nil && len(matches) == 1 && matches[0].Title == title && matches[0].Description == description && selection.matches(matches[0]) {
		normalized, _, normalizeErr := normalizeMR(matches[0], target.Host, target.Repo, true)
		if normalizeErr == nil {
			return commandOutput{data: ensureResult{MR: normalized, Action: action, Creation: selection}, meta: meta}, nil
		}
		err = normalizeErr
	}
	if err == nil && len(matches) == 0 && code == uxv1.CodeAmbiguousCreate {
		if classified := uxv1.AsError(cause); classified != nil {
			if rejection, ok := uxv1.NewHTTPRejection(classified.StatusCode); ok {
				rejection.Cause = cause
				return commandOutput{meta: meta}, rejection
			}
		}
	}
	if err != nil {
		cause = errors.Join(cause, err)
	}
	message := "merge request write outcome is ambiguous; reconciliation did not prove the requested state"
	return commandOutput{meta: meta}, uxv1.Wrap(code, message, cause)
}

func loadEnsureProject(ctx context.Context, client mrOperationClient, target Target) (ensureProject, string, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpEnsureProject, Host: target.Host, Repo: target.Repo})
	if err != nil {
		return ensureProject{}, response.UpstreamVersion, err
	}
	var project ensureProject
	if err := decodeStrict(response.Body, &project); err != nil {
		return ensureProject{}, response.UpstreamVersion, err
	}
	if project.ID < 1 || project.PathWithNamespace != target.Repo || target.webBase != "" && project.WebURL != mrTargetProjectURL(target) {
		return ensureProject{}, response.UpstreamVersion, uxv1.NewError(uxv1.CodeSafety, "official glab returned a different repository identity")
	}
	if _, err := authorityRepoURL(project.WebURL, target.Host, target.Repo, true); err != nil {
		return ensureProject{}, response.UpstreamVersion, err
	}
	return project, response.UpstreamVersion, nil
}

func loadEnsureMatches(ctx context.Context, client mrOperationClient, target Target, projectID int64, source, targetBranch string) ([]upstreamMR, string, error) {
	matches := make([]upstreamMR, 0, 1)
	version := ""
	for page := 1; page <= limits.MaxPages; page++ {
		response, err := client.Do(ctx, glab.Request{Operation: glab.OpEnsureList, Host: target.Host, Repo: target.Repo, Source: source, Target: targetBranch, Page: page, PerPage: 100})
		if response.UpstreamVersion != "" {
			version = response.UpstreamVersion
		}
		if err != nil {
			return nil, version, err
		}
		if err := validateUniqueJSON(response.Body, '[', "matching merge request list"); err != nil {
			return nil, version, err
		}
		var batch []upstreamMR
		if err := decodeStrict(response.Body, &batch); err != nil {
			return nil, version, err
		}
		if len(batch) > 100 {
			return nil, version, uxv1.NewError(uxv1.CodeUpstream, "official glab returned too many matching merge requests")
		}
		for _, record := range batch {
			if err := validateEnsureRecord(record, target, projectID, source, targetBranch); err != nil {
				return nil, version, err
			}
			matches = append(matches, record)
			if len(matches) > 1 {
				return matches, version, nil
			}
		}
		if len(batch) < 100 {
			return matches, version, nil
		}
	}
	return nil, version, uxv1.NewError(uxv1.CodeConflict, "matching merge request lookup exceeded the hard page limit")
}

func validateEnsureRecord(record upstreamMR, target Target, projectID int64, source, targetBranch string) error {
	if record.ID < 1 || record.IID < 1 || record.SourceProjectID != projectID || record.TargetProjectID != projectID || record.SourceBranch != source || record.TargetBranch != targetBranch || record.State != "opened" {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned an out-of-scope merge request")
	}
	if target.webBase != "" && record.WebURL != mrTargetURL(target, record.IID) {
		return uxv1.NewError(uxv1.CodeSafety, "official glab returned a different merge request URL")
	}
	if _, _, err := normalizeMR(record, target.Host, target.Repo, true); err != nil {
		return err
	}
	return nil
}

func decodeAndValidateEnsureMR(body []byte, target Target, projectID int64, source, targetBranch string) (MergeRequest, upstreamMR, error) {
	if err := validateUniqueJSON(body, '{', "merge request"); err != nil {
		return MergeRequest{}, upstreamMR{}, err
	}
	var record upstreamMR
	if err := decodeStrict(body, &record); err != nil {
		return MergeRequest{}, upstreamMR{}, err
	}
	if err := validateEnsureRecord(record, target, projectID, source, targetBranch); err != nil {
		return MergeRequest{}, upstreamMR{}, err
	}
	normalized, _, err := normalizeMR(record, target.Host, target.Repo, true)
	return normalized, record, err
}

func writePrivateJSON(value any) (string, func(), error) {
	dir, err := os.MkdirTemp("", "gl-axi-input-")
	if err != nil {
		return "", func() {}, uxv1.Wrap(uxv1.CodeUpstream, "cannot create private JSON input directory", err)
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	if err := os.Chmod(dir, 0o700); err != nil {
		cleanup()
		return "", func() {}, uxv1.Wrap(uxv1.CodeUpstream, "cannot secure private JSON input directory", err)
	}
	path := filepath.Join(dir, "request.json")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		cleanup()
		return "", func() {}, uxv1.Wrap(uxv1.CodeUpstream, "cannot create private JSON input", err)
	}
	encoder := json.NewEncoder(file)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		file.Close()
		cleanup()
		return "", func() {}, uxv1.Wrap(uxv1.CodeInternal, "cannot encode private JSON input", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		cleanup()
		return "", func() {}, uxv1.Wrap(uxv1.CodeUpstream, "cannot sync private JSON input", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", func() {}, uxv1.Wrap(uxv1.CodeUpstream, "cannot close private JSON input", err)
	}
	return path, cleanup, nil
}
