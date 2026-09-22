package product

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/privatefile"
	"gl-axi/internal/safeurl"
)

// These are observational guards, not a provider compare-and-swap. GitLab's
// ordinary MR update and note endpoints accept no expected MR revision.
type mrWriteSnapshot struct {
	Identity mrDiscussionSnapshotIdentity
	State    string
}

type mrWriteReceipt struct {
	Action                   string `json:"action"`
	Outcome                  string `json:"outcome"`
	IID                      int64  `json:"iid"`
	WebURL                   string `json:"web_url"`
	SourceBranch             string `json:"source_branch"`
	TargetBranch             string `json:"target_branch"`
	HeadSHA                  string `json:"head_sha"`
	ExpectedState            string `json:"expected_state"`
	ObservedState            string `json:"observed_state,omitempty"`
	Attempts                 int    `json:"attempts"`
	ProviderRevisionEnforced bool   `json:"provider_revision_enforced"`
	NoteID                   int64  `json:"note_id,omitempty"`
	NoteURL                  string `json:"note_url,omitempty"`
}

func mrWriteFlags(note bool) []FlagDefinition {
	flags := []FlagDefinition{
		{Name: "--expected-url", Value: "URL", Description: "Exact canonical MR URL.", Required: true},
		{Name: "--expected-source", Value: "BRANCH", Description: "Exact source branch.", Required: true},
		{Name: "--expected-target", Value: "BRANCH", Description: "Exact target branch.", Required: true},
		{Name: "--expected-head", Value: "SHA", Description: "Exact lowercase source head SHA.", Required: true},
		{Name: "--expected-state", Value: "STATE", Description: "Observed state: opened or closed.", Required: true},
	}
	if note {
		flags = append(flags, FlagDefinition{Name: "--body-file", Value: "FILE", Description: "Absolute private UTF-8 note file, at most 128 KiB; no quick actions or emoji-only body.", Required: true})
	}
	return flags
}

func validateMRWriteParsed(parsed Parsed) error {
	iid, err := mergeIID(parsed)
	if err != nil {
		return err
	}
	if safeurl.ValidateHost(parsed.Values["--hostname"]) != nil || safeurl.ValidateProject(parsed.Values["--repo"]) != nil {
		return uxv1.NewError(uxv1.CodeValidation, "explicit valid host and repository are required")
	}
	rawURL := parsed.Values["--expected-url"]
	selectedURL, urlErr := url.Parse(rawURL)
	suffix := "/" + parsed.Values["--repo"] + "/-/merge_requests/" + strconv.FormatInt(iid, 10)
	if urlErr != nil || selectedURL.Scheme != "https" || selectedURL.Host == "" || selectedURL.User != nil || selectedURL.RawQuery != "" || selectedURL.Fragment != "" || !strings.HasSuffix(rawURL, suffix) || selectedURL.String() != rawURL {
		return uxv1.NewError(uxv1.CodeSafety, "--expected-url must be an exact HTTPS URL for the selected project and IID")
	}
	if _, err := safeurl.NewAuthority(selectedURL.Host, "https://"+selectedURL.Host+"/api/v4", strings.TrimSuffix(rawURL, suffix)); err != nil {
		return uxv1.NewError(uxv1.CodeSafety, "--expected-url contains an invalid web authority")
	}
	// Configured native web/API hosts may differ from the logical hostname.
	// executeNativeMR binds this syntactically exact URL before any request.
	if safeurl.ValidateBranch(parsed.Values["--expected-source"]) != nil || safeurl.ValidateBranch(parsed.Values["--expected-target"]) != nil || parsed.Values["--expected-source"] == parsed.Values["--expected-target"] {
		return uxv1.NewError(uxv1.CodeValidation, "distinct valid expected source and target branches are required")
	}
	if !validMergeSHA(parsed.Values["--expected-head"]) {
		return uxv1.NewError(uxv1.CodeValidation, "--expected-head must be a lowercase 40- or 64-hex SHA")
	}
	if state := parsed.Values["--expected-state"]; state != "opened" && state != "closed" {
		return uxv1.NewError(uxv1.CodeValidation, "--expected-state must be opened or closed")
	}
	if parsed.Values["--body-file"] != "" {
		_, err = readMRNoteBody(parsed.Values["--body-file"])
	}
	return err
}

var mrNoteShortcode = regexp.MustCompile(`:[A-Za-z0-9_+\-]+:`)

func readMRNoteBody(path string) (string, error) {
	body, err := privatefile.Read(path, limits.MaxDescriptionBytes, false)
	if err != nil {
		return "", err
	}
	// GitLab interprets quick actions even through the notes API. Fail closed
	// rather than trying to emulate Markdown's code/quote parser. Plain notes
	// must also not become the emoji-reaction special case of that endpoint.
	prose := mrNoteShortcode.ReplaceAllString(body, "")
	if !strings.ContainsFunc(prose, func(r rune) bool { return unicode.IsLetter(r) && !unicode.Is(unicode.Common, r) }) {
		return "", uxv1.NewError(uxv1.CodeValidation, "note must contain prose, not only an emoji reaction")
	}
	if err := validateMRContentActions(body); err != nil {
		return "", err
	}
	return body, nil
}

func validateMRContentActions(body string) error {
	if strings.ContainsFunc(body, func(r rune) bool { return unicode.Is(unicode.Cf, r) || unicode.IsControl(r) && r != '\n' && r != '\t' }) {
		return uxv1.NewError(uxv1.CodeValidation, "MR content contains unsupported control characters")
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "/") {
			return uxv1.NewError(uxv1.CodeSecurityBoundary, "GitLab quick actions are not permitted in MR content")
		}
	}
	return nil
}

func executeMRWrite(ctx context.Context, client mrOperationClient, target Target, parsed Parsed, meta uxv1.Meta) (commandOutput, error) {
	iid, _ := mergeIID(parsed)
	action := parsed.Definition.Path[1]
	isNote := action == "comment" || action == "note"
	var body string
	var err error
	if isNote {
		body, err = readMRNoteBody(parsed.Values["--body-file"])
		if err != nil {
			return commandOutput{meta: meta}, err
		}
	}
	budget := &mergeReadBudget{}
	preflight, cancelPreflight := context.WithTimeout(ctx, limits.MergePreflightOperation)
	defer cancelPreflight()
	projectResponse, err := client.Do(preflight, glab.Request{Operation: glab.OpMRDiscussionsTargetProject, Host: target.Host, Repo: target.Repo})
	meta.UpstreamVersion = projectResponse.UpstreamVersion
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if err = budget.add(projectResponse.Body); err != nil {
		return commandOutput{meta: meta}, err
	}
	project, err := normalizeMRDiscussionProject(projectResponse.Body, target.Host, 0, target.Repo)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if project.WebURL != mrTargetProjectURL(target) {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeSafety, "project URL is not the exact selected project")
	}
	load := func(readCtx context.Context) (mrWriteSnapshot, error) {
		response, readErr := client.Do(readCtx, glab.Request{Operation: glab.OpMRView, Host: target.Host, Repo: target.Repo, IID: iid})
		if response.UpstreamVersion != "" {
			meta.UpstreamVersion = response.UpstreamVersion
		}
		if readErr != nil {
			return mrWriteSnapshot{}, readErr
		}
		if err := budget.add(response.Body); err != nil {
			return mrWriteSnapshot{}, err
		}
		identity, err := normalizeMRDiscussionSnapshot(response.Body, target, project.ID, iid)
		if err != nil {
			return mrWriteSnapshot{}, err
		}
		if identity.SourceProjectID != project.ID || identity.WebURL != parsed.Values["--expected-url"] || identity.SourceBranch != parsed.Values["--expected-source"] || identity.TargetBranch != parsed.Values["--expected-target"] || identity.HeadSHA != parsed.Values["--expected-head"] {
			return mrWriteSnapshot{}, uxv1.NewError(uxv1.CodeConflict, "merge request does not match the expected same-project URL, branches, and head")
		}
		var state struct {
			State string `json:"state"`
		}
		if err := decodeStrict(response.Body, &state); err != nil {
			return mrWriteSnapshot{}, err
		}
		if state.State != "opened" && state.State != "closed" {
			return mrWriteSnapshot{}, uxv1.NewError(uxv1.CodeConflict, "merge request is neither opened nor closed")
		}
		return mrWriteSnapshot{Identity: identity, State: state.State}, nil
	}
	initial, err := load(preflight)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if initial.State != parsed.Values["--expected-state"] {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "merge request state differs from --expected-state")
	}
	desired := "closed"
	if action == "reopen" {
		desired = "opened"
	}
	inputValue := map[string]any{"state_event": action}
	operation := mrOpStateUpdate
	if isNote {
		inputValue, operation = map[string]any{"body": body}, mrOpNoteCreate
	}
	input, cleanup, err := writePrivateJSON(inputValue)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	defer cleanup()
	final, err := load(preflight)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if final.State != initial.State || !sameMRDiscussionSnapshot(final.Identity, initial.Identity) {
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeConflict, "merge request changed during write preflight")
	}
	receipt := mrWriteReceipt{Action: action, Outcome: "unchanged", IID: iid, WebURL: initial.Identity.WebURL, SourceBranch: initial.Identity.SourceBranch, TargetBranch: initial.Identity.TargetBranch, HeadSHA: initial.Identity.HeadSHA, ExpectedState: initial.State, ObservedState: initial.State}
	result := func() commandOutput { return commandOutput{data: map[string]any{"write": receipt}, meta: meta} }
	if !isNote && initial.State == desired {
		return result(), nil
	}
	cancelPreflight()
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return commandOutput{meta: meta}, uxv1.Wrap(uxv1.CodeCanceled, "merge request write canceled before mutation", ctx.Err())
		}
		return commandOutput{meta: meta}, uxv1.Wrap(uxv1.CodeUpstream, "merge request write timed out before mutation", ctx.Err())
	}
	mutation, cancelMutation := context.WithTimeout(ctx, limits.MergeMutationOperation)
	response, writeErr := client.Do(mutation, glab.Request{Operation: operation, Host: target.Host, Repo: target.Repo, IID: iid, InputFile: input})
	cancelMutation()
	receipt.Attempts, receipt.Outcome, receipt.ObservedState = 1, "unknown", ""
	if response.UpstreamVersion != "" {
		meta.UpstreamVersion = response.UpstreamVersion
	}
	if boundErr := budget.add(response.Body); boundErr != nil {
		writeErr = errors.Join(writeErr, boundErr)
	}
	// Only a validated note in this invocation's successful POST response gives
	// us an attributable ID. Never search by body, timestamp, or latest note.
	var created DiscussionNote
	if isNote && writeErr == nil {
		created, writeErr = validateMRCreatedNote(response.Body, initial, body, 0)
	}
	readCtx, cancelRead := context.WithTimeout(ctx, limits.MergeReconcileOperation)
	defer cancelRead()
	post, readErr := load(readCtx)
	if readErr == nil && !sameMRWriteTarget(initial.Identity, post.Identity) {
		readErr = uxv1.NewError(uxv1.CodeConflict, "merge request identity changed after mutation")
	}
	if readErr == nil {
		receipt.ObservedState = post.State
	}
	if isNote && writeErr == nil && readErr == nil && post.State == initial.State {
		noteResponse, noteErr := client.Do(readCtx, glab.Request{Operation: mrOpNoteView, Host: target.Host, Repo: target.Repo, IID: iid, ID: created.ID})
		if noteErr == nil {
			noteErr = budget.add(noteResponse.Body)
		}
		if noteErr == nil {
			var note DiscussionNote
			note, noteErr = validateMRCreatedNote(noteResponse.Body, initial, body, created.ID)
			if noteErr == nil && (note.Author.ID != created.Author.ID || note.Author.Username != created.Author.Username || !note.CreatedAt.Equal(created.CreatedAt)) {
				noteErr = uxv1.NewError(uxv1.CodeSafety, "note attribution changed after creation")
			}
		}
		if noteErr == nil {
			receipt.Outcome, receipt.NoteID = "created", created.ID
			receipt.NoteURL = receipt.WebURL + "#note_" + strconv.FormatInt(created.ID, 10)
			return result(), nil
		}
		readErr = noteErr
	}
	if !isNote && readErr == nil && post.State == desired {
		receipt.Outcome = "observed"
		return result(), nil
	}
	code := uxv1.CodeAmbiguousUpdate
	if isNote {
		code = uxv1.CodeAmbiguousCreate
	}
	failure := uxv1.Wrap(code, "merge request write outcome is ambiguous; do not blindly retry", errors.Join(writeErr, readErr))
	if writeErr != nil && readErr == nil && post.State == initial.State {
		if rejection, ok := uxv1.NewHTTPRejection(uxv1.AsError(writeErr).StatusCode); ok {
			failure = rejection
			receipt.Outcome = "rejected"
		}
	}
	failure.Receipt = map[string]any{"write": receipt}
	return commandOutput{meta: meta}, failure
}

func sameMRWriteTarget(left, right mrDiscussionSnapshotIdentity) bool {
	// State writes and comments may legitimately change updated_at, but never
	// the selected numeric identity, projects, branches, or diff base/head.
	right.UpdatedAt = left.UpdatedAt
	return sameMRDiscussionSnapshot(left, right)
}

func validateMRCreatedNote(body []byte, snapshot mrWriteSnapshot, expectedBody string, expectedID int64) (DiscussionNote, error) {
	if err := validateUniqueJSON(body, '{', "created note"); err != nil {
		return DiscussionNote{}, err
	}
	var raw upstreamDiscussionNote
	if err := decodeStrict(body, &raw); err != nil {
		return DiscussionNote{}, err
	}
	normalizer := discussionPageNormalizer{mrID: snapshot.Identity.ID, iid: snapshot.Identity.IID, projectID: snapshot.Identity.TargetProjectID, seenNoteIDs: map[int64]bool{}}
	note, cut, err := normalizer.normalizeNote(raw)
	if err != nil {
		return DiscussionNote{}, err
	}
	if cut || note.Body != expectedBody || note.System || note.Resolvable || note.Resolved || note.Position != nil || note.Type != "" || expectedID > 0 && note.ID != expectedID {
		return DiscussionNote{}, uxv1.NewError(uxv1.CodeSafety, "response did not prove the exact ordinary merge request note")
	}
	return note, nil
}
