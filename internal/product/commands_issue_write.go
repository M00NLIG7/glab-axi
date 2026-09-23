package product

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
	"gl-axi/internal/privatefile"
	"gl-axi/internal/productnative"
)

const (
	issueWritePreflight = 10 * time.Second
	issueWriteAttempt   = 20 * time.Second
)

type issueWriteIdentity struct {
	Host            string `json:"host"`
	ProjectID       int64  `json:"project_id"`
	ProjectFullPath string `json:"project_full_path"`
	ProjectWebURL   string `json:"project_web_url"`
	IssueID         int64  `json:"issue_id,omitempty"`
	IID             int64  `json:"iid,omitempty"`
	WebURL          string `json:"web_url,omitempty"`
}

type issueWriteReceipt struct {
	Operation          string             `json:"operation"`
	Outcome            string             `json:"outcome"`
	Identity           issueWriteIdentity `json:"identity"`
	MutationAttempts   int                `json:"mutation_attempts"`
	MutationResponse   string             `json:"mutation_response"`
	Postcondition      string             `json:"postcondition"`
	AtomicPrecondition bool               `json:"atomic_precondition"`
	RetrySafe          bool               `json:"retry_safe"`
	RequestedSHA256    string             `json:"requested_sha256"`
	ExpectedState      string             `json:"expected_state,omitempty"`
	RequestedState     string             `json:"requested_state,omitempty"`
	ObservedState      string             `json:"observed_state,omitempty"`
	NoteID             int64              `json:"note_id,omitempty"`
}

type issueWriteOutput struct {
	Write issueWriteReceipt `json:"write"`
}

type issueCreatePayload struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	IssueType   string `json:"issue_type"`
}
type issueNotePayload struct {
	Body string `json:"body"`
}
type issueStatePayload struct {
	StateEvent string `json:"state_event"`
}

type issueWriteRecord struct {
	ID          int64      `json:"id"`
	IID         int64      `json:"iid"`
	ProjectID   int64      `json:"project_id"`
	WebURL      string     `json:"web_url"`
	State       string     `json:"state"`
	UpdatedAt   *time.Time `json:"updated_at"`
	Title       *string    `json:"title"`
	Description *string    `json:"description"`
	IssueType   string     `json:"issue_type"`
}

func executeIssueWrite(ctx context.Context, target Target, parsed Parsed, deps Dependencies, meta uxv1.Meta) (commandOutput, error) {
	action := parsed.Definition.Path[1]
	if action == "note" {
		action = "comment"
	}
	// Load and validate all private content before even version/credential work.
	payload, err := loadIssueWritePayload(parsed, action)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeInternal, "cannot encode issue request")
	}
	// Resolve one native identity only after all private content is validated.
	client, err := openNative(ctx, parsed, deps)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	defer client.Close()
	host := client.Host()
	meta.Backend, meta.Host, meta.UpstreamVersion = "native", host.Name, ""
	projectURL := host.Authority.ExpectedProjectURL(target.Repo)
	expectedURL := projectURL
	if action != "create" {
		iid, _ := issueEditIID(parsed)
		expectedURL += "/-/issues/" + strconv.FormatInt(iid, 10)
	}
	if parsed.Values["--expected-url"] != expectedURL {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeSafety, "--expected-url does not match the configured native web authority and selected target")
	}
	sum := sha256.Sum256(encoded)
	projectID, _ := issueWriteID(parsed.Values["--expected-project-id"], "--expected-project-id")
	issueID, _ := issueWriteID(parsed.Values["--expected-issue-id"], "--expected-issue-id")
	iid, _ := issueEditIID(parsed)
	receipt := issueWriteReceipt{Operation: action, Outcome: "ambiguous", Identity: issueWriteIdentity{
		Host: host.Name, ProjectID: projectID, ProjectFullPath: target.Repo, ProjectWebURL: projectURL,
	}, MutationResponse: "not_attempted", Postcondition: "not_checked", RequestedSHA256: hex.EncodeToString(sum[:])}
	if action != "create" {
		receipt.Identity.IssueID = issueID
		receipt.Identity.IID = iid
		receipt.Identity.WebURL = parsed.Values["--expected-url"]
	}
	stateAction := action == "close" || action == "reopen"
	if stateAction {
		receipt.ExpectedState = parsed.Values["--expected-state"]
		receipt.RequestedState = "closed"
		if action == "reopen" {
			receipt.RequestedState = "opened"
		}
	}
	// Fixed reads only. There is no list/search or pagination to turn somebody
	// else's issue/comment into evidence of this invocation's write.
	issuePath := "projects/" + strconv.FormatInt(projectID, 10) + "/issues/" + strconv.FormatInt(iid, 10)
	read := func(readCtx context.Context, path string) ([]byte, error) {
		response, err := client.Do(readCtx, productnative.Request{Method: http.MethodGet, Path: path, MaxBytes: limits.MaxJSONPageBytes})
		if err != nil {
			return nil, err
		}
		return response.Body, nil
	}
	readProject := func(readCtx context.Context) error {
		body, err := read(readCtx, "projects/"+url.PathEscape(target.Repo))
		if err != nil {
			return err
		}
		if err := validateUniqueJSON(body, '{', "issue-write project"); err != nil {
			return uxv1.NewError(uxv1.CodeUpstream, "GitLab returned malformed issue-write project JSON")
		}
		var p issueEditProject
		if err := decodeStrict(body, &p); err != nil {
			return uxv1.NewError(uxv1.CodeUpstream, "GitLab returned malformed issue-write project JSON")
		}
		if p.ID != projectID || p.PathWithNamespace != target.Repo || p.WebURL != receipt.Identity.ProjectWebURL {
			return uxv1.NewError(uxv1.CodeSafety, "GitLab returned a different issue-write project identity")
		}
		return nil
	}
	readIssue := func(readCtx context.Context) (issueWriteRecord, error) {
		body, err := read(readCtx, issuePath)
		if err != nil {
			return issueWriteRecord{}, err
		}
		return decodeIssueWriteRecord(body, receipt.Identity)
	}
	preflight, cancel := context.WithTimeout(ctx, issueWritePreflight)
	defer cancel()
	if err := readProject(preflight); err != nil {
		return commandOutput{meta: meta}, err
	}
	if action != "create" {
		before, err := readIssue(preflight)
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		if stateAction && before.State != receipt.ExpectedState {
			return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "issue state does not match --expected-state")
		}
		adjacent, err := readIssue(preflight)
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		if before.State != adjacent.State || !before.UpdatedAt.Equal(*adjacent.UpdatedAt) {
			return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "issue changed during write preflight")
		}
		if stateAction {
			receipt.ObservedState = adjacent.State
		}
	}
	if err := readProject(preflight); err != nil {
		return commandOutput{meta: meta}, err
	}
	if err := preflight.Err(); err != nil {
		return commandOutput{meta: meta}, issueWriteContextError(err)
	}
	cancel()
	if stateAction {
		receipt.Postcondition = "preflight"
		if receipt.ObservedState == receipt.RequestedState {
			receipt.Outcome = "unchanged"
			return commandOutput{data: issueWriteOutput{receipt}, meta: meta}, nil
		}
		receipt.Outcome = "refused"
		return issueWriteFailure(receipt, meta, uxv1.NewError(uxv1.CodeUnsupported, "issue close/reopen mutations are temporarily unavailable because GitLab cannot prevent collateral content edits"))
	}
	if err := ctx.Err(); err != nil {
		return commandOutput{meta: meta}, issueWriteContextError(err)
	}
	path := "projects/" + strconv.FormatInt(projectID, 10) + "/issues"
	if action == "comment" {
		path = issuePath + "/notes"
	}
	writeCtx, cancelWrite := context.WithTimeout(ctx, issueWriteAttempt)
	response, writeErr := client.Do(writeCtx, productnative.Request{
		Method: http.MethodPost, Path: path, Body: encoded, MaxBytes: limits.MaxJSONPageBytes,
		Headers: http.Header{"Content-Type": {"application/json"}},
	})
	cancelWrite()
	// An attempted transfer is not a claim that the provider received it.
	// Unknown transport/response outcomes stay ambiguous, without replay.
	receipt.MutationAttempts = 1
	receipt.MutationResponse = "unconfirmed"
	if writeErr != nil {
		if rejection, ok := uxv1.NewHTTPRejection(uxv1.AsError(writeErr).StatusCode); ok {
			receipt.Outcome = "rejected"
			receipt.MutationResponse = "rejected"
			rejection.Retryable = false // invocation retries must be explicit, not automatic
			return issueWriteFailure(receipt, meta, rejection)
		}
	} else {
		if action == "comment" {
			receipt.NoteID, writeErr = validateCreatedIssueNote(response.Body, receipt.Identity, payload.(issueNotePayload).Body)
		} else {
			var returned issueWriteRecord
			returned, writeErr = decodeIssueWriteRecord(response.Body, receipt.Identity)
			if writeErr == nil {
				wanted := payload.(issueCreatePayload)
				if returned.Title == nil || *returned.Title != wanted.Title || returned.Description == nil || *returned.Description != wanted.Description || returned.State != "opened" || returned.IssueType != "issue" {
					writeErr = uxv1.NewError(uxv1.CodeConflict, "created issue does not prove the requested content and type")
				} else {
					receipt.Identity.IssueID, receipt.Identity.IID, receipt.Identity.WebURL = returned.ID, returned.IID, returned.WebURL
				}
			}
		}
		if writeErr == nil {
			receipt.MutationResponse = "accepted"
			receipt.Postcondition = "response"
		}
	}
	if writeErr != nil {
		return issueWriteAmbiguous(receipt, meta, uxv1.CodeAmbiguousCreate)
	}
	receipt.Outcome = "created"
	if action == "comment" {
		receipt.Outcome = "commented"
	}
	return commandOutput{data: issueWriteOutput{receipt}, meta: meta}, nil
}

func loadIssueWritePayload(parsed Parsed, action string) (any, error) {
	switch action {
	case "create":
		title, err := privatefile.Read(parsed.Values["--title-file"], limits.MaxTitleBytes, true)
		if err != nil {
			return nil, err
		}
		title = strings.Trim(title, " \t\n\v\f\r\x00")
		if strings.TrimSpace(title) == "" || strings.ContainsAny(title, "\r\n") {
			return nil, uxv1.NewError(uxv1.CodeValidation, "issue title must be a nonempty single line")
		}
		body, err := privatefile.Read(parsed.Values["--description-file"], limits.MaxDescriptionBytes, false)
		if err != nil {
			return nil, err
		}
		if err := validateIssueWriteBody(body); err != nil {
			return nil, err
		}
		body = normalizeIssueWriteBody(body)
		if strings.TrimSpace(body) == "" {
			return nil, uxv1.NewError(uxv1.CodeUnsupported, "blank issue descriptions are temporarily unavailable because GitLab may execute default-template quick actions")
		}
		return issueCreatePayload{title, body, "issue"}, nil
	case "comment":
		body, err := privatefile.Read(parsed.Values["--body-file"], limits.MaxDescriptionBytes, false)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(body) == "" {
			return nil, uxv1.NewError(uxv1.CodeValidation, "issue note body must not be empty")
		}
		if err := validateIssueWriteBody(body); err != nil {
			return nil, err
		}
		return issueNotePayload{normalizeIssueWriteBody(body)}, nil
	default:
		return issueStatePayload{action}, nil
	}
}

func normalizeIssueWriteBody(body string) string {
	return strings.TrimRight(strings.ReplaceAll(body, "\r", ""), " \t\n\v\f\x00")
}

func decodeIssueWriteRecord(body []byte, identity issueWriteIdentity) (issueWriteRecord, error) {
	if err := validateUniqueJSON(body, '{', "issue-write"); err != nil {
		return issueWriteRecord{}, uxv1.NewError(uxv1.CodeUpstream, "GitLab returned malformed issue-write JSON")
	}
	var record issueWriteRecord
	if err := decodeStrict(body, &record); err != nil {
		return record, uxv1.NewError(uxv1.CodeUpstream, "GitLab returned malformed issue-write JSON")
	}
	if record.ID < 1 || record.IID < 1 || record.ProjectID != identity.ProjectID || (identity.IssueID > 0 && record.ID != identity.IssueID) || (identity.IID > 0 && record.IID != identity.IID) || record.WebURL != identity.ProjectWebURL+"/-/issues/"+strconv.FormatInt(record.IID, 10) {
		return record, uxv1.NewError(uxv1.CodeSafety, "GitLab returned a different issue-write identity")
	}
	if record.UpdatedAt == nil || record.UpdatedAt.IsZero() || (record.State != "opened" && record.State != "closed") {
		return record, uxv1.NewError(uxv1.CodeUpstream, "GitLab returned an incomplete issue-write document")
	}
	return record, nil
}

func validateCreatedIssueNote(body []byte, identity issueWriteIdentity, wanted string) (int64, error) {
	if err := validateUniqueJSON(body, '{', "issue-note"); err != nil {
		return 0, uxv1.NewError(uxv1.CodeUpstream, "GitLab returned malformed issue-note JSON")
	}
	var note struct {
		ID           int64  `json:"id"`
		ProjectID    int64  `json:"project_id"`
		NoteableID   int64  `json:"noteable_id"`
		NoteableIID  int64  `json:"noteable_iid"`
		NoteableType string `json:"noteable_type"`
		Body         string `json:"body"`
		System       *bool  `json:"system"`
		Internal     *bool  `json:"internal"`
	}
	if err := decodeStrict(body, &note); err != nil {
		return 0, uxv1.NewError(uxv1.CodeUpstream, "GitLab returned malformed issue-note JSON")
	}
	if note.ID < 1 || note.ProjectID != identity.ProjectID || note.NoteableID != identity.IssueID || note.NoteableIID != identity.IID || note.NoteableType != "Issue" || note.Body != wanted || note.System == nil || *note.System || note.Internal == nil || *note.Internal {
		return 0, uxv1.NewError(uxv1.CodeUpstream, "issue note response did not prove the exact requested note")
	}
	return note.ID, nil
}

func issueWriteFailure(receipt issueWriteReceipt, meta uxv1.Meta, err *uxv1.Error) (commandOutput, error) {
	err.Receipt = issueWriteOutput{receipt}
	return commandOutput{meta: meta}, err
}
func issueWriteAmbiguous(receipt issueWriteReceipt, meta uxv1.Meta, code uxv1.Code) (commandOutput, error) {
	receipt.Outcome = "ambiguous"
	return issueWriteFailure(receipt, meta, uxv1.NewError(code, "issue write outcome is ambiguous; do not retry blindly or infer deduplication from another invocation"))
}
func issueWriteContextError(err error) error {
	if err == context.Canceled {
		return uxv1.NewError(uxv1.CodeCanceled, "issue write canceled before mutation")
	}
	return uxv1.NewError(uxv1.CodeUpstream, "issue write timed out before mutation")
}
