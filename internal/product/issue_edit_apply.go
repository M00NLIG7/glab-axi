package product

import (
	"context"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func issueEditLabelNames(labels []issueEditLabel) []string {
	names := make([]string, len(labels))
	for i, label := range labels {
		names[i] = label.Name
	}
	return names
}

func applyIssueEdit(ctx context.Context, client delegateClient, target Target, project issueEditProject, before upstreamIssue, expectedURL, expectedState string, expectedAt time.Time, plan issueEditPlan, add, remove []issueEditLabel, meta uxv1.Meta, budget *issueEditReadBudget) (commandOutput, error) {
	if err := validateIssueEditDescription(plan.desired.Description); err != nil {
		return commandOutput{meta: meta}, err
	}
	input, cleanup, err := writePrivateJSON(plan.payload)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	defer cleanup()
	if err := ctx.Err(); err != nil {
		return commandOutput{meta: meta}, issueEditValidationContextError(err)
	}

	// A bounded check/write race remains. This is exactly one mutation attempt,
	// not a compare-and-swap, and it must never be retried after any outcome.
	writeCtx, cancelWrite := context.WithTimeout(ctx, limits.IssueEditMutation)
	response, writeErr := client.Do(writeCtx, glab.Request{
		Operation: glab.OpIssueEditUpdate, Host: target.Host, Repo: target.Repo,
		ID: project.ID, IID: before.IID, InputFile: input,
	})
	cancelWrite()
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	responseVerified := false
	unsafeResponse := false
	if writeErr == nil {
		if err := budget.add(response.Body); err == nil {
			if issue, err := decodeIssueEditIssue(response.Body); err == nil {
				// An intelligible but wrong identity/poststate is not repaired by
				// an unrelated later read that happens to show the desired state.
				err = validateIssueEditPoststate(issue, target, project.ID, before, plan)
				responseVerified = err == nil
				unsafeResponse = err != nil
			}
		}
	}

	// Always verify the canonical issue, including after a nominally successful
	// response. Malformed/lost responses reconcile by observation, not replay.
	// Parent cancellation is retained; it never starts detached network work.
	readCtx, cancelRead := context.WithTimeout(ctx, limits.IssueEditReconcile)
	defer cancelRead()
	var canonical upstreamIssue
	readErr := readCtx.Err()
	if readErr == nil {
		canonical, readErr = loadIssueEditIssue(readCtx, client, target, before.IID, &meta, budget)
	}
	if readErr == nil {
		readErr = validateIssueEditPoststate(canonical, target, project.ID, before, plan)
	}
	if readErr == nil && len(add)+len(remove) > 0 {
		var catalog []issueEditLabel
		catalog, readErr = loadIssueEditLabels(readCtx, client, target, &meta, budget)
		if readErr == nil {
			var actualAdd, actualRemove []issueEditLabel
			actualAdd, readErr = resolveIssueEditLabels(catalog, issueEditLabelNames(add), "add")
			if readErr == nil {
				actualRemove, readErr = resolveIssueEditLabels(catalog, issueEditLabelNames(remove), "remove")
			}
			if readErr == nil && (!sameIssueEditLabelIdentities(add, actualAdd) || !sameIssueEditLabelIdentities(remove, actualRemove)) {
				readErr = uxv1.NewError(uxv1.CodeConflict, "requested label identity changed during issue edit")
			}
		}
	}
	if readErr == nil && readCtx.Err() == nil && !unsafeResponse {
		action := "reconciled_update"
		if responseVerified {
			action = "updated"
		}
		return issueEditReceipt(action, false, target, project, canonical, expectedURL, expectedState, expectedAt, plan, meta), nil
	}

	result := issueEditReceipt("ambiguous", false, target, project, before, expectedURL, expectedState, expectedAt, plan, meta)
	result.meta.Reason = "issue_edit_outcome_unknown"
	failure := uxv1.NewError(uxv1.CodeAmbiguousUpdate, "issue edit outcome is ambiguous; bounded verification did not prove the requested state and label identities; inspect the issue before any new edit")
	failure.Receipt = result.data
	return result, failure
}

func validateIssueEditPoststate(actual upstreamIssue, target Target, projectID int64, before upstreamIssue, plan issueEditPlan) error {
	if err := validateIssueEditIdentity(actual, target, projectID, before.IID, before.ID, before.WebURL, before.State); err != nil {
		return err
	}
	if actual.UpdatedAt.Before(*before.UpdatedAt) {
		return uxv1.NewError(uxv1.CodeConflict, "issue edit returned an older revision")
	}
	left, leftErr := canonicalIssueEditLabels(actual.Labels)
	right, rightErr := canonicalIssueEditLabels(plan.desired.Labels)
	if leftErr != nil || rightErr != nil || !equalStrings(left, right) || actual.Title != plan.desired.Title || actual.Description != plan.desired.Description {
		return uxv1.NewError(uxv1.CodeConflict, "issue edit postconditions or preserved fields changed")
	}
	return nil
}
