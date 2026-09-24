package product

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"io"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/output"
)

// All identity reads and pages share the existing operation byte ceiling.
// The command's context also bounds the entire sequence, not each child alone.
type collaborationReadBudget struct {
	delegateClient
	bytes int
}

func (c *collaborationReadBudget) Do(ctx context.Context, request glab.Request) (glab.Response, error) {
	if err := ctx.Err(); err != nil {
		return glab.Response{}, uxv1.Wrap(uxv1.CodeCanceled, "collaboration read canceled", err)
	}
	response, err := c.delegateClient.Do(ctx, request)
	if err != nil {
		return response, err
	}
	c.bytes += len(response.Body)
	if len(response.Body) > limits.MaxJSONPageBytes || c.bytes > limits.MaxOperationBytes {
		return glab.Response{}, uxv1.NewError(uxv1.CodeUpstream, "collaboration evidence exceeded the safety limit")
	}
	return response, nil
}

func collaborationOutput(data any, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	if err := output.WriteValue(io.Discard, parsed.Format, uxv1.Success(data, meta)); err != nil {
		meta.Complete, meta.Truncated, meta.Reason = false, true, "operation_limit"
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeUpstream, "normalized collaboration output exceeded the safety limit")
	}
	return commandOutput{data: data, meta: meta}, nil
}

type IssueDiscussionIdentity struct {
	ID        int64                       `json:"id"`
	IID       int64                       `json:"iid"`
	ProjectID int64                       `json:"project_id"`
	Project   MRDiscussionProjectIdentity `json:"project"`
	WebURL    string                      `json:"web_url"`
	UpdatedAt time.Time                   `json:"updated_at"`
}

type IssueDiscussion struct {
	ID              string           `json:"id"`
	ProjectID       int64            `json:"project_id"`
	IssueID         int64            `json:"issue_id"`
	IssueIID        int64            `json:"issue_iid"`
	IndividualNote  bool             `json:"individual_note"`
	Resolvable      bool             `json:"resolvable"`
	Resolved        bool             `json:"resolved"`
	ResolutionState string           `json:"resolution_state"`
	Notes           []DiscussionNote `json:"notes"`
}

func readIssueDiscussionIdentity(ctx context.Context, client delegateClient, target Target, project MRDiscussionProjectIdentity, iid int64) (IssueDiscussionIdentity, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpIssueEditView, Host: target.Host, Repo: target.Repo, IID: iid})
	if err != nil {
		return IssueDiscussionIdentity{}, err
	}
	if err := validateUniqueJSON(response.Body, '{', "issue identity"); err != nil {
		return IssueDiscussionIdentity{}, err
	}
	var source struct {
		ID        int64      `json:"id"`
		IID       int64      `json:"iid"`
		ProjectID int64      `json:"project_id"`
		WebURL    string     `json:"web_url"`
		UpdatedAt *time.Time `json:"updated_at"`
	}
	if err := decodeStrict(response.Body, &source); err != nil {
		return IssueDiscussionIdentity{}, err
	}
	if source.ID < 1 || source.IID < 1 || source.ProjectID < 1 || source.UpdatedAt == nil || source.UpdatedAt.IsZero() {
		return IssueDiscussionIdentity{}, malformed("issue identity")
	}
	if source.IID != iid || source.ProjectID != project.ID || source.WebURL != canonicalIssueURL(target.Host, target.Repo, iid) {
		return IssueDiscussionIdentity{}, uxv1.NewError(uxv1.CodeSafety, "official glab returned a different issue identity")
	}
	return IssueDiscussionIdentity{ID: source.ID, IID: iid, ProjectID: project.ID, Project: project, WebURL: source.WebURL, UpdatedAt: *source.UpdatedAt}, nil
}

func executeIssueDiscussions(ctx context.Context, delegate delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	client := &collaborationReadBudget{delegateClient: delegate}
	iid, err := positivePosition(parsed, "issue IID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	project, version, err := readMRDiscussionProject(ctx, client, target, glab.OpIssueEditProject, 0, target.Repo)
	meta.UpstreamVersion = version
	if err != nil {
		meta.Reason = mrDiscussionIdentityFailureReason(err)
		return commandOutput{meta: meta}, err
	}
	initial, err := readIssueDiscussionIdentity(ctx, client, target, project, iid)
	if err != nil {
		meta.Reason = mrDiscussionIdentityFailureReason(err)
		return commandOutput{meta: meta}, err
	}
	normalizer := &discussionPageNormalizer{noteableType: "Issue", mrID: initial.ID, iid: iid, projectID: project.ID, seenDiscussionIDs: map[string]bool{}, seenNoteIDs: map[int64]bool{}}
	threads, state, err := fetchList(ctx, client, glab.Request{Operation: glab.OpIssueDiscussions, Host: target.Host, Repo: target.Repo, IID: iid}, parsed.Limit, normalizer.normalize)
	meta = mergeMeta(meta, state)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	threads, recordCut, bodyCut, err := boundDiscussionOutput(threads)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if recordCut {
		meta.Truncated, meta.Reason = true, "nested_record_limit"
	} else if bodyCut && meta.Reason == "" {
		meta.Reason = "field_limit"
	}
	meta.Truncated = meta.Truncated || bodyCut
	meta.Complete = meta.Complete && !meta.Truncated
	final, err := readIssueDiscussionIdentity(ctx, client, target, project, iid)
	if err != nil {
		meta.Reason = "snapshot_recheck_failed"
		return commandOutput{meta: meta}, err
	}
	finalProject, version, err := readMRDiscussionProject(ctx, client, target, glab.OpIssueEditProject, 0, target.Repo)
	meta.UpstreamVersion = version
	if err != nil {
		meta.Reason = "snapshot_recheck_failed"
		return commandOutput{meta: meta}, err
	}
	if initial != final || project != finalProject {
		meta.Reason = "snapshot_changed"
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "issue identity changed while discussion evidence was collected")
	}
	discussions := make([]IssueDiscussion, 0, len(threads))
	for _, thread := range threads {
		discussions = append(discussions, IssueDiscussion{ID: thread.ID, ProjectID: project.ID, IssueID: initial.ID, IssueIID: iid, IndividualNote: thread.IndividualNote, Resolvable: thread.Resolvable, Resolved: thread.Resolved, ResolutionState: thread.ResolutionState, Notes: thread.Notes})
	}
	return collaborationOutput(map[string]any{"issue": initial, "discussions": discussions}, parsed, meta)
}

// Approval state is reported, never computed from approval counts or notes.
// Enterprise and Community editions need not assign the same meaning to true.
type MRApprovalSummary struct {
	Availability      string           `json:"availability"`
	Reason            string           `json:"reason,omitempty"`
	State             string           `json:"state"`
	Tier              string           `json:"tier"`
	ApprovalsRequired *int64           `json:"approvals_required,omitempty"`
	ApprovalsLeft     *int64           `json:"approvals_left,omitempty"`
	ApprovedBy        []DiscussionUser `json:"approved_by"`
}

type MRReviewerAssignments struct {
	fingerprint  [32]byte
	Availability string           `json:"availability"`
	Users        []DiscussionUser `json:"users"`
}

func normalizeAssignedReviewers(body []byte, limit int) (MRReviewerAssignments, bool, error) {
	var source struct {
		Reviewers *[]*upstreamDiscussionUser `json:"reviewers"`
	}
	if err := decodeStrict(body, &source); err != nil {
		return MRReviewerAssignments{}, false, err
	}
	out := MRReviewerAssignments{Availability: "unknown", Users: []DiscussionUser{}}
	if source.Reviewers == nil {
		return out, false, nil
	}
	users, cut, err := normalizeCollaborationUsers(*source.Reviewers, limit)
	encoded, _ := json.Marshal(*source.Reviewers)
	out.fingerprint = sha256.Sum256(encoded)
	out.Availability, out.Users = "available", users
	return out, cut, err
}

func normalizeCollaborationUsers(source []*upstreamDiscussionUser, limit int) ([]DiscussionUser, bool, error) {
	users := make([]DiscussionUser, 0, min(len(source), limit))
	seen := map[int64]bool{}
	cut := len(source) > limit
	for _, raw := range source {
		user, fieldCut, err := normalizeDiscussionUser(raw, "collaboration user")
		if err != nil {
			return nil, false, err
		}
		if seen[user.ID] {
			return nil, false, malformed("duplicate collaboration user")
		}
		seen[user.ID] = true
		cut = cut || fieldCut
		if len(users) < limit {
			users = append(users, user)
		}
	}
	return users, cut, nil
}

func normalizeMRApprovals(body []byte, identity mrDiscussionSnapshotIdentity, limit int) (MRApprovalSummary, bool, error) {
	if err := validateUniqueJSON(body, '{', "merge request approvals"); err != nil {
		return MRApprovalSummary{}, false, err
	}
	var source struct {
		ID                int64                `json:"id"`
		IID               int64                `json:"iid"`
		ProjectID         int64                `json:"project_id"`
		Approved          upstreamNullableBool `json:"approved"`
		ApprovalsRequired *int64               `json:"approvals_required"`
		ApprovalsLeft     *int64               `json:"approvals_left"`
		ApprovedBy        *[]struct {
			User *upstreamDiscussionUser `json:"user"`
		} `json:"approved_by"`
	}
	if err := decodeStrict(body, &source); err != nil {
		return MRApprovalSummary{}, false, err
	}
	if source.ID < 1 || source.IID < 1 || source.ProjectID < 1 || source.ApprovedBy == nil {
		return MRApprovalSummary{}, false, malformed("merge request approvals")
	}
	if source.ID != identity.ID || source.IID != identity.IID || source.ProjectID != identity.TargetProjectID {
		return MRApprovalSummary{}, false, uxv1.NewError(uxv1.CodeSafety, "official glab returned approvals for a different merge request")
	}
	if source.ApprovalsRequired != nil && *source.ApprovalsRequired < 0 || source.ApprovalsLeft != nil && *source.ApprovalsLeft < 0 {
		return MRApprovalSummary{}, false, malformed("approval counts")
	}
	state := "unknown"
	if source.Approved.Present && !source.Approved.Null {
		state = "not_approved"
		if source.Approved.Value {
			state = "approved"
		}
	}
	users := make([]*upstreamDiscussionUser, 0, len(*source.ApprovedBy))
	for _, approver := range *source.ApprovedBy {
		users = append(users, approver.User)
	}
	approvedBy, cut, err := normalizeCollaborationUsers(users, limit)
	return MRApprovalSummary{Availability: "available", State: state, Tier: "unknown", ApprovalsRequired: source.ApprovalsRequired, ApprovalsLeft: source.ApprovalsLeft, ApprovedBy: approvedBy}, cut, err
}

func executeMRApprovals(ctx context.Context, delegate delegateClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	client := &collaborationReadBudget{delegateClient: delegate}
	iid, err := positivePosition(parsed, "merge request IID")
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	targetProject, version, err := readMRDiscussionProject(ctx, client, target, glab.OpMRDiscussionsTargetProject, 0, target.Repo)
	meta.UpstreamVersion = version
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpMRView, Host: target.Host, Repo: target.Repo, IID: iid})
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	initial, err := normalizeMRDiscussionSnapshot(response.Body, target, targetProject.ID, iid)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	reviewers, reviewersCut, err := normalizeAssignedReviewers(response.Body, parsed.Limit)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	sourceProject := targetProject
	if initial.SourceProjectID != initial.TargetProjectID {
		sourceProject, _, err = readMRDiscussionProject(ctx, client, target, glab.OpMRDiscussionsSourceProject, initial.SourceProjectID, "")
		if err != nil {
			return commandOutput{meta: meta}, err
		}
	}
	response, err = client.Do(ctx, glab.Request{Operation: glab.OpMRApprovals, Host: target.Host, Repo: target.Repo, IID: iid})
	approvals := MRApprovalSummary{Availability: "unavailable", State: "unknown", Tier: "unknown", ApprovedBy: []DiscussionUser{}}
	approvalsCut := false
	if err != nil {
		switch uxv1.AsError(err).StatusCode {
		case 403:
			approvals.Reason = "access_denied"
		case 404:
			approvals.Reason = "not_found_or_unsupported"
		default:
			return commandOutput{meta: meta}, err
		}
	} else {
		approvals, approvalsCut, err = normalizeMRApprovals(response.Body, initial, parsed.Limit)
		if err != nil {
			return commandOutput{meta: meta}, err
		}
	}
	meta.Truncated = reviewersCut || approvalsCut
	// The envelope count remains the primary collection (approving users),
	// never the sum of independent arrays, which could exceed ux-v1's cap.
	meta.Count = len(approvals.ApprovedBy)
	if meta.Truncated {
		meta.Complete, meta.Reason = false, "user_or_field_limit"
	}
	if approvals.State == "unknown" || reviewers.Availability == "unknown" {
		meta.Complete, meta.Reason = false, "state_unknown"
	}
	if approvals.Availability == "unavailable" {
		meta.Complete, meta.Reason = false, "approvals_unavailable"
	}

	response, err = client.Do(ctx, glab.Request{Operation: glab.OpMRView, Host: target.Host, Repo: target.Repo, IID: iid})
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	final, err := normalizeMRDiscussionSnapshot(response.Body, target, targetProject.ID, iid)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	// Compare the complete, untrimmed assignment lists as well. A reviewer
	// change must not hide behind display truncation or an unchanged timestamp.
	finalReviewers, _, err := normalizeAssignedReviewers(response.Body, parsed.Limit)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if !sameMRDiscussionSnapshot(initial, final) || !sameReviewerAssignments(reviewers, finalReviewers) {
		meta.Reason = "snapshot_changed"
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "merge request identity or reviewers changed while approvals were collected")
	}
	finalTarget, version, err := readMRDiscussionProject(ctx, client, target, glab.OpMRDiscussionsTargetProject, 0, target.Repo)
	meta.UpstreamVersion = version
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if targetProject != finalTarget {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "target project changed while approvals were collected")
	}
	if initial.SourceProjectID != initial.TargetProjectID {
		finalSource, _, err := readMRDiscussionProject(ctx, client, target, glab.OpMRDiscussionsSourceProject, initial.SourceProjectID, "")
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		if sourceProject != finalSource {
			return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "source project changed while approvals were collected")
		}
	}
	return collaborationOutput(map[string]any{"mr": materializeMRDiscussionIdentity(initial, sourceProject, targetProject), "reviewers": reviewers, "approvals": approvals}, parsed, meta)
}

func sameReviewerAssignments(a, b MRReviewerAssignments) bool {
	if a.Availability != b.Availability || a.fingerprint != b.fingerprint || len(a.Users) != len(b.Users) {
		return false
	}
	for i := range a.Users {
		if a.Users[i] != b.Users[i] {
			return false
		}
	}
	return true
}
