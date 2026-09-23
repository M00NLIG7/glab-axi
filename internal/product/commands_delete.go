package product

import (
	"context"
	"encoding/json"
	"errors"
	"mime"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
	"gl-axi/internal/productnative"
	"gl-axi/internal/safeurl"
)

func validateDeletionParsed(p Parsed) error {
	group := p.Definition.Path[0]
	if len(p.Positionals) != 1 {
		return uxv1.NewError(uxv1.CodeValidation, "one exact deletion selector is required")
	}
	if group == "release" {
		if safeurl.ValidateBranch(p.Positionals[0]) != nil || len(p.Positionals[0]) > 512 || strings.Contains(p.Positionals[0], "%") {
			return uxv1.NewError(uxv1.CodeValidation, "release tag must be an exact valid Git tag without percent escapes")
		}
	} else if _, err := deletionID(p.Positionals[0]); err != nil {
		return err
	}
	for _, flag := range []string{"--expected-id", "--expected-project-id", "--expected-author-id"} {
		if raw, present := p.Values[flag]; present {
			if _, err := deletionID(raw); err != nil {
				return err
			}
		}
	}
	for _, flag := range []string{"--expected-updated-at", "--expected-created-at"} {
		if raw, present := p.Values[flag]; present {
			stamp, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil || stamp.IsZero() || len(raw) > 64 {
				return uxv1.NewError(uxv1.CodeValidation, "deletion timestamps must be exact nonzero RFC 3339 values")
			}
		}
	}
	if group == "issue" && p.Values["--expected-state"] != "opened" && p.Values["--expected-state"] != "closed" {
		return uxv1.NewError(uxv1.CodeValidation, "expected issue state must be opened or closed")
	}
	if group == "pipeline" {
		if !validMergeSHA(p.Values["--expected-sha"]) || safeurl.ValidateBranch(p.Values["--expected-ref"]) != nil {
			return uxv1.NewError(uxv1.CodeValidation, "exact valid pipeline ref and lowercase commit SHA are required")
		}
		switch p.Values["--expected-status"] {
		case "created", "waiting_for_resource", "preparing", "pending", "running", "success", "failed", "canceled", "skipped", "manual", "scheduled":
		default:
			return uxv1.NewError(uxv1.CodeValidation, "expected pipeline status is not recognized")
		}
	}
	if group == "release" && !validMergeSHA(p.Values["--expected-commit"]) {
		return uxv1.NewError(uxv1.CodeValidation, "expected release commit must be a lowercase 40- or 64-hex SHA")
	}
	raw := p.Values["--expected-url"]
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || strings.ContainsFunc(raw, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.String() != raw {
		return uxv1.NewError(uxv1.CodeValidation, "expected deletion URL must be an exact HTTPS resource URL")
	}
	if p.Values["--confirm-delete-"+group] != raw {
		return uxv1.NewError(uxv1.CodeSafety, "operation-specific deletion confirmation must exactly equal --expected-url")
	}
	if group == "pipeline" && p.Values["--acknowledge-child-cancellation"] != raw {
		return uxv1.NewError(uxv1.CodeSafety, "deleting this exact parent may cancel surviving child pipelines and their jobs, even if parent deletion later fails; --acknowledge-child-cancellation must equal --expected-url on every invocation in addition to --confirm-delete-pipeline; a snapshot cannot guarantee absence of child effects")
	}
	if group == "release" && p.Values["--acknowledge-catalog-unpublication"] != raw {
		return uxv1.NewError(uxv1.CodeSafety, "deleting this exact release may unpublish the project's CI/CD Catalog resource when its last catalog version is removed; --acknowledge-catalog-unpublication must equal --expected-url on every invocation in addition to --confirm-delete-release; a snapshot cannot guarantee absence of catalog effects, including after an ambiguous response")
	}
	return nil
}

func deletionID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, uxv1.NewError(uxv1.CodeValidation, "deletion identifiers must be canonical positive integers")
	}
	return id, nil
}

type deletionRecord struct {
	ID        int64           `json:"id"`
	IID       int64           `json:"iid"`
	ProjectID json.RawMessage `json:"project_id"`
	URL       string          `json:"web_url"`
	State     string          `json:"state"`
	UpdatedAt string          `json:"updated_at"`
	CreatedAt string          `json:"created_at"`
	SHA       string          `json:"sha"`
	Ref       string          `json:"ref"`
	Status    string          `json:"status"`
	Tag       string          `json:"tag_name"`
	Commit    struct {
		ID string `json:"id"`
	} `json:"commit"`
	Author struct {
		ID int64 `json:"id"`
	} `json:"author"`
	Links struct {
		Self string `json:"self"`
	} `json:"_links"`
}

type deletionActor struct {
	ID    int64  `json:"id"`
	State string `json:"state"`
}

type deletionProject struct {
	ID   int64  `json:"id"`
	Path string `json:"path_with_namespace"`
	URL  string `json:"web_url"`
}

type deletionExpected struct {
	State     string `json:"state,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	CreatedAt string `json:"created_at,omitempty"`
	SHA       string `json:"sha,omitempty"`
	Ref       string `json:"ref,omitempty"`
	Status    string `json:"status,omitempty"`
	AuthorID  int64  `json:"author_id,omitempty"`
}

type deletionReceipt struct {
	Action               string                        `json:"action"`
	Resource             string                        `json:"resource"`
	Scope                string                        `json:"scope"`
	URL                  string                        `json:"url"`
	ProjectID            int64                         `json:"project_id,omitempty"`
	ID                   int64                         `json:"id,omitempty"`
	IID                  int64                         `json:"iid,omitempty"`
	Tag                  string                        `json:"tag,omitempty"`
	ActorID              int64                         `json:"actor_id"`
	Expected             deletionExpected              `json:"expected"`
	DeleteAttempted      bool                          `json:"delete_attempted"`
	DeleteStatus         int                           `json:"delete_status,omitempty"`
	Acknowledged         bool                          `json:"acknowledged"`
	Postcondition        string                        `json:"postcondition"`
	TagPostcondition     string                        `json:"tag_postcondition,omitempty"`
	Concurrency          string                        `json:"concurrency"`
	IntendedEffects      []string                      `json:"intended_effects"`
	ChildCancellation    *deletionEffectAcknowledgment `json:"child_cancellation,omitempty"`
	CatalogUnpublication *deletionEffectAcknowledgment `json:"catalog_unpublication,omitempty"`
}

type deletionEffectAcknowledgment struct {
	Acknowledged bool   `json:"acknowledged"`
	Outcome      string `json:"outcome"`
}

type deletionOutput struct {
	Deletion deletionReceipt `json:"deletion"`
}

type deletionSelection struct {
	resource, scope, route, expectedURL, repo string
	projectID, id                             int64
}

func selectDeletion(c *productnative.Client, p Parsed) (deletionSelection, error) {
	s := deletionSelection{resource: p.Definition.Path[0], scope: "project", repo: p.Values["--repo"]}
	s.projectID, _ = deletionID(p.Values["--expected-project-id"])
	s.id, _ = deletionID(p.Positionals[0])
	web := c.Host().Authority.ExpectedProjectURL(s.repo)
	base := "projects/" + strconv.FormatInt(s.projectID, 10)
	switch s.resource {
	case "issue":
		s.route = base + "/issues/" + p.Positionals[0]
		s.expectedURL = web + "/-/issues/" + p.Positionals[0]
	case "pipeline":
		s.route = base + "/pipelines/" + p.Positionals[0]
		s.expectedURL = web + "/-/pipelines/" + p.Positionals[0]
	case "release":
		s.route = base + "/releases/" + url.PathEscape(p.Positionals[0])
		// Rails web route segments preserve these valid Git tag sub-delimiters.
		// Keep the API selector escaped and require the one canonical web URL.
		s.expectedURL = web + "/-/releases/" + strings.NewReplacer(
			"%21", "!", "%27", "'", "%28", "(", "%29", ")", "%2C", ",", "%3B", ";",
		).Replace(url.PathEscape(p.Positionals[0]))
	case "snippet":
		if p.Definition.Path[1] == "delete" {
			s.scope = "personal"
			s.route = "snippets/" + p.Positionals[0]
			s.expectedURL = strings.TrimSuffix(c.Host().Authority.Web.String(), "/") + "/-/snippets/" + p.Positionals[0]
		} else {
			s.route = base + "/snippets/" + p.Positionals[0]
			s.expectedURL = web + "/-/snippets/" + p.Positionals[0]
		}
	default:
		return s, uxv1.NewError(uxv1.CodeUnsupported, "resource deletion is not declared")
	}
	if s.expectedURL != p.Values["--expected-url"] {
		return s, uxv1.NewError(uxv1.CodeSafety, "expected deletion URL does not match the selected native authority and resource")
	}
	return s, nil
}

// One native client binds the complete operation. No write is delegated, no
// endpoint comes from provider output, and reconciliation never mutates.
func executeResourceDeletion(ctx context.Context, p Parsed, deps Dependencies, meta uxv1.Meta) (out commandOutput, err error) {
	meta.Backend, meta.UpstreamVersion, meta.Limit = "native", "", 0
	out.meta = meta
	c, err := openNative(ctx, p, deps)
	if err != nil {
		return out, err
	}
	defer c.Close()
	out.meta.Host = c.Host().Name
	s, err := selectDeletion(c, p)
	if err != nil {
		return out, err
	}
	actor, err := readDeletionActor(ctx, c)
	if err != nil {
		return out, err
	}
	if s.scope == "project" {
		if err := checkDeletionProject(ctx, c, s, true); err != nil {
			return out, err
		}
	}
	if s.resource == "release" {
		if err := checkDeletionTag(ctx, c, s, p); err != nil {
			return out, err
		}
	}
	record, _, err := readDeletionRecord(ctx, c, s.route)
	if err != nil {
		// In particular, initial 404 is not an already-deleted receipt. It can
		// mean invisible/private, with no observed resource identity to delete.
		return out, err
	}
	if err := checkDeletionRecord(record, s, p, actor.ID); err != nil {
		return out, err
	}
	r := newDeletionReceipt(s, record, actor.ID)
	defer func() {
		if err != nil {
			bounded := *uxv1.AsError(err)
			bounded.Retryable = false
			bounded.Receipt = deletionOutput{Deletion: r}
			err = &bounded
		}
	}()
	if s.scope == "project" {
		if err := checkDeletionProject(ctx, c, s, false); err != nil {
			return out, err
		}
	}
	// The second resource read is the last provider operation before DELETE.
	// It detects observed drift but is not an atomic provider expected revision.
	record, _, err = readDeletionRecord(ctx, c, s.route)
	if err != nil {
		return out, err
	}
	if err := checkDeletionRecord(record, s, p, actor.ID); err != nil {
		return out, err
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return out, uxv1.NewError(uxv1.CodeCanceled, "resource deletion canceled before mutation")
		}
		return out, uxv1.NewError(uxv1.CodeUpstream, "resource deletion deadline exceeded before mutation")
	}

	r.DeleteAttempted = true
	if r.ChildCancellation != nil {
		r.ChildCancellation.Outcome = "unverified"
	}
	if r.CatalogUnpublication != nil {
		r.CatalogUnpublication.Outcome = "unverified"
	}
	mutation, cancelMutation := context.WithTimeout(ctx, 10*time.Second)
	response, writeErr := c.Do(mutation, productnative.Request{Method: "DELETE", Path: s.route, MaxBytes: limits.MaxJSONPageBytes})
	cancelMutation()
	r.DeleteStatus = response.StatusCode
	if writeErr == nil {
		if s.resource == "release" {
			var deleted deletionRecord
			writeErr = decodeDeletionResponse(response, &deleted)
			if writeErr == nil {
				writeErr = checkDeletionRecord(deleted, s, p, actor.ID)
			}
		} else if response.StatusCode != 204 || len(response.Body) != 0 {
			writeErr = uxv1.NewError(uxv1.CodeUpstream, "deletion did not return its exact no-content acknowledgment")
		}
	}
	r.Acknowledged = writeErr == nil
	if rejected := deletionRejection(s.resource, response.StatusCode); writeErr != nil && rejected != nil {
		r.Action = "rejected"
		return out, rejected
	}
	if ctx.Err() != nil {
		r.Action = "ambiguous"
		return out, ambiguousDeletion(writeErr)
	}

	reconcile, cancelReconcile := context.WithTimeout(ctx, 10*time.Second)
	defer cancelReconcile()
	_, postStatus, readErr := readDeletionRecord(reconcile, c, s.route)
	switch {
	case postStatus == 404 && uxv1.AsError(readErr) != nil && uxv1.AsError(readErr).StatusCode == 404:
		r.Postcondition = "not_found"
	case readErr == nil:
		r.Postcondition = "present"
	default:
		r.Postcondition = "unverified"
	}
	if r.Postcondition == "not_found" {
		// A scoped 404 alone cannot distinguish deletion from lost access.
		// Recheck the parent and the same effective native account, then the
		// retained release tag. Success additionally requires the DELETE ack.
		if s.scope == "project" {
			readErr = checkDeletionProject(reconcile, c, s, false)
		} else {
			readErr = nil
		}
		if readErr == nil {
			var after deletionActor
			after, readErr = readDeletionActor(reconcile, c)
			if readErr == nil && after != actor {
				readErr = uxv1.NewError(uxv1.CodeSafety, "effective native account changed during deletion")
			}
		}
		if readErr == nil && s.resource == "release" {
			readErr = checkDeletionTag(reconcile, c, s, p)
			if readErr == nil {
				r.TagPostcondition = "unchanged"
			}
		}
		if readErr != nil {
			r.Postcondition = "unverified"
		}
	}
	if r.Acknowledged && r.Postcondition == "not_found" {
		r.Action = "deleted"
		out.meta.Count = 1
		out.data = deletionOutput{Deletion: r}
		return out, nil
	}
	r.Action = "ambiguous"
	return out, ambiguousDeletion(errors.Join(writeErr, readErr))
}

func readDeletionRecord(ctx context.Context, c *productnative.Client, route string) (deletionRecord, int, error) {
	var record deletionRecord
	response, err := c.Do(ctx, productnative.Request{Method: "GET", Path: route, MaxBytes: limits.MaxJSONPageBytes})
	if err == nil {
		err = decodeDeletionResponse(response, &record)
	}
	return record, response.StatusCode, err
}

func decodeDeletionResponse(response productnative.Response, out any) error {
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if response.StatusCode != 200 || err != nil || media != "application/json" || validateUniqueJSON(response.Body, '{', "deletion metadata") != nil {
		return uxv1.NewError(uxv1.CodeUpstream, "deletion metadata is not an exact bounded JSON object response")
	}
	if err := json.Unmarshal(response.Body, out); err != nil {
		return uxv1.NewError(uxv1.CodeUpstream, "deletion metadata is malformed")
	}
	return nil
}

func readDeletionActor(ctx context.Context, c *productnative.Client) (deletionActor, error) {
	var actor deletionActor
	response, err := c.Do(ctx, productnative.Request{Method: "GET", Path: "user", MaxBytes: limits.MaxJSONPageBytes})
	if err == nil {
		err = decodeDeletionResponse(response, &actor)
	}
	if err == nil && (actor.ID <= 0 || actor.State != "active") {
		err = uxv1.NewError(uxv1.CodeSafety, "native deletion requires an identified active account")
	}
	return actor, err
}

func checkDeletionProject(ctx context.Context, c *productnative.Client, s deletionSelection, byPath bool) error {
	selector := strconv.FormatInt(s.projectID, 10)
	if byPath {
		selector = url.PathEscape(s.repo)
	}
	response, err := c.Do(ctx, productnative.Request{Method: "GET", Path: "projects/" + selector, MaxBytes: limits.MaxJSONPageBytes})
	if err != nil {
		return err
	}
	var project deletionProject
	if err := decodeDeletionResponse(response, &project); err != nil {
		return err
	}
	if project.ID != s.projectID || project.Path != s.repo || project.URL != c.Host().Authority.ExpectedProjectURL(s.repo) {
		return uxv1.NewError(uxv1.CodeSafety, "deletion project ID, path or URL does not match the exact target")
	}
	return nil
}

func checkDeletionTag(ctx context.Context, c *productnative.Client, s deletionSelection, p Parsed) error {
	route := "projects/" + strconv.FormatInt(s.projectID, 10) + "/repository/tags/" + url.PathEscape(p.Positionals[0])
	response, err := c.Do(ctx, productnative.Request{Method: "GET", Path: route, MaxBytes: limits.MaxJSONPageBytes})
	if err != nil {
		return err
	}
	var tag struct {
		Name   string `json:"name"`
		Commit struct {
			ID string `json:"id"`
		} `json:"commit"`
	}
	if err := decodeDeletionResponse(response, &tag); err != nil {
		return err
	}
	if tag.Name != p.Positionals[0] || tag.Commit.ID != p.Values["--expected-commit"] {
		return uxv1.NewError(uxv1.CodeConflict, "release tag no longer has the expected name and commit")
	}
	return nil
}

func checkDeletionRecord(r deletionRecord, s deletionSelection, p Parsed, actorID int64) error {
	identity := func() error {
		return uxv1.NewError(uxv1.CodeSafety, "deletion resource identity does not match the exact reviewed target")
	}
	state := func() error {
		return uxv1.NewError(uxv1.CodeConflict, "deletion resource state or revision differs from the reviewed expectation")
	}
	if s.resource == "release" {
		if r.Tag != p.Positionals[0] || r.Links.Self != s.expectedURL {
			return identity()
		}
		if r.Commit.ID != p.Values["--expected-commit"] || r.CreatedAt != p.Values["--expected-created-at"] {
			return state()
		}
		return nil
	}
	if r.ID <= 0 || r.URL != s.expectedURL {
		return identity()
	}
	if s.scope == "personal" {
		if string(r.ProjectID) != "null" {
			return identity()
		}
	} else {
		var projectID int64
		if json.Unmarshal(r.ProjectID, &projectID) != nil || projectID != s.projectID {
			return identity()
		}
	}
	if r.UpdatedAt != p.Values["--expected-updated-at"] {
		return state()
	}
	switch s.resource {
	case "issue":
		id, _ := deletionID(p.Values["--expected-id"])
		if r.IID != s.id || r.ID != id {
			return identity()
		}
		if r.State != p.Values["--expected-state"] {
			return state()
		}
	case "pipeline":
		if r.ID != s.id {
			return identity()
		}
		if r.SHA != p.Values["--expected-sha"] || r.Ref != p.Values["--expected-ref"] || r.Status != p.Values["--expected-status"] {
			return state()
		}
	case "snippet":
		author, _ := deletionID(p.Values["--expected-author-id"])
		if r.ID != s.id || r.Author.ID != author || s.scope == "personal" && actorID != author {
			return identity()
		}
	}
	return nil
}

func newDeletionReceipt(s deletionSelection, record deletionRecord, actorID int64) deletionReceipt {
	r := deletionReceipt{
		Action: "not_applied", Resource: s.resource, Scope: s.scope, URL: s.expectedURL,
		ProjectID: s.projectID,
		ActorID:   actorID, Postcondition: "not_checked", Concurrency: "best_effort_non_atomic",
	}
	switch s.resource {
	case "issue":
		r.ID, r.IID = record.ID, record.IID
		r.Expected = deletionExpected{State: record.State, UpdatedAt: record.UpdatedAt}
		r.IntendedEffects = []string{"delete_issue_not_close"}
	case "pipeline":
		r.ID = record.ID
		r.Expected = deletionExpected{SHA: record.SHA, Ref: record.Ref, Status: record.Status, UpdatedAt: record.UpdatedAt}
		r.IntendedEffects = []string{"delete_related_builds_logs_artifacts_triggers", "expire_pipeline_caches", "do_not_recursively_delete_child_pipelines", "may_cancel_surviving_child_pipelines"}
		r.ChildCancellation = &deletionEffectAcknowledgment{Acknowledged: true, Outcome: "not_attempted"}
	case "release":
		r.Tag = record.Tag
		r.Expected = deletionExpected{SHA: record.Commit.ID, CreatedAt: record.CreatedAt}
		r.IntendedEffects = []string{"delete_release", "may_unpublish_catalog_resource", "retain_tag"}
		r.CatalogUnpublication = &deletionEffectAcknowledgment{Acknowledged: true, Outcome: "not_attempted"}
		r.TagPostcondition = "unverified"
	case "snippet":
		r.ID = record.ID
		r.Expected = deletionExpected{AuthorID: record.Author.ID, UpdatedAt: record.UpdatedAt}
		r.IntendedEffects = []string{"delete_selected_snippet_and_provider_managed_content"}
	}
	return r
}

func deletionRejection(resource string, status int) *uxv1.Error {
	// Snippet repository removal can fail after the database deletion commits;
	// GitLab maps that failure to 400, so only bounded readback is appropriate.
	if resource == "snippet" && status == 400 {
		return nil
	}
	if status == 412 {
		return &uxv1.Error{Code: uxv1.CodeConflict, Message: "GitLab rejected the deletion precondition (HTTP 412)", StatusCode: status}
	}
	if status == 405 {
		return &uxv1.Error{Code: uxv1.CodeUnsupported, Message: "GitLab rejected this deletion method (HTTP 405)", StatusCode: status}
	}
	if rejection, ok := uxv1.NewHTTPRejection(status); ok {
		return rejection
	}
	return nil
}

func ambiguousDeletion(cause error) error {
	return uxv1.Wrap(uxv1.CodeAmbiguousDelete, "deletion outcome is ambiguous; bounded readback did not prove the exact acknowledged deletion; do not blindly retry", cause)
}
