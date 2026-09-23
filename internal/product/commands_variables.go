package product

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"time"

	"gl-axi/internal/civariable"
	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
	"gl-axi/internal/privatefile"
	"gl-axi/internal/productnative"
	"gl-axi/internal/safeurl"
)

func isVariableDefinition(d Definition) bool {
	return len(d.Path) == 2 && (d.Path[0] == "secret" || d.Path[0] == "variable")
}
func validVariableType(v string) bool         { return v == "env_var" || v == "file" }
func validBool(v string) bool                 { return v == "true" || v == "false" }
func variableValidation(message string) error { return uxv1.NewError(uxv1.CodeValidation, message) }
func validateVariableParsed(p Parsed) error {
	v := p.Values
	if safeurl.ValidateHost(v["--hostname"]) != nil || safeurl.ValidateProject(v["--repo"]) != nil {
		return variableValidation("invalid explicit CI variable host or project")
	}
	if !civariable.ValidScope(v["--scope"]) {
		return variableValidation("invalid exact environment scope")
	}
	if p.Definition.Path[1] == "list" {
		return nil
	}
	// The shared private-file boundary does not verify Windows DACLs. Do not
	// claim private persisted inputs or self-managed config support there.
	if runtime.GOOS == "windows" {
		return uxv1.NewError(uxv1.CodeUnsupported, "CI variable mutations are unavailable on Windows without a verified private-file ACL boundary")
	}
	if !civariable.ValidKey(p.Positionals[0]) {
		return variableValidation("CI variable key must contain 1..255 ASCII letters, digits, or underscores")
	}
	id, err := strconv.ParseInt(v["--expected-project-id"], 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != v["--expected-project-id"] {
		return variableValidation("expected project ID must be a canonical positive integer")
	}
	expected, err := url.Parse(v["--expected-project-url"])
	if err != nil || expected.Scheme != "https" || expected.Host == "" || expected.User != nil || expected.RawQuery != "" || expected.Fragment != "" || expected.Opaque != "" {
		return variableValidation("expected project URL must be a canonical HTTPS URL without credentials, query, or fragment")
	}
	// Exact URL binding uses the configured native web base after openNative,
	// before the first request. Logical host and web/API origins may differ.
	class := v["--expected-class"]
	switch class {
	case "absent", "ordinary", "protected", "masked", "hidden":
	default:
		return variableValidation("invalid expected CI variable class")
	}
	prestate := []string{"--expected-type", "--expected-protected", "--expected-raw", "--expected-value-file"}
	if class == "absent" {
		if p.Definition.Path[1] != "set" {
			return variableValidation("delete requires an existing exact prestate")
		}
		for _, flag := range prestate {
			if v[flag] != "" {
				return variableValidation("absent prestate cannot include existing-value guards")
			}
		}
	} else {
		if !validVariableType(v["--expected-type"]) || !validBool(v["--expected-protected"]) || !validBool(v["--expected-raw"]) {
			return variableValidation("existing prestate requires type, protected, and raw guards")
		}
		if class == "hidden" {
			if v["--expected-value-file"] != "" {
				return variableValidation("hidden values cannot be verified; expected-value files are not supported for hidden entries")
			}
		} else if v["--expected-value-file"] == "" || v["--expected-value-file"] == "-" {
			return variableValidation("unhidden prestate requires a private expected-value file")
		}
		if p.Definition.Path[0] == "variable" && class != "ordinary" || p.Definition.Path[0] == "secret" && class == "ordinary" {
			return uxv1.NewError(uxv1.CodeSafety, "selected class belongs to the other CI variable surface")
		}
		if class == "ordinary" && v["--expected-protected"] != "false" || class == "protected" && v["--expected-protected"] != "true" {
			return variableValidation("expected class and protection disagree")
		}
	}
	if p.Definition.Path[1] == "set" {
		if !validVariableType(v["--type"]) || !validBool(v["--protected"]) {
			return variableValidation("set requires env_var or file type and true or false protection")
		}
		if p.Definition.Path[0] == "variable" && v["--protected"] != "false" {
			return uxv1.NewError(uxv1.CodeSafety, "ordinary variables cannot carry secret protection")
		}
		if class != "absent" {
			if v["--type"] != v["--expected-type"] || v["--protected"] != v["--expected-protected"] {
				return uxv1.NewError(uxv1.CodeSafety, "set preserves existing type and protection; transitions are not supported")
			}
			if p.Definition.Path[0] == "secret" && class != "hidden" {
				return uxv1.NewError(uxv1.CodeUnsupported, "GitLab cannot hide an existing unhidden variable; secret set only rotates hidden entries")
			}
		}
	}
	return nil
}

type variableReceipt struct {
	Action               string               `json:"action"`
	Outcome              string               `json:"outcome"`
	ProjectID            int64                `json:"project_id"`
	ProjectURL           string               `json:"project_url"`
	Key                  string               `json:"key"`
	EnvironmentScope     string               `json:"environment_scope"`
	AtomicPrecondition   bool                 `json:"atomic_precondition"`
	MutationAttempted    bool                 `json:"mutation_attempted"`
	ProviderAcknowledged bool                 `json:"provider_acknowledged"`
	Reconciliation       string               `json:"reconciliation"`
	ValueVerification    string               `json:"value_verification"`
	State                *civariable.Metadata `json:"state,omitempty"`
}
type variableMutationOutput struct {
	Variable variableReceipt `json:"variable"`
}

func readVariableInput(ctx context.Context, path string, stdin io.Reader) (string, error) {
	if path != "-" {
		value, err := privatefile.Read(path, civariable.MaxValueBytes, false)
		if err != nil {
			return "", uxv1.NewError(uxv1.CodeSafety, "CI variable input must be a bounded private regular file")
		}
		return value, nil
	}
	if stdin == nil {
		return "", variableValidation("piped stdin is required")
	}
	if file, ok := stdin.(*os.File); ok {
		info, err := file.Stat()
		if err != nil || info.Mode()&os.ModeCharDevice != 0 || info.Mode().IsRegular() && info.Mode().Perm()&0o077 != 0 {
			return "", uxv1.NewError(uxv1.CodeSafety, "stdin must be a pipe or private regular file, not a terminal")
		}
	}
	type result struct {
		body []byte
		err  error
	}
	resultCh := make(chan result)
	go func() {
		b, err := io.ReadAll(io.LimitReader(stdin, civariable.MaxValueBytes+1))
		select {
		case resultCh <- result{b, err}:
		case <-ctx.Done():
			clear(b)
		}
	}()
	select {
	case <-ctx.Done():
		if closer, ok := stdin.(io.Closer); ok {
			_ = closer.Close()
		}
		return "", uxv1.NewError(uxv1.CodeCanceled, "CI variable input was canceled or timed out")
	case r := <-resultCh:
		defer clear(r.body)
		if r.err != nil {
			return "", variableValidation("cannot read private CI variable input")
		}
		return string(r.body), nil
	}
}

var variableVersionPattern = regexp.MustCompile(`^([0-9]+)\.([0-9]+)\.([0-9]+)(-ee|-pre)?$`)

func variableJSON(response productnative.Response, out any) error {
	media, _, err := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if err != nil || media != "application/json" || validateUniqueJSON(response.Body, '{', "CI variable metadata") != nil || json.Unmarshal(response.Body, out) != nil {
		return uxv1.NewError(uxv1.CodeUpstream, "CI variable metadata is malformed or unavailable")
	}
	return nil
}
func requireVariableVersion(ctx context.Context, c *productnative.Client) error {
	r, err := c.Do(ctx, productnative.Request{Method: http.MethodGet, Path: "version", MaxBytes: limits.MaxJSONPageBytes})
	defer clear(r.Body)
	if err != nil {
		return err
	}
	var version struct {
		Version string `json:"version"`
	}
	if err := variableJSON(r, &version); err != nil {
		return err
	}
	match := variableVersionPattern.FindStringSubmatch(version.Version)
	if match == nil {
		return uxv1.NewError(uxv1.CodeUnsupported, "GitLab CI variable version capability is unavailable")
	}
	major, majorErr := strconv.Atoi(match[1])
	minor, minorErr := strconv.Atoi(match[2])
	patch, patchErr := strconv.Atoi(match[3])
	if majorErr != nil || minorErr != nil || patchErr != nil {
		return uxv1.NewError(uxv1.CodeUnsupported, "GitLab CI variable version capability is unavailable")
	}
	if major < 17 || major == 17 && (minor < 6 || minor == 6 && patch == 0 && match[4] == "-pre") {
		return uxv1.NewError(uxv1.CodeUnsupported, "CI variable commands require GitLab 17.6 or newer with hidden metadata")
	}
	return nil
}
func loadVariableProject(ctx context.Context, c *productnative.Client, repo string) (ensureProject, error) {
	r, err := c.Do(ctx, productnative.Request{Method: http.MethodGet, Path: "projects/" + url.PathEscape(repo), MaxBytes: limits.MaxJSONPageBytes})
	defer clear(r.Body)
	if err != nil {
		return ensureProject{}, err
	}
	var project ensureProject
	if variableJSON(r, &project) != nil || project.ID < 1 || project.PathWithNamespace != repo || project.WebURL != c.Host().Authority.ExpectedProjectURL(repo) {
		return ensureProject{}, uxv1.NewError(uxv1.CodeSafety, "GitLab returned a different or invalid project identity")
	}
	return project, nil
}

// Complete inventory is required to prove absence. A 404, even a definite
// HTTP response, never proves absence. One native client owns the whole budget.
func variableInventory(ctx context.Context, c *productnative.Client, id int64, match civariable.Comparison) ([]civariable.Observation, error) {
	all := make([]civariable.Observation, 0)
	seen := map[string]bool{}
	for page := 1; page <= limits.MaxPages; page++ {
		if ctx.Err() != nil {
			return nil, uxv1.NewError(uxv1.CodeCanceled, "CI variable inventory was canceled")
		}
		r, err := c.Do(ctx, productnative.Request{Method: http.MethodGet, Path: "projects/" + strconv.FormatInt(id, 10) + "/variables", Query: url.Values{"page": {strconv.Itoa(page)}, "per_page": {"100"}}, MaxBytes: limits.MaxJSONPageBytes})
		if err != nil {
			clear(r.Body)
			return nil, err
		}
		media, _, mediaErr := mime.ParseMediaType(r.Header.Get("Content-Type"))
		observations, decodeErr := civariable.Decode(r.Body, match)
		clear(r.Body) // Values and descriptions never leave this read boundary.
		if mediaErr != nil || media != "application/json" || decodeErr != nil {
			return nil, uxv1.NewError(uxv1.CodeUpstream, "CI variable inventory is invalid or unavailable")
		}
		for _, o := range observations {
			key := o.Key + "\x00" + o.EnvironmentScope
			if seen[key] {
				return nil, uxv1.NewError(uxv1.CodeSafety, "CI variable inventory repeated an exact key and scope")
			}
			seen[key] = true
			all = append(all, o)
		}
		if len(observations) < 100 {
			return all, nil
		}
	}
	return nil, uxv1.NewError(uxv1.CodeSafety, "CI variable inventory exceeded the hard page limit; absence is unproven")
}
func findVariable(items []civariable.Observation, key, scope string) *civariable.Observation {
	for i := range items {
		if items[i].Key == key && items[i].EnvironmentScope == scope {
			return &items[i]
		}
	}
	return nil
}
func expectedVariable(p Parsed) civariable.Metadata {
	class := p.Values["--expected-class"]
	return civariable.Metadata{Key: p.Positionals[0], EnvironmentScope: p.Values["--scope"], VariableType: p.Values["--expected-type"], Class: class, Masked: class == "masked" || class == "hidden", Hidden: class == "hidden", Protected: p.Values["--expected-protected"] == "true", Raw: p.Values["--expected-raw"] == "true"}
}
func variablePreconditionObserved(p Parsed, o *civariable.Observation) bool {
	if p.Values["--expected-class"] == "absent" {
		return o == nil
	}
	if o == nil || o.Metadata != expectedVariable(p) {
		return false
	}
	if o.Hidden {
		return true
	}
	return o.MatchesExpected
}

func executeVariables(ctx context.Context, t Target, p Parsed, meta uxv1.Meta, deps Dependencies) (commandOutput, error) {
	fail := func(err error) (commandOutput, error) { return commandOutput{meta: meta}, err }
	action, group := p.Definition.Path[1], p.Definition.Path[0]
	scope := p.Values["--scope"]
	match := civariable.Comparison{Scope: scope}
	if action != "list" {
		match.Key = p.Positionals[0]
		if p.Values["--expected-value-file"] != "" {
			value, err := readVariableInput(ctx, p.Values["--expected-value-file"], nil)
			if err != nil {
				return fail(err)
			}
			if !civariable.ValidValue(value, false) {
				return fail(variableValidation("invalid bounded expected CI variable value"))
			}
			match.Expected = &value
		}
		if action == "set" {
			value, err := readVariableInput(ctx, p.Values["--value-file"], deps.Runtime.Stdin)
			if err != nil {
				return fail(err)
			}
			if !civariable.ValidValue(value, group == "secret") {
				return fail(variableValidation("invalid CI variable value; hidden values require at least eight characters and no whitespace"))
			}
			match.Desired = &value
		}
	}
	// The approved shared helper is opened once, after private input validation.
	// No official profile/delegate participates in any part of this operation.
	c, err := openNative(ctx, p, deps)
	if err != nil {
		return fail(err)
	}
	defer c.Close()
	meta.Backend = "native"
	meta.Host = c.Host().Name
	meta.UpstreamVersion = ""
	if action != "list" && p.Values["--expected-project-url"] != c.Host().Authority.ExpectedProjectURL(t.Repo) {
		return fail(uxv1.NewError(uxv1.CodeSafety, "expected project URL does not match the configured native authority"))
	}
	if err := requireVariableVersion(ctx, c); err != nil {
		return fail(err)
	}
	project, err := loadVariableProject(ctx, c, t.Repo)
	if err != nil {
		return fail(err)
	}
	if action != "list" && strconv.FormatInt(project.ID, 10) != p.Values["--expected-project-id"] {
		return fail(uxv1.NewError(uxv1.CodeSafety, "expected project ID does not match GitLab"))
	}
	inventory, err := variableInventory(ctx, c, project.ID, match)
	if err != nil {
		return fail(err)
	}
	if action == "list" {
		again, err := loadVariableProject(ctx, c, t.Repo)
		if err != nil {
			return fail(err)
		}
		if again != project {
			return fail(uxv1.NewError(uxv1.CodeSafety, "project identity changed during inventory"))
		}
		items := make([]civariable.Metadata, 0)
		for _, o := range inventory {
			if o.EnvironmentScope == scope && (group == "variable") == (o.Class == "ordinary") {
				items = append(items, o.Metadata)
			}
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			meta.Complete = false
			meta.Truncated = true
			meta.Reason = "display_limit"
		}
		meta.Count = len(items)
		return commandOutput{data: map[string]any{"variables": items, "values_disclosed": false}, meta: meta}, nil
	}
	before := findVariable(inventory, match.Key, scope)
	if !variablePreconditionObserved(p, before) {
		return fail(uxv1.NewError(uxv1.CodeConflict, "CI variable prestate does not match the exact metadata or readable-value guards"))
	}
	receipt := variableReceipt{Action: action, Outcome: "ambiguous", ProjectID: project.ID, ProjectURL: project.WebURL, Key: match.Key, EnvironmentScope: scope, Reconciliation: "not_observed", ValueVerification: "not_observed"}
	if p.Values["--expected-class"] == "hidden" || action == "set" && group == "secret" {
		receipt.ValueVerification = "unavailable_hidden"
	} else if action == "delete" {
		receipt.ValueVerification = "matched"
	}
	desired := civariable.Metadata{Key: match.Key, EnvironmentScope: scope, VariableType: p.Values["--type"], Masked: group == "secret", Hidden: group == "secret", Protected: p.Values["--protected"] == "true", Raw: true}
	desired.Class = desired.Classification()
	if action == "set" && !desired.Hidden && before != nil && before.Metadata == desired && before.MatchesDesired {
		receipt.Action = "unchanged"
		receipt.Outcome = "precondition_observed"
		receipt.State = &desired
		receipt.ValueVerification = "matched"
		return commandOutput{data: variableMutationOutput{receipt}, meta: meta}, nil
	}
	// GitLab has no CAS or immutable variable ID; value-only races and ABA remain.
	inventory, err = variableInventory(ctx, c, project.ID, match)
	if err != nil {
		return fail(err)
	}
	if !variablePreconditionObserved(p, findVariable(inventory, match.Key, scope)) {
		return fail(uxv1.NewError(uxv1.CodeConflict, "CI variable prestate changed before mutation"))
	}
	again, err := loadVariableProject(ctx, c, t.Repo)
	if err != nil {
		return fail(err)
	}
	if again != project {
		return fail(uxv1.NewError(uxv1.CodeSafety, "project identity changed before mutation"))
	}
	route := "projects/" + strconv.FormatInt(project.ID, 10) + "/variables"
	request := productnative.Request{Method: http.MethodDelete, Path: route + "/" + url.PathEscape(match.Key), Query: url.Values{"filter[environment_scope]": {scope}}, MaxBytes: limits.MaxJSONPageBytes}
	expectedStatus := http.StatusNoContent
	if action == "set" {
		request.Method = http.MethodPut
		expectedStatus = http.StatusOK
		payload := map[string]any{"value": *match.Desired, "variable_type": desired.VariableType, "protected": desired.Protected, "masked": desired.Masked, "raw": true}
		if before == nil {
			request.Method = http.MethodPost
			expectedStatus = http.StatusCreated
			request.Path = route
			request.Query = nil
			payload["key"] = match.Key
			payload["environment_scope"] = scope
			payload["masked_and_hidden"] = desired.Hidden
		}
		body, err := json.Marshal(payload)
		if err != nil {
			return fail(uxv1.NewError(uxv1.CodeInternal, "cannot encode private CI variable payload"))
		}
		request.Body = body
		defer clear(body)
		request.Headers = http.Header{"Content-Type": {"application/json"}}
	}
	if ctx.Err() != nil {
		return fail(uxv1.NewError(uxv1.CodeCanceled, "CI variable mutation canceled before dispatch"))
	}
	receipt.MutationAttempted = true
	response, writeErr := c.Do(ctx, request)
	receipt.ProviderAcknowledged = writeErr == nil && response.StatusCode == expectedStatus
	clear(response.Body) // Never use a value-bearing write echo as the receipt.
	clear(request.Body)
	// Observe the exact postcondition once, within the same identity and lifetime.
	reconcileCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	inventory, readErr := variableInventory(reconcileCtx, c, project.ID, match)
	if readErr == nil {
		again, readErr = loadVariableProject(reconcileCtx, c, t.Repo)
		if readErr == nil && again != project {
			readErr = uxv1.NewError(uxv1.CodeSafety, "project identity changed during reconciliation")
		}
	}
	if readErr == nil {
		after := findVariable(inventory, match.Key, scope)
		postcondition := false
		if action == "delete" && after == nil {
			receipt.Reconciliation = "absence_observed"
			postcondition = true
		} else if action == "set" && after != nil && after.Metadata == desired {
			receipt.State = &desired
			receipt.Reconciliation = "metadata_observed"
			if desired.Hidden {
				postcondition = true
			} else if after.MatchesDesired {
				receipt.Reconciliation = "value_and_metadata_observed"
				receipt.ValueVerification = "matched"
				postcondition = true
			}
		}
		if receipt.ProviderAcknowledged && postcondition {
			receipt.Outcome = "postcondition_observed"
			return commandOutput{data: variableMutationOutput{receipt}, meta: meta}, nil
		}
		if writeErr != nil && variablePreconditionObserved(p, after) {
			if rejection, ok := uxv1.NewHTTPRejection(uxv1.AsError(writeErr).StatusCode); ok {
				receipt.Outcome = "rejected"
				rejection.Retryable = false
				rejection.Receipt = variableMutationOutput{receipt}
				return fail(rejection)
			}
		}
	}
	ambiguous := uxv1.NewError(uxv1.CodeAmbiguousVariable, "CI variable mutation outcome is ambiguous; inspect the exact key and scope before any retry")
	ambiguous.Receipt = variableMutationOutput{receipt}
	return fail(ambiguous)
}
