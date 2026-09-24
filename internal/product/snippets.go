package product

import (
	"context"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/safeurl"
)

const snippetDetails = "Scope is required. Personal lists contain only the authenticated user's personal snippets; personal view may read another visible owner. Project scope requires explicit -R. All reads verify authentication, with no anonymous fallback.\nVisibility is public/internal/private, never GitHub secret/unlisted. Private means creator-only for personal snippets and members-only for project snippets.\nVisibility filtering is local over at most ten provider pages. Completeness describes only that selected scope/filter.\nSelectors accept canonical positive IDs or selected-host /-/snippets/ID URLs, prefixed with the project path for project scope.\nDefault view includes metadata and available file names, not file contents. --files omits description; --filename selects one exact inventory file at its reported root ref, never a raw URL.\nContent is UTF-8 text, default 32768 bytes, hard maximum 131072. Binary/unavailable content fails closed. Field truncation is explicit; --full and --raw are unsupported."

func snippetFlags(view bool) []FlagDefinition {
	flags := []FlagDefinition{
		{Name: "--scope", Value: "personal|project", Required: true, Description: "Explicit personal or project snippet scope."},
		{Name: "--fields", Value: "FIELD,...", Description: "Add description,created_at,updated_at; identity fields always remain."},
	}
	if !view {
		return append(flags, FlagDefinition{Name: "--visibility", Value: "public|internal|private", Description: "Exact local visibility filter, not secret/unlisted."})
	}
	return append(flags,
		FlagDefinition{Name: "--files", Boolean: true, Description: "File names and required metadata only; no description/content."},
		FlagDefinition{Name: "--filename", Value: "PATH", Description: "Read one exact file from the bound snippet inventory."},
		FlagDefinition{Name: "--content-limit", Value: "BYTES", Description: "UTF-8 content byte limit 0..131072, default 32768; requires --filename."},
	)
}

func validateSnippetParsed(p Parsed) error {
	scope, repo := p.Values["--scope"], p.Values["--repo"]
	if scope != "personal" && scope != "project" {
		return controlledMessage("--scope must be personal or project")
	}
	if scope == "personal" && repo != "" {
		return controlledMessage("personal snippets cannot select --repo")
	}
	if scope == "project" {
		if err := safeurl.ValidateProject(repo); err != nil {
			return controlledMessage("project snippets require explicit --repo namespace/project")
		}
	}
	if host := p.Values["--hostname"]; host != "" {
		if err := safeurl.ValidateHost(host); err != nil {
			return controlledMessage("invalid GitLab hostname")
		}
	}
	if v := p.Values["--visibility"]; v != "" && !snippetVisibility(v) {
		return controlledMessage("visibility must be public, internal, or private; GitLab has no secret/unlisted equivalent")
	}
	seen := map[string]bool{}
	if fields := p.Values["--fields"]; fields != "" {
		for _, field := range strings.Split(fields, ",") {
			if seen[field] || field != "description" && field != "created_at" && field != "updated_at" {
				return controlledMessage("--fields accepts unique description,created_at,updated_at only")
			}
			seen[field] = true
		}
	}
	if p.Booleans["--files"] && (p.Values["--filename"] != "" || p.Values["--fields"] != "") {
		return controlledMessage("--files cannot combine with --filename or --fields")
	}
	if name := p.Values["--filename"]; name != "" {
		if err := glab.ValidateSnippetFilename(name); err != nil {
			return err
		}
	}
	if raw := p.Values["--content-limit"]; raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || n > 131072 || p.Values["--filename"] == "" {
			return controlledMessage("--content-limit requires --filename and an integer 0..131072")
		}
	}
	if len(p.Positionals) != 0 {
		// Syntax and project binding are checked here; final host binding (including
		// environment/default host) is checked before constructing the delegate.
		host := p.Values["--hostname"]
		if strings.HasPrefix(p.Positionals[0], "https://") && host == "" {
			u, err := url.Parse(p.Positionals[0])
			if err == nil {
				host = u.Host
			}
		}
		if host == "" {
			host = "gitlab.com"
		}
		_, err := snippetSelector(p.Positionals[0], Target{Host: host, Repo: repo}, scope)
		return err
	}
	return nil
}

func snippetVisibility(v string) bool { return v == "public" || v == "internal" || v == "private" }

func snippetBasePath(repo string, id int64) string {
	prefix := ""
	if repo != "" {
		prefix = "/" + repo
	}
	return prefix + "/-/snippets/" + strconv.FormatInt(id, 10)
}

func snippetSelector(raw string, target Target, scope string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err == nil && id > 0 && strconv.FormatInt(id, 10) == raw {
		return id, nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != target.Host || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return 0, controlledMessage("snippet selector must be a canonical positive ID or an exact selected-host snippet URL")
	}
	last := u.Path[strings.LastIndex(u.Path, "/")+1:]
	id, err = strconv.ParseInt(last, 10, 64)
	if err != nil || id < 1 || strconv.FormatInt(id, 10) != last {
		return 0, controlledMessage("invalid snippet ID in URL")
	}
	repo := target.Repo
	if scope == "personal" {
		repo = ""
	}
	if u.Path == snippetBasePath(repo, id) {
		return id, nil
	}
	return 0, controlledMessage("snippet URL does not match the selected scope and project")
}

// snippetReadSession applies the aggregate bound to identity, page, content and
// recheck reads alike. The delegated child independently bounds each response.
type snippetReadSession struct {
	client  delegateClient
	target  Target
	bytes   int
	version string
}

func (s *snippetReadSession) read(ctx context.Context, r glab.Request) ([]byte, error) {
	r.Host, r.Repo = s.target.Host, s.target.Repo
	response, err := s.client.Do(ctx, r)
	s.version = response.UpstreamVersion
	if err != nil {
		return nil, err
	}
	s.bytes += len(response.Body)
	if len(response.Body) > limits.MaxJSONPageBytes || s.bytes > limits.MaxOperationBytes {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "snippet response exceeded the byte budget")
	}
	return response.Body, nil
}

type SnippetUser struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}
type snippetUserResponse struct {
	SnippetUser
	WebURL string `json:"web_url"`
}

func validSnippetUser(u SnippetUser) bool {
	return u.ID > 0 && u.Username != "" && len(u.Username) <= 256 && utf8.ValidString(u.Username) && !strings.ContainsFunc(u.Username, func(r rune) bool {
		return unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune("/\\%?#", r)
	}) && u.Username != "." && u.Username != ".."
}

func executeSnippet(ctx context.Context, client delegateClient, target Target, p Parsed, meta uxv1.Meta) (out commandOutput, err error) {
	session := &snippetReadSession{client: client, target: target}
	defer func() { out.meta.UpstreamVersion = session.version }()
	out.meta = meta
	body, err := session.read(ctx, glab.Request{Operation: glab.OpSnippetUser})
	if err != nil {
		return out, err
	}
	var user snippetUserResponse
	if err := decodeStrict(body, &user); err != nil {
		return out, err
	}
	if !validSnippetUser(user.SnippetUser) {
		return out, malformed("authenticated snippet user")
	}
	if user.WebURL != "" && user.WebURL != "https://"+target.Host+"/"+user.Username {
		return out, uxv1.NewError(uxv1.CodeSafety, "authenticated user URL is outside the selected authority")
	}
	var projectID int64
	if p.Values["--scope"] == "project" {
		body, err := session.read(ctx, glab.Request{Operation: glab.OpSnippetProject})
		if err != nil {
			return out, err
		}
		var project upstreamRepo
		if err := decodeStrict(body, &project); err != nil {
			return out, err
		}
		if project.ID < 1 || project.PathWithNamespace != target.Repo || project.WebURL != "https://"+target.Host+"/"+target.Repo {
			return out, uxv1.NewError(uxv1.CodeSafety, "snippet project identity does not match the selected project")
		}
		projectID = project.ID
	}
	if p.Definition.Path[1] == "list" {
		return executeSnippetList(ctx, session, p, projectID, user.SnippetUser, meta)
	}
	id, _ := snippetSelector(p.Positionals[0], target, p.Values["--scope"])
	request := glab.Request{Operation: glab.OpSnippetView, Scope: p.Values["--scope"], ID: id}
	body, err = session.read(ctx, request)
	if err != nil {
		return out, err
	}
	var raw upstreamSnippet
	if err := decodeStrict(body, &raw); err != nil {
		return out, err
	}
	snippet, truncated, err := normalizeSnippet(raw, target, projectID, p)
	if err != nil {
		return out, err
	}
	if snippet.ID != id {
		return out, uxv1.NewError(uxv1.CodeSafety, "provider returned a different snippet ID")
	}
	if p.Booleans["--files"] && !snippet.FilesAvailable {
		return out, uxv1.NewError(uxv1.CodeUnsupported, "snippet file inventory is unavailable")
	}
	if filename := p.Values["--filename"]; filename != "" {
		if raw.UpdatedAt == "" {
			return out, malformed("snippet updated_at for content read")
		}
		if !snippet.FilesAvailable {
			return out, uxv1.NewError(uxv1.CodeUnsupported, "snippet file inventory is unavailable")
		}
		ref := ""
		for _, file := range raw.Files {
			if file.Path == filename {
				ref, err = snippetFileRef(file, target, raw.ID)
				break
			}
		}
		if err != nil {
			return out, err
		}
		if ref == "" {
			return out, uxv1.NewError(uxv1.CodeNotFound, "selected snippet file is nonexistent or unavailable")
		}
		content, err := session.read(ctx, glab.Request{Operation: glab.OpSnippetFile, Scope: p.Values["--scope"], ID: id, Filename: filename, Ref: ref})
		if err != nil {
			return out, err
		}
		limit := 32768
		if v := p.Values["--content-limit"]; v != "" {
			limit, _ = strconv.Atoi(v)
		}
		text, cut, err := snippetText(string(content), limit)
		if err != nil {
			return out, err
		}
		truncated = truncated || cut
		snippet.Content = &SnippetContent{Filename: filename, Ref: ref, Text: text, Truncated: cut}
		// Mutable root refs are not immutable snapshots. Refuse if the provider's
		// metadata/inventory changes around the content read, never claim atomicity.
		body, err = session.read(ctx, request)
		if err != nil {
			return out, err
		}
		var after upstreamSnippet
		if err := decodeStrict(body, &after); err != nil {
			return out, err
		}
		if !reflect.DeepEqual(raw, after) {
			return out, uxv1.NewError(uxv1.CodeSafety, "snippet changed while reading the selected file; retry")
		}
	}
	out.data = map[string]any{"snippet": snippet, "authenticated_user": user.SnippetUser}
	out.meta.Truncated = truncated
	if truncated {
		out.meta.Reason = "field_limit"
	}
	return out, nil
}

func executeSnippetList(ctx context.Context, s *snippetReadSession, p Parsed, projectID int64, user SnippetUser, meta uxv1.Meta) (commandOutput, error) {
	items := make([]Snippet, 0)
	seen := map[int64]bool{}
	perPage := min(100, p.Limit+1)
	meta.Complete = false
	fieldCut := false
	for page := 1; page <= limits.MaxPages; page++ {
		body, err := s.read(ctx, glab.Request{Operation: glab.OpSnippetList, Scope: p.Values["--scope"], Page: page, PerPage: perPage})
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		var raw []upstreamSnippet
		if err := decodeStrict(body, &raw); err != nil {
			return commandOutput{meta: meta}, err
		}
		if raw == nil || len(raw) > perPage {
			return commandOutput{meta: meta}, malformed("snippet page")
		}
		for _, item := range raw {
			if item.ID < 1 || seen[item.ID] {
				return commandOutput{meta: meta}, malformed("duplicate or invalid snippet ID")
			}
			seen[item.ID] = true
			if projectID == 0 {
				if item.Author.ID != user.ID || item.Author.Username != user.Username {
					return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeSafety, "personal list returned a different owner")
				}
				// The authenticated author route includes project snippets too. Validate
				// their scope/URL before excluding them from the personal selection.
				if item.ProjectID != nil {
					otherTarget, err := snippetProjectTarget(item, s.target.Host)
					if err != nil {
						return commandOutput{meta: meta}, err
					}
					if _, _, err := normalizeSnippet(item, otherTarget, *item.ProjectID, p); err != nil {
						return commandOutput{meta: meta}, err
					}
					continue
				}
			}
			normalized, cut, err := normalizeSnippet(item, s.target, projectID, p)
			if err != nil {
				return commandOutput{meta: meta}, err
			}
			if v := p.Values["--visibility"]; v != "" && normalized.Visibility != v {
				continue
			}
			if len(items) <= p.Limit {
				items = append(items, normalized)
				fieldCut = fieldCut || cut
			}
		}
		if len(items) > p.Limit {
			items = items[:p.Limit]
			meta.Truncated, meta.Reason = true, "display_limit"
			break
		}
		if len(raw) < perPage {
			meta.Complete = true
			break
		}
		if page == limits.MaxPages {
			meta.Truncated, meta.Reason = true, "hard_page_limit"
		}
	}
	meta.Count = len(items)
	if fieldCut {
		meta.Truncated = true
		if meta.Reason == "" {
			meta.Reason = "field_limit"
		}
	}
	return commandOutput{data: map[string]any{"snippets": items, "scope": p.Values["--scope"], "authenticated_user": user}, meta: meta}, nil
}
