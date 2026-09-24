package product

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/safeurl"
)

const searchDetails = "Query text is GitLab-native, not a GitHub qualifier parser. State and created sorting are the only mapped search filters.\nLabels, assignee, author, review, draft, stars, other sorts and code language are unsupported, not silently ignored.\nCommit/code search remains project-only. Host/group code and commit search require additional advanced-search/tier contracts.\nDisabled search, tier restrictions and upstream errors fail closed; there is no fallback to another scope.\nRepository created sorting keeps the same search route and query. It requires the complete result set within the existing page/byte/deadline bounds, then sorts timestamps before applying the display limit; otherwise it refuses.\nRepository language/user filters use project discovery search; language means uses a language, not primary language."

func discoveryFlags() []FlagDefinition {
	return []FlagDefinition{
		{Name: "--group", Value: "FULL_PATH", Description: "Exact group namespace, including nested paths; excludes shared projects."},
		{Name: "--include-subgroups", Boolean: true, Description: "Include descendant namespaces; requires --group."},
		{Name: "--visibility", Value: "public|internal|private", Description: "GitLab visibility."},
		{Name: "--archived", Boolean: true, Description: "Archived projects only."},
		{Name: "--language", Value: "LANGUAGE", Description: "Uses programming language, not primary language; host/user discovery only."},
	}
}

func searchFlags(kind string) []FlagDefinition {
	if kind == "code" || kind == "commits" {
		return nil
	}
	flags := []FlagDefinition{
		{Name: "--group", Value: "FULL_PATH", Description: "Search within an exact GitLab group and descendants."},
		{Name: "--sort", Value: "created", Description: "Order by created_at descending; other GitHub sorts are unsupported."},
	}
	if kind == "repos" {
		return append(flags,
			FlagDefinition{Name: "--owner", Value: "USER", Description: "User-owned projects only, not a group."},
			FlagDefinition{Name: "--language", Value: "LANGUAGE", Description: "Uses programming language, not primary language; host/user only. Uses project discovery search."})
	}
	return append(flags,
		FlagDefinition{Name: "--scope", Value: "project|host", Description: "Default project; host explicitly ignores checkout context. Mutually exclusive with --group."},
		FlagDefinition{Name: "--state", Value: "STATE", Description: "opened, closed, all; MRs also accept merged."})
}

func discoverySelection(p Parsed) glab.DiscoverySelectors {
	s := glab.DiscoverySelectors{Owner: p.Values["--owner"], Group: p.Values["--group"], Visibility: p.Values["--visibility"], Language: p.Values["--language"], IncludeSubgroups: p.Booleans["--include-subgroups"]}
	if strings.Join(p.Definition.Path, " ") == "repo list" && len(p.Positionals) == 1 {
		s.Owner = p.Positionals[0]
	}
	if p.Booleans["--archived"] {
		s.Archived = "true"
	}
	return s
}

func searchSelection(p Parsed) glab.SearchSelectors {
	return glab.SearchSelectors{Area: p.Values["--scope"], Group: p.Values["--group"], State: p.Values["--state"], Sort: p.Values["--sort"]}
}

func validateDiscoveryParsed(p Parsed) error {
	path := strings.Join(p.Definition.Path, " ")
	if path != "repo list" && path != "repo view" && !strings.HasPrefix(path, "search ") {
		return nil
	}
	if host := p.Values["--hostname"]; host != "" && safeurl.ValidateHost(host) != nil {
		return uxv1.NewError(uxv1.CodeValidation, "invalid GitLab hostname")
	}
	if repo := p.Values["--repo"]; repo != "" && safeurl.ValidateProject(repo) != nil {
		return uxv1.NewError(uxv1.CodeValidation, "invalid repository target")
	}
	if path == "repo view" {
		return nil
	}
	if err := discoverySelection(p).Validate(); err != nil {
		return err
	}
	if strings.HasPrefix(path, "search ") {
		if len(p.Positionals[0]) > 1024 || strings.TrimSpace(p.Positionals[0]) == "" {
			return uxv1.NewError(uxv1.CodeValidation, "search query must contain text and be at most 1024 bytes")
		}
		if err := searchSelection(p).Validate(p.Definition.Path[1]); err != nil {
			return err
		}
		if (p.Values["--scope"] == "host" || p.Values["--group"] != "") && p.Values["--repo"] != "" {
			return uxv1.NewError(uxv1.CodeValidation, "host/group search cannot also select a repository")
		}
	}
	return nil
}

func discoveryGroup(ctx context.Context, client delegateClient, host, group string) (int64, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpDiscoveryGroup, Host: host, Discovery: glab.DiscoverySelectors{Group: group}})
	if err != nil {
		return 0, err
	}
	var source struct {
		ID       int64  `json:"id"`
		FullPath string `json:"full_path"`
		WebURL   string `json:"web_url"`
	}
	if err := decodeStrict(response.Body, &source); err != nil {
		return 0, err
	}
	if source.ID < 1 || source.FullPath != group || !exactDiscoveryURL(source.WebURL, host, "/groups/"+group) {
		return 0, uxv1.NewError(uxv1.CodeSafety, "provider returned a different group identity")
	}
	return source.ID, nil
}

func discoveryProject(ctx context.Context, client delegateClient, target Target, id int64) (upstreamRepo, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpDiscoveryProject, Host: target.Host, Repo: target.Repo, ID: id})
	if err != nil {
		return upstreamRepo{}, err
	}
	var source upstreamRepo
	if err := decodeStrict(response.Body, &source); err != nil {
		return source, err
	}
	if source.ID < 1 || id > 0 && source.ID != id || target.Repo != "" && source.PathWithNamespace != target.Repo {
		return source, uxv1.NewError(uxv1.CodeSafety, "provider returned a different project identity")
	}
	if err := exactDiscoveryRepo(source, target.Host); err != nil {
		return source, err
	}
	return source, nil
}

func exactDiscoveryRepo(source upstreamRepo, host string) error {
	if source.ID < 1 || safeurl.ValidateProject(source.PathWithNamespace) != nil || !exactDiscoveryURL(source.WebURL, host, "/"+source.PathWithNamespace) {
		return uxv1.NewError(uxv1.CodeSafety, "provider returned a noncanonical repository identity")
	}
	return nil
}

func exactDiscoveryURL(raw, host, path string) bool {
	parsed, err := url.Parse(raw)
	return err == nil && strings.EqualFold(parsed.Host, host) && raw == "https://"+parsed.Host+path
}

func normalizeDiscoveryRepos(body []byte, host string, selectors glab.DiscoverySelectors, groupID int64) ([]Repository, bool, error) {
	var source []upstreamRepo
	if err := decodeStrict(body, &source); err != nil {
		return nil, false, err
	}
	if source == nil {
		return nil, false, malformed("repository list")
	}
	out := make([]Repository, 0, len(source))
	truncated := false
	for _, item := range source {
		if err := exactDiscoveryRepo(item, host); err != nil {
			return nil, false, err
		}
		namespace, _, _ := strings.Cut(item.PathWithNamespace, "/")
		if index := strings.LastIndex(item.PathWithNamespace, "/"); index >= 0 {
			namespace = item.PathWithNamespace[:index]
		}
		if selectors.Owner != "" && (namespace != selectors.Owner || item.Namespace.Kind != "user" || item.Namespace.FullPath != selectors.Owner || item.Namespace.ID < 1) {
			return nil, false, uxv1.NewError(uxv1.CodeSafety, "provider returned a different user owner")
		}
		if selectors.Group != "" {
			inGroup := namespace == selectors.Group || selectors.IncludeSubgroups && strings.HasPrefix(namespace, selectors.Group+"/")
			if !inGroup || item.Namespace.Kind != "group" || item.Namespace.FullPath != namespace || item.Namespace.ID < 1 || namespace == selectors.Group && item.Namespace.ID != groupID {
				return nil, false, uxv1.NewError(uxv1.CodeSafety, "provider returned a different group owner")
			}
		}
		if selectors.Visibility != "" && item.Visibility != selectors.Visibility || selectors.Archived != "" && (item.Archived == nil || strconv.FormatBool(*item.Archived) != selectors.Archived) {
			return nil, false, uxv1.NewError(uxv1.CodeSafety, "provider returned a repository outside selected filters")
		}
		repo, cut, err := normalizeRepo(item, host)
		if err != nil {
			return nil, false, err
		}
		truncated = truncated || cut
		out = append(out, repo)
	}
	return out, truncated, nil
}

func normalizeSelectedRepo(body []byte, target Target) (Repository, bool, error) {
	var source upstreamRepo
	if err := decodeStrict(body, &source); err != nil {
		return Repository{}, false, err
	}
	if source.PathWithNamespace != target.Repo {
		return Repository{}, false, uxv1.NewError(uxv1.CodeSafety, "provider returned a different project identity")
	}
	if err := exactDiscoveryRepo(source, target.Host); err != nil {
		return Repository{}, false, err
	}
	return normalizeRepo(source, target.Host)
}

// Count all paginated bodies and project/group identity lookups together. The
// delegate bounds each child; this bounds the entire discovery operation.
type discoveryReadClient struct {
	delegateClient
	bytes int
}

func (c *discoveryReadClient) Do(ctx context.Context, request glab.Request) (glab.Response, error) {
	if err := ctx.Err(); err != nil {
		return glab.Response{}, uxv1.Wrap(uxv1.CodeCanceled, "discovery canceled", err)
	}
	response, err := c.delegateClient.Do(ctx, request)
	if err != nil {
		return response, err
	}
	c.bytes += len(response.Body)
	if len(response.Body) > limits.MaxJSONPageBytes || c.bytes > limits.MaxOperationBytes {
		return glab.Response{UpstreamVersion: response.UpstreamVersion}, uxv1.NewError(uxv1.CodeSafety, "discovery response exceeded its byte limit")
	}
	return response, nil
}

func fetchDiscoveryRepos(ctx context.Context, client delegateClient, target Target, p Parsed) ([]Repository, listState, error) {
	client = &discoveryReadClient{delegateClient: client}
	s := discoverySelection(p)
	groupID := int64(0)
	if s.Group != "" {
		var err error
		groupID, err = discoveryGroup(ctx, client, target.Host, s.Group)
		if err != nil {
			return nil, listState{}, err
		}
	}
	op := glab.OpRepoDiscovery
	// Keep the official profile's historical unfiltered default unchanged.
	if s == (glab.DiscoverySelectors{}) {
		op = glab.OpRepoList
	}
	return fetchList(ctx, client, glab.Request{Operation: op, Host: target.Host, Discovery: s}, p.Limit, func(body []byte) ([]Repository, bool, error) {
		return normalizeDiscoveryRepos(body, target.Host, s, groupID)
	})
}

func fetchScopedSearch(ctx context.Context, client delegateClient, target Target, p Parsed) ([]map[string]any, listState, error) {
	client = &discoveryReadClient{delegateClient: client}
	s := searchSelection(p)
	kind := p.Definition.Path[1]
	if s.Group != "" {
		if _, err := discoveryGroup(ctx, client, target.Host, s.Group); err != nil {
			return nil, listState{}, err
		}
	}
	request := glab.Request{Operation: glab.OpSearch, Host: target.Host, Repo: target.Repo, Scope: kind, Query: p.Positionals[0], Search: s}
	// The project discovery endpoint is the pinned language/user filter route.
	if kind == "repos" && (p.Values["--owner"] != "" || p.Values["--language"] != "") {
		request.Operation, request.Discovery = glab.OpRepoDiscovery, discoverySelection(p)
	}
	projects := map[int64]string{}
	if kind != "repos" && target.Repo != "" {
		project, err := discoveryProject(ctx, client, target, 0)
		if err != nil {
			return nil, listState{}, err
		}
		projects[project.ID] = project.PathWithNamespace
	}
	createdOrder := kind == "repos" && s.Sort == "created"
	pageWidth := min(100, p.Limit+1)
	if createdOrder {
		// GitLab's project-list routes rewrite created_at ordering to ID.
		// Keep native matching and collect the entire bounded candidate set;
		// no provider date-order promise or sort of a truncated page is safe.
		request.Search.Sort = ""
		pageWidth = 100
	}
	normalize := func(body []byte) ([]map[string]any, bool, error) {
		var source []map[string]any
		if err := decodeStrict(body, &source); err != nil {
			return nil, false, err
		}
		if source == nil {
			return nil, false, malformed("search result list")
		}
		if len(source) > pageWidth {
			return nil, false, malformed("search page size")
		}
		out := make([]map[string]any, 0, len(source))
		truncated := false
		for _, item := range source {
			repo := target.Repo
			if kind == "repos" {
				path, ok := item["path_with_namespace"].(string)
				if !ok || safeurl.ValidateProject(path) != nil {
					return nil, false, malformed("search repository identity")
				}
				repo = path
				if _, ok := positiveJSONNumber(item["id"]); !ok {
					return nil, false, malformed("search repository ID")
				}
				if s.Group != "" && !strings.HasPrefix(repo, s.Group+"/") {
					return nil, false, uxv1.NewError(uxv1.CodeSafety, "search result is outside selected group")
				}
				if p.Values["--owner"] != "" {
					encoded, _ := json.Marshal([]map[string]any{item})
					if _, _, err := normalizeDiscoveryRepos(encoded, target.Host, request.Discovery, 0); err != nil {
						return nil, false, err
					}
				}
			} else {
				number, ok := positiveJSONNumber(item["project_id"])
				if !ok {
					return nil, false, malformed("search project ID")
				}
				id, _ := number.Int64()
				if known, ok := projects[id]; ok {
					repo = known
				} else {
					if target.Repo != "" {
						return nil, false, uxv1.NewError(uxv1.CodeSafety, "search returned a different project")
					}
					project, err := discoveryProject(ctx, client, Target{Host: target.Host}, id)
					if err != nil {
						return nil, false, err
					}
					repo = project.PathWithNamespace
					projects[id] = repo
				}
				if s.Group != "" && !strings.HasPrefix(repo, s.Group+"/") {
					return nil, false, uxv1.NewError(uxv1.CodeSafety, "search result is outside selected group")
				}
			}
			if kind == "issues" || kind == "mrs" || kind == "repos" {
				web, ok := item["web_url"].(string)
				want := "/" + repo
				if kind != "repos" {
					iid, ok := positiveJSONNumber(item["iid"])
					if !ok {
						return nil, false, malformed("search IID")
					}
					resource := "issues"
					if kind == "mrs" {
						resource = "merge_requests"
					}
					want += "/-/" + resource + "/" + iid.String()
				}
				if !ok || !exactDiscoveryURL(web, target.Host, want) {
					return nil, false, uxv1.NewError(uxv1.CodeSafety, "search returned a different resource URL")
				}
				if s.State != "" && s.State != "all" && item["state"] != s.State {
					return nil, false, uxv1.NewError(uxv1.CodeSafety, "search returned a different state")
				}
			}
			// Preserve the established closed search-result output shape.
			encoded, _ := json.Marshal([]map[string]any{item})
			items, cut, err := normalizeSearch(encoded, kind, target.Host, repo)
			if err != nil {
				return nil, false, err
			}
			truncated = truncated || cut
			out = append(out, items...)
		}
		return out, truncated, nil
	}
	if createdOrder {
		return fetchCreatedRepoSearch(ctx, client, request, p.Limit, normalize)
	}
	return fetchList(ctx, client, request, p.Limit, normalize)
}

// Creation order cannot be inferred from provider IDs, nor from a limited
// prefix of results. Refuse when the complete set cannot be proved within the
// normal operation bounds. Timestamp metadata is consumed, not added to output.
func fetchCreatedRepoSearch(ctx context.Context, client delegateClient, request glab.Request, displayLimit int, normalize func([]byte) ([]map[string]any, bool, error)) ([]map[string]any, listState, error) {
	type candidate struct {
		data    map[string]any
		id      int64
		created time.Time
	}
	seen := make(map[int64]bool)
	candidates, state, err := fetchList(ctx, client, request, limits.MaxPages*100, func(body []byte) ([]candidate, bool, error) {
		items, cut, err := normalize(body)
		if err != nil {
			return nil, false, err
		}
		var dates []struct {
			ID        int64  `json:"id"`
			CreatedAt string `json:"created_at"`
		}
		if err := decodeStrict(body, &dates); err != nil {
			return nil, false, err
		}
		out := make([]candidate, 0, len(items))
		for i, item := range items {
			stamp := dates[i]
			created, err := time.Parse(time.RFC3339Nano, stamp.CreatedAt)
			if err != nil || len(stamp.CreatedAt) > 64 || stamp.ID < 1 || seen[stamp.ID] {
				return nil, false, malformed("repository creation-order evidence")
			}
			seen[stamp.ID] = true
			out = append(out, candidate{data: item, id: stamp.ID, created: created})
		}
		return out, cut, nil
	})
	state.count = min(state.count, displayLimit)
	if err != nil {
		return nil, state, err
	}
	if !state.complete {
		return nil, state, uxv1.NewError(uxv1.CodeSafety, "repository creation order requires complete search results within the hard page, byte, and deadline limits")
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].created.Equal(candidates[j].created) {
			return candidates[i].id > candidates[j].id
		}
		return candidates[i].created.After(candidates[j].created)
	})
	if len(candidates) > displayLimit {
		candidates = candidates[:displayLimit]
		state.complete, state.truncated, state.reason = false, true, "display_limit"
	}
	out := make([]map[string]any, 0, len(candidates))
	for _, item := range candidates {
		out = append(out, item.data)
	}
	state.count = len(out)
	return out, state, nil
}
