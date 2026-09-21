package product

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/safeurl"
)

type upstreamSnippetFile struct {
	Path   string `json:"path"`
	RawURL string `json:"raw_url"`
}
type upstreamSnippet struct {
	ID          int64                 `json:"id"`
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Visibility  string                `json:"visibility"`
	Author      snippetUserResponse   `json:"author"`
	ProjectID   *int64                `json:"project_id"`
	WebURL      string                `json:"web_url"`
	RawURL      string                `json:"raw_url"`
	Files       []upstreamSnippetFile `json:"files"`
	CreatedAt   string                `json:"created_at"`
	UpdatedAt   string                `json:"updated_at"`
}

// Scope evidence must be explicit: a missing project_id is not proof that a
// snippet is personal. GitLab represents personal scope with JSON null.
func (s *upstreamSnippet) UnmarshalJSON(body []byte) error {
	type plain upstreamSnippet
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		return err
	}
	if _, ok := fields["project_id"]; !ok {
		return errors.New("missing snippet project scope")
	}
	var decoded plain
	if err := json.Unmarshal(body, &decoded); err != nil {
		return err
	}
	*s = upstreamSnippet(decoded)
	return nil
}

type SnippetContent struct {
	Filename  string `json:"filename"`
	Ref       string `json:"ref"`
	Text      string `json:"text"`
	Truncated bool   `json:"truncated"`
}

type Snippet struct {
	ID             int64           `json:"id"`
	Title          string          `json:"title"`
	Scope          string          `json:"scope"`
	ProjectID      int64           `json:"project_id,omitempty"`
	Visibility     string          `json:"visibility"`
	Owner          SnippetUser     `json:"owner"`
	WebURL         string          `json:"web_url"`
	Files          []string        `json:"files"`
	FilesAvailable bool            `json:"files_available"`
	Description    *string         `json:"description,omitempty"`
	CreatedAt      string          `json:"created_at,omitempty"`
	UpdatedAt      string          `json:"updated_at,omitempty"`
	Content        *SnippetContent `json:"content,omitempty"`
}

// No inline truncation marker: even a zero/small byte limit is obeyed. The
// envelope and content.truncated communicate field truncation explicitly.
func snippetText(text string, limit int) (string, bool, error) {
	if !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return "", false, malformed("snippet UTF-8 text")
	}
	if len(text) <= limit {
		return text, false, nil
	}
	cut := limit
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut], true, nil
}

func normalizeSnippet(raw upstreamSnippet, target Target, projectID int64, p Parsed) (Snippet, bool, error) {
	bad := func() (Snippet, bool, error) { return Snippet{}, false, malformed("snippet metadata or scope") }
	if raw.ID < 1 || raw.Title == "" || !snippetVisibility(raw.Visibility) || !validSnippetUser(raw.Author.SnippetUser) {
		return bad()
	}
	if projectID == 0 && raw.ProjectID != nil || projectID != 0 && (raw.ProjectID == nil || *raw.ProjectID != projectID) {
		return Snippet{}, false, uxv1.NewError(uxv1.CodeSafety, "snippet project identity does not match selected scope")
	}
	scope := "personal"
	if projectID != 0 {
		scope = "project"
	}
	id, err := snippetSelector(raw.WebURL, target, scope)
	if !strings.HasPrefix(raw.WebURL, "https://") || err != nil || id != raw.ID {
		return Snippet{}, false, uxv1.NewError(uxv1.CodeSafety, "snippet URL does not match returned ID and selected scope")
	}
	if raw.Author.WebURL != "" && raw.Author.WebURL != "https://"+target.Host+"/"+raw.Author.Username {
		return Snippet{}, false, uxv1.NewError(uxv1.CodeSafety, "snippet owner URL is outside selected authority")
	}
	if raw.RawURL != "" {
		valid := false
		for _, base := range snippetBasePaths(target.Repo, raw.ID) {
			if raw.RawURL == "https://"+target.Host+base+"/raw" {
				valid = true
			}
		}
		if !valid {
			return Snippet{}, false, uxv1.NewError(uxv1.CodeSafety, "snippet raw URL is outside the exact snippet")
		}
	}
	title, truncated, err := snippetText(raw.Title, 4096)
	if err != nil {
		return Snippet{}, false, err
	}
	out := Snippet{ID: raw.ID, Title: title, Scope: scope, ProjectID: projectID, Visibility: raw.Visibility, Owner: raw.Author.SnippetUser, WebURL: raw.WebURL, Files: []string{}, FilesAvailable: raw.Files != nil}
	// A missing file inventory is not the same as an empty snippet repository.
	if len(raw.Files) > 100 {
		return bad()
	}
	seen := map[string]bool{}
	for _, f := range raw.Files {
		if glab.ValidateSnippetFilename(f.Path) != nil || seen[f.Path] {
			return bad()
		}
		seen[f.Path] = true
		if f.RawURL != "" {
			if _, err := snippetFileRef(f, target, raw.ID); err != nil {
				return Snippet{}, false, err
			}
		}
		out.Files = append(out.Files, f.Path)
	}
	fields := "," + p.Values["--fields"] + ","
	if strings.Contains(fields, ",description,") || p.Definition.Path[1] == "view" && !p.Booleans["--files"] {
		text, cut, err := snippetText(raw.Description, 32768)
		if err != nil {
			return Snippet{}, false, err
		}
		out.Description, truncated = &text, truncated || cut
	}
	for _, pair := range []struct {
		name, value string
		dest        *string
	}{{"created_at", raw.CreatedAt, &out.CreatedAt}, {"updated_at", raw.UpdatedAt, &out.UpdatedAt}} {
		if pair.value != "" {
			if _, err := time.Parse(time.RFC3339Nano, pair.value); err != nil {
				return bad()
			}
		}
		if strings.Contains(fields, ","+pair.name+",") {
			if pair.value == "" {
				return bad()
			}
			*pair.dest = pair.value
		}
	}
	return out, truncated, nil
}

// A returned file URL supplies a root ref, not network authority. Validate its
// full decoded path against known snippet and filename boundaries, then build
// a different, closed API route using the validated ref and exact filename.
func snippetFileRef(f upstreamSnippetFile, target Target, id int64) (string, error) {
	invalid := func() (string, error) {
		return "", uxv1.NewError(uxv1.CodeSafety, "snippet file URL does not match host, scope, ID, ref and filename")
	}
	u, err := url.Parse(f.RawURL)
	if err != nil || u.Scheme != "https" || u.Host != target.Host || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return invalid()
	}
	if err := glab.ValidateSnippetFilename(f.Path); err != nil {
		return invalid()
	}
	for _, base := range snippetBasePaths(target.Repo, id) {
		prefix, suffix := base+"/raw/", "/"+f.Path
		if !strings.HasPrefix(u.Path, prefix) || !strings.HasSuffix(u.Path, suffix) {
			continue
		}
		middle := strings.TrimSuffix(strings.TrimPrefix(u.Path, prefix), suffix)
		if err := safeurl.ValidateBranch(middle); err != nil {
			return invalid()
		}
		// Prevent alternative encodings from disguising path separators/traversal.
		expected := (&url.URL{Scheme: "https", Host: target.Host, Path: base + "/raw/" + middle + suffix}).String()
		if f.RawURL != expected {
			return invalid()
		}
		return middle, nil
	}
	return invalid()
}

func snippetProjectTarget(raw upstreamSnippet, host string) (Target, error) {
	fail := func() (Target, error) {
		return Target{}, uxv1.NewError(uxv1.CodeSafety, "personal list returned an invalid project snippet URL")
	}
	if raw.ProjectID == nil || *raw.ProjectID < 1 {
		return fail()
	}
	u, err := url.Parse(raw.WebURL)
	if err != nil || u.Host != host || u.Scheme != "https" {
		return fail()
	}
	suffix := "/snippets/" + strconv.FormatInt(raw.ID, 10)
	if !strings.HasSuffix(u.Path, suffix) {
		return fail()
	}
	repo := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSuffix(u.Path, suffix), "/-"), "/")
	if err := safeurl.ValidateProject(repo); err != nil {
		return fail()
	}
	return Target{Host: host, Repo: repo}, nil
}
