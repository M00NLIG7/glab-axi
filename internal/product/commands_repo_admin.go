package product

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
	"gl-axi/internal/privatefile"
	"gl-axi/internal/productnative"
	"gl-axi/internal/safeurl"
)

const repoAdminRace = "GitLab provides no atomic expected project revision; one native credential is pinned for this operation, but identity/settings checks cannot eliminate provider-side races. Never blindly retry a mutation."

// These are deliberately not GitHub owner aliases. A GitLab personal namespace
// ID is not a user ID, and a subgroup is not its parent group.
type adminNamespace struct {
	ID       int64  `json:"id"`
	FullPath string `json:"full_path"`
	Kind     string `json:"kind"`
}
type adminAccount struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}
type adminSettings struct {
	Description         string `json:"description"`
	Visibility          string `json:"visibility"`
	DefaultBranch       string `json:"default_branch"`
	Issues              string `json:"issues_access_level"`
	Wiki                string `json:"wiki_access_level"`
	PipelineRequired    bool   `json:"only_allow_merge_if_pipeline_succeeds"`
	DiscussionsRequired bool   `json:"only_allow_merge_if_all_discussions_are_resolved"`
	SkippedAllowed      bool   `json:"allow_merge_on_skipped_pipeline"`
	MergeMethod         string `json:"merge_method"`
	SquashOption        string `json:"squash_option"`
}
type adminProject struct {
	ID        int64          `json:"id"`
	Path      string         `json:"path_with_namespace"`
	URL       string         `json:"web_url"`
	Namespace adminNamespace `json:"namespace"`
	UpdatedAt string         `json:"updated_at"`
	adminSettings
}
type adminForkSource struct {
	ID   int64  `json:"id"`
	Path string `json:"path_with_namespace"`
	URL  string `json:"web_url"`
}
type adminProviderProject struct {
	adminProject
	ImportStatus string           `json:"import_status"`
	ForkedFrom   *adminForkSource `json:"forked_from_project"`
}
type adminReceipt struct {
	Operation         string         `json:"operation"`
	Outcome           string         `json:"outcome"`
	MutationAttempted bool           `json:"mutation_attempted"`
	Reconciled        bool           `json:"reconciled"`
	Host              string         `json:"host"`
	ProjectPath       string         `json:"project_path"`
	Namespace         adminNamespace `json:"namespace"`
	Account           adminAccount   `json:"account"`
	Project           *adminProject  `json:"project,omitempty"`
	SourceID          int64          `json:"source_id,omitempty"`
	ImportStatus      string         `json:"import_status,omitempty"`
	Residual          string         `json:"residual"`
	LocalEffects      string         `json:"local_effects"`
}
type adminOutput struct {
	Administration adminReceipt `json:"administration"`
}

type adminSession struct {
	client    *productnative.Client
	target    Target
	authority safeurl.Authority
	meta      uxv1.Meta
}

func repoAdminFlags(action string) []FlagDefinition {
	flags := []FlagDefinition{
		{Name: "--allow-project-admin", Boolean: true, Required: true, Description: "Explicitly authorize this consequential project operation."},
		{Name: "--expected-user-id", Value: "ID", Required: true, Description: "Exact authenticated user ID (not namespace ID)."},
		{Name: "--expected-username", Value: "NAME", Required: true, Description: "Exact authenticated username."},
		{Name: "--namespace-id", Value: "ID", Required: true, Description: "Exact destination namespace ID; never defaults to the account."},
		{Name: "--namespace-kind", Value: "user|group", Required: true, Description: "Exact namespace kind; subgroup paths remain distinct."},
		{Name: "--visibility", Value: "private|internal|public", Required: action != "edit", Description: "Explicit project visibility. No implicit public creation."},
	}
	if action == "create" || action == "edit" {
		flags = append(flags, FlagDefinition{Name: "--description-file", Value: "FILE", Description: "Absolute private description file (maximum 2000 UTF-8 bytes)."})
	}
	if action == "edit" {
		flags = append(flags,
			FlagDefinition{Name: "--expected-state-file", Value: "FILE", Required: true, Description: "Private closed project snapshot; see docs/project-administration.md."},
			FlagDefinition{Name: "--accept-non-atomic", Boolean: true, Required: true, Description: "Acknowledge that GitLab cannot enforce an expected project revision."},
			FlagDefinition{Name: "--default-branch", Value: "BRANCH", Description: "Set an existing default branch; does not create a branch."},
			FlagDefinition{Name: "--issues-access-level", Value: "disabled|private|enabled", Description: "Exact GitLab issues feature access, preserving its three-state semantics."},
			FlagDefinition{Name: "--wiki-access-level", Value: "disabled|private|enabled", Description: "Exact GitLab wiki feature access."})
	}
	if action == "fork" {
		flags = append(flags,
			FlagDefinition{Name: "--destination", Value: "NAMESPACE/PROJECT", Required: true, Description: "Exact new project path on the selected host; -R names the source."},
			FlagDefinition{Name: "--expected-source-id", Value: "ID", Required: true, Description: "Exact source project ID."},
			FlagDefinition{Name: "--wait-seconds", Value: "0..20", Description: "Bounded fork observation, default 0; at most 10 postcondition reads."})
	}
	return flags
}

func adminID(raw string) (int64, bool) {
	n, err := strconv.ParseInt(raw, 10, 64)
	return n, err == nil && n > 0 && strconv.FormatInt(n, 10) == raw
}
func adminDestination(p Parsed) string {
	if p.Definition.Path[1] == "fork" {
		return p.Values["--destination"]
	}
	return p.Values["--repo"]
}
func adminVisibility(s string) bool { return s == "private" || s == "internal" || s == "public" }
func adminAccess(s string) bool     { return s == "disabled" || s == "private" || s == "enabled" }

func validateRepoAdminParsed(p Parsed) error {
	bad := func(message string) error { return uxv1.NewError(uxv1.CodeValidation, message) }
	if err := safeurl.ValidateHost(p.Values["--hostname"]); err != nil {
		return bad("invalid explicit GitLab hostname")
	}
	for _, path := range []string{p.Values["--repo"], adminDestination(p)} {
		if err := safeurl.ValidateProject(path); err != nil {
			return bad("invalid explicit project path")
		}
	}
	for _, flag := range []string{"--expected-user-id", "--namespace-id", "--expected-source-id"} {
		if flag == "--expected-source-id" && p.Definition.Path[1] != "fork" {
			continue
		}
		if _, ok := adminID(p.Values[flag]); !ok {
			return bad(flag + " must be a canonical positive integer")
		}
	}
	username := p.Values["--expected-username"]
	if strings.Contains(username, "/") || safeurl.ValidateProject(username+"/project") != nil {
		return bad("invalid expected username")
	}
	kind := p.Values["--namespace-kind"]
	if kind != "user" && kind != "group" {
		return bad("namespace kind must be user or group")
	}
	dest := adminDestination(p)
	namespace := dest[:strings.LastIndex(dest, "/")]
	if kind == "user" && namespace != username {
		return bad("personal namespace must exactly match the expected authenticated username")
	}
	if v := p.Values["--visibility"]; v != "" && !adminVisibility(v) {
		return bad("visibility must be private, internal, or public")
	}
	for _, flag := range []string{"--issues-access-level", "--wiki-access-level"} {
		if v := p.Values[flag]; v != "" && !adminAccess(v) {
			return bad(flag + " must be disabled, private, or enabled")
		}
	}
	if branch := p.Values["--default-branch"]; branch != "" && (safeurl.ValidateBranch(branch) != nil || strings.HasPrefix(branch, "refs/")) {
		return bad("default branch must be a short Git branch name")
	}
	if p.Definition.Path[1] == "edit" && p.Values["--description-file"] == "" && p.Values["--visibility"] == "" && p.Values["--default-branch"] == "" && p.Values["--issues-access-level"] == "" && p.Values["--wiki-access-level"] == "" {
		return bad("repo edit requires at least one setting")
	}
	if p.Definition.Path[1] == "fork" && dest == p.Values["--repo"] {
		return bad("fork source and destination must differ")
	}
	if raw := p.Values["--wait-seconds"]; raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 20 || strconv.Itoa(n) != raw {
			return bad("wait seconds must be an integer from 0 through 20")
		}
	}
	return nil
}

// Reject duplicate keys, unknown fields, missing values and trailing JSON in
// caller snapshots. Provider documents may contain additional documented fields.
func adminJSON(body []byte, out any, closed bool) error {
	if !json.Valid(body) || !utf8Valid(body) {
		return uxv1.NewError(uxv1.CodeValidation, "invalid project JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	var object func() error
	object = func() error {
		token, err := decoder.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			if delim != '{' {
				return io.ErrUnexpectedEOF
			}
			seen := map[string]bool{}
			for decoder.More() {
				key, err := decoder.Token()
				if err != nil {
					return err
				}
				name, ok := key.(string)
				if !ok || seen[name] {
					return io.ErrUnexpectedEOF
				}
				seen[name] = true
				if err := object(); err != nil {
					return err
				}
			}
			_, err = decoder.Token()
			return err
		}
		return nil
	}
	// The private snapshot contains objects and scalar values only.
	if closed {
		if err := object(); err != nil {
			return uxv1.NewError(uxv1.CodeValidation, "snapshot has duplicate keys or invalid structure")
		}
	}
	decoder = json.NewDecoder(bytes.NewReader(body))
	if closed {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(out); err != nil {
		return uxv1.NewError(uxv1.CodeValidation, "invalid project document fields")
	}
	return nil
}

func decodeAdminProject(body []byte, closed bool) (adminProviderProject, error) {
	var project adminProviderProject
	if len(body) > limits.MaxJSONPageBytes {
		return project, uxv1.NewError(uxv1.CodeUpstream, "project response exceeded the byte budget")
	}
	var fields map[string]json.RawMessage
	if err := decodeStrict(body, &fields); err != nil {
		return project, err
	}
	for _, key := range []string{"id", "path_with_namespace", "web_url", "namespace", "updated_at", "description", "visibility", "default_branch", "issues_access_level", "wiki_access_level", "only_allow_merge_if_pipeline_succeeds", "only_allow_merge_if_all_discussions_are_resolved", "allow_merge_on_skipped_pipeline", "merge_method", "squash_option"} {
		v, ok := fields[key]
		if !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) && key != "description" && key != "default_branch" && key != "allow_merge_on_skipped_pipeline" {
			return project, uxv1.NewError(uxv1.CodeSafety, "incomplete project settings evidence")
		}
	}
	if closed {
		if err := adminJSON(body, &project.adminProject, true); err != nil {
			return project, err
		}
	} else if err := decodeStrict(body, &project); err != nil {
		return project, err
	}
	p := project.adminProject
	if p.ID < 1 || p.Namespace.ID < 1 || (p.Namespace.Kind != "user" && p.Namespace.Kind != "group") || !adminVisibility(p.Visibility) || !adminAccess(p.Issues) || !adminAccess(p.Wiki) || len(p.Description) > 2000 || !validIssueEditText(p.Description) || p.DefaultBranch != "" && safeurl.ValidateBranch(p.DefaultBranch) != nil {
		return project, uxv1.NewError(uxv1.CodeSafety, "invalid project settings evidence")
	}
	if timestamp, err := time.Parse(time.RFC3339Nano, p.UpdatedAt); err != nil || timestamp.IsZero() || len(p.UpdatedAt) > 64 {
		return project, uxv1.NewError(uxv1.CodeSafety, "invalid project revision evidence")
	}
	if p.MergeMethod != "merge" && p.MergeMethod != "rebase_merge" && p.MergeMethod != "ff" {
		return project, uxv1.NewError(uxv1.CodeSafety, "unknown project merge method")
	}
	if p.SquashOption != "never" && p.SquashOption != "always" && p.SquashOption != "default_on" && p.SquashOption != "default_off" {
		return project, uxv1.NewError(uxv1.CodeSafety, "unknown project squash setting")
	}
	return project, nil
}

func (s *adminSession) get(ctx context.Context, path string) (productnative.Response, error) {
	return s.client.Do(ctx, productnative.Request{Method: http.MethodGet, Path: path, MaxBytes: limits.MaxJSONPageBytes})
}
func (s *adminSession) identities(ctx context.Context, account adminAccount, ns adminNamespace) error {
	r, err := s.get(ctx, "user")
	if err != nil {
		return err
	}
	var user adminAccount
	if err := decodeStrict(r.Body, &user); err != nil {
		return err
	}
	if user != account {
		return uxv1.NewError(uxv1.CodeSafety, "authenticated account does not match the explicit account")
	}
	r, err = s.get(ctx, "namespaces/"+strconv.FormatInt(ns.ID, 10))
	if err != nil {
		return err
	}
	var got adminNamespace
	if err := decodeStrict(r.Body, &got); err != nil {
		return err
	}
	if got != ns {
		return uxv1.NewError(uxv1.CodeSafety, "namespace identity does not match the explicit destination")
	}
	return nil
}
func (s *adminSession) project(ctx context.Context, path string, id int64, ns *adminNamespace) (adminProviderProject, error) {
	r, err := s.get(ctx, "projects/"+url.PathEscape(path))
	if err != nil {
		return adminProviderProject{}, err
	}
	p, err := decodeAdminProject(r.Body, false)
	if err != nil {
		return p, err
	}
	return p, s.bind(p, path, id, ns)
}
func (s *adminSession) bind(p adminProviderProject, path string, id int64, ns *adminNamespace) error {
	if p.Path != path || p.URL != s.authority.ExpectedProjectURL(path) || id > 0 && p.ID != id || ns != nil && p.Namespace != *ns || p.Namespace.FullPath != path[:strings.LastIndex(path, "/")] {
		return uxv1.NewError(uxv1.CodeSafety, "project identity does not match the explicit target")
	}
	return nil
}
func (s *adminSession) absent(ctx context.Context, path string) error {
	_, err := s.get(ctx, "projects/"+url.PathEscape(path))
	if err != nil && uxv1.AsError(err).StatusCode == 404 {
		return nil
	}
	if err != nil {
		return err
	}
	return uxv1.NewError(uxv1.CodeConflict, "destination already exists or is ambiguous; no mutation attempted")
}
func (s *adminSession) output(r adminReceipt) commandOutput {
	return commandOutput{data: adminOutput{r}, meta: s.meta}
}
func (s *adminSession) failure(r adminReceipt, code uxv1.Code, message string) (commandOutput, error) {
	s.meta.Complete = false
	err := uxv1.NewError(code, message)
	err.Receipt = adminOutput{r}
	return s.output(r), err
}

func executeRepoAdmin(ctx context.Context, p Parsed, deps Dependencies, meta uxv1.Meta) (commandOutput, error) {
	target := Target{Host: p.Values["--hostname"], Repo: p.Values["--repo"]}
	s := adminSession{target: target, meta: meta}
	action := p.Definition.Path[1]
	dest := adminDestination(p)
	nsID, _ := adminID(p.Values["--namespace-id"])
	userID, _ := adminID(p.Values["--expected-user-id"])
	r := adminReceipt{Operation: action, Outcome: "unchanged", Host: target.Host, ProjectPath: dest,
		Namespace: adminNamespace{ID: nsID, FullPath: dest[:strings.LastIndex(dest, "/")], Kind: p.Values["--namespace-kind"]},
		Account:   adminAccount{ID: userID, Username: p.Values["--expected-username"]}, Residual: repoAdminRace, LocalEffects: "none"}
	var description *string
	if path := p.Values["--description-file"]; (action == "create" || action == "edit") && path != "" {
		text, err := privatefile.Read(path, 2000, false)
		if err != nil {
			return s.output(r), err
		}
		description = &text
	}
	var expected adminProviderProject
	if action == "edit" {
		text, err := privatefile.Read(p.Values["--expected-state-file"], 16<<10, false)
		if err != nil {
			return s.output(r), err
		}
		expected, err = decodeAdminProject([]byte(text), true)
		if err != nil {
			return s.output(r), err
		}
		if expected.Path != dest || expected.Namespace != r.Namespace {
			return s.output(r), uxv1.NewError(uxv1.CodeSafety, "expected project identity does not match the explicit target")
		}
	}
	// Select one existing native credential only after syntax and private input
	// validation. Every read, mutation and reconciliation uses this same client.
	client, openErr := openNative(ctx, p, deps)
	if openErr != nil {
		return s.output(r), openErr
	}
	defer client.Close()
	host := client.Host()
	s.client, s.authority = client, host.Authority
	s.target.Host, target.Host, r.Host, s.meta.Host = host.Name, host.Name, host.Name, host.Name
	s.meta.Backend, s.meta.UpstreamVersion = "native", ""
	if action == "edit" {
		if err := s.bind(expected, dest, expected.ID, &r.Namespace); err != nil {
			return s.output(r), err
		}
	}
	// Keep time for exactly one mutation and bounded canonical reconciliation.
	preflight, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	if err := s.identities(preflight, r.Account, r.Namespace); err != nil {
		return s.output(r), err
	}
	var before adminProviderProject
	var err error
	if action == "edit" {
		before, err = s.project(preflight, dest, expected.ID, &r.Namespace)
		if err == nil && before.adminProject != expected.adminProject {
			err = uxv1.NewError(uxv1.CodeConflict, "project settings differ from expected prestate")
		}
	} else {
		err = s.absent(preflight, dest)
		if err == nil && action == "fork" {
			r.SourceID, _ = adminID(p.Values["--expected-source-id"])
			before, err = s.project(preflight, target.Repo, r.SourceID, nil)
		}
	}
	if err != nil {
		return s.output(r), err
	}
	payload := map[string]any{}
	desired := before.adminSettings
	if description != nil {
		payload["description"] = *description
		desired.Description = *description
	}
	if v := p.Values["--visibility"]; v != "" {
		payload["visibility"] = v
		desired.Visibility = v
	}
	method, route := http.MethodPost, "projects"
	if action == "edit" {
		method, route = http.MethodPut, "projects/"+strconv.FormatInt(before.ID, 10)
		for _, setting := range []struct {
			flag, key string
			value     *string
		}{
			{"--default-branch", "default_branch", &desired.DefaultBranch},
			{"--issues-access-level", "issues_access_level", &desired.Issues},
			{"--wiki-access-level", "wiki_access_level", &desired.Wiki},
		} {
			if v := p.Values[setting.flag]; v != "" {
				payload[setting.key] = v
				*setting.value = v
			}
		}
	} else {
		name := dest[strings.LastIndex(dest, "/")+1:]
		payload["namespace_id"], payload["path"], payload["name"] = nsID, name, name
		if action == "fork" {
			route = "projects/" + strconv.FormatInt(before.ID, 10) + "/fork"
		} else {
			payload["initialize_with_readme"] = false
		}
	}
	input, err := json.Marshal(payload)
	if err != nil {
		return s.output(r), uxv1.NewError(uxv1.CodeInternal, "cannot encode project settings")
	}
	// No namespace search, fuzzy match, environment owner default or pagination.
	if err := s.identities(preflight, r.Account, r.Namespace); err != nil {
		return s.output(r), err
	}
	if action == "edit" || action == "fork" {
		adjacent, readErr := s.project(preflight, target.Repo, before.ID, nil)
		if readErr != nil {
			return s.output(r), readErr
		}
		if adjacent.adminProject != before.adminProject {
			return s.output(r), uxv1.NewError(uxv1.CodeConflict, "project changed during administration preflight")
		}
	}
	if action != "edit" {
		if err := s.absent(preflight, dest); err != nil {
			return s.output(r), err
		}
	}
	if err := preflight.Err(); err != nil {
		return s.output(r), issueEditValidationContextError(err)
	}
	if action == "edit" && desired == before.adminSettings {
		r.Project = &before.adminProject
		return s.output(r), nil
	}
	if err := ctx.Err(); err != nil {
		return s.output(r), issueEditValidationContextError(err)
	}
	r.MutationAttempted = true
	r.Outcome = "ambiguous"
	writeCtx, writeCancel := context.WithTimeout(ctx, 10*time.Second)
	response, writeErr := client.Do(writeCtx, productnative.Request{Method: method, Path: route, Body: input, Headers: http.Header{"Content-Type": {"application/json"}}, MaxBytes: limits.MaxJSONPageBytes})
	writeCancel()
	// A mutation response alone is never a postcondition. An invalid successful
	// identity is never laundered into success by a later path lookup.
	var returned adminProviderProject
	identityInvalid := false
	if writeErr == nil {
		returned, err = decodeAdminProject(response.Body, false)
		if err == nil {
			wantID := int64(0)
			if action == "edit" {
				wantID = before.ID
			}
			err = s.bind(returned, dest, wantID, &r.Namespace)
		}
		identityInvalid = err != nil
		if action == "fork" && returned.ForkedFrom != nil {
			from := returned.ForkedFrom
			identityInvalid = identityInvalid || from.ID != r.SourceID || from.Path != target.Repo || from.URL != s.authority.ExpectedProjectURL(target.Repo)
		}
	}
	postCtx, postCancel := context.WithTimeout(ctx, 20*time.Second)
	defer postCancel()
	id := returned.ID
	if writeErr != nil || identityInvalid {
		id = 0
	}
	if action == "edit" {
		id = before.ID
	}
	observed, readErr := s.project(postCtx, dest, id, &r.Namespace)
	code := uxv1.CodeAmbiguousCreate
	if action == "edit" {
		code = uxv1.CodeAmbiguousUpdate
	}
	if readErr != nil || identityInvalid {
		return s.failure(r, code, "project mutation outcome is ambiguous; inspect the exact target before any further action")
	}
	r.Project = &observed.adminProject
	if action != "edit" && writeErr != nil {
		return s.failure(r, code, "destination observed after an uncertain write; creation cannot be attributed to this attempt")
	}
	if action == "edit" {
		if observed.adminSettings != desired {
			return s.failure(r, code, "project postconditions or preserved merge settings did not match; no retry was attempted")
		}
		r.Outcome = "completed"
		r.Reconciled = writeErr != nil
		return s.output(r), nil
	}
	if observed.Visibility != p.Values["--visibility"] || description != nil && observed.Description != *description {
		return s.failure(r, code, "created project metadata did not match; no corrective mutation was attempted")
	}
	if action == "create" {
		r.Outcome = "completed"
		return s.output(r), nil
	}
	return s.observeFork(postCtx, p, r, observed)
}

func (s *adminSession) observeFork(ctx context.Context, p Parsed, r adminReceipt, project adminProviderProject) (commandOutput, error) {
	seconds, _ := strconv.Atoi(p.Values["--wait-seconds"])
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()
	for reads := 1; ; reads++ {
		from := project.ForkedFrom
		if from != nil && (from.ID != r.SourceID || from.Path != s.target.Repo || from.URL != s.authority.ExpectedProjectURL(s.target.Repo)) {
			r.Outcome = "ambiguous"
			return s.failure(r, uxv1.CodeAmbiguousCreate, "fork source identity did not match")
		}
		if project.ImportStatus == "finished" && from == nil {
			r.Outcome = "ambiguous"
			return s.failure(r, uxv1.CodeAmbiguousCreate, "completed fork lacks exact source identity evidence")
		}
		snapshot := project.adminProject
		r.Project = &snapshot
		r.ImportStatus = project.ImportStatus
		switch project.ImportStatus {
		case "finished":
			r.Outcome = "completed"
			return s.output(r), nil
		case "failed":
			r.Outcome = "failed"
			return s.failure(r, uxv1.CodeUpstream, "GitLab reported fork import failure; the project may remain and was not deleted")
		case "none", "scheduled":
			r.Outcome = "accepted"
		case "started":
			r.Outcome = "in_progress"
		default:
			r.Outcome = "ambiguous"
			r.ImportStatus = "unknown"
			return s.failure(r, uxv1.CodeAmbiguousCreate, "unknown fork import status; readiness is unproven")
		}
		s.meta.Complete = false
		s.meta.Reason = "fork_incomplete"
		if seconds == 0 || reads >= 10 {
			return s.output(r), nil
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			if ctx.Err() == context.DeadlineExceeded {
				s.meta.Reason = "fork_observation_deadline"
				return s.output(r), nil
			}
			return s.failure(r, uxv1.CodeCanceled, "fork observation was canceled after mutation; the accepted fork may still complete")
		case <-timer.C:
		}
		next, err := s.project(ctx, r.ProjectPath, r.Project.ID, &r.Namespace)
		if err != nil || ctx.Err() != nil {
			if ctx.Err() == context.DeadlineExceeded {
				s.meta.Reason = "fork_observation_deadline"
				return s.output(r), nil
			}
			if ctx.Err() == context.Canceled {
				return s.failure(r, uxv1.CodeCanceled, "fork observation was canceled after mutation; the accepted fork may still complete")
			}
			r.Outcome = "ambiguous"
			return s.failure(r, uxv1.CodeAmbiguousCreate, "fork observation failed; no mutation was retried")
		}
		if next.Visibility != r.Project.Visibility || next.Description != r.Project.Description {
			r.Outcome = "ambiguous"
			return s.failure(r, uxv1.CodeAmbiguousCreate, "fork settings drifted while observing completion")
		}
		project = next
		s.meta.Complete = true
		s.meta.Reason = ""
	}
}
