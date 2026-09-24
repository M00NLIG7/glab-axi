package product

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
)

type discoveryStep struct {
	argv string
	body any
	fail bool
}
type discoveryOwnershipPage struct {
	owned, accessible []any
}
type discoveryCase struct {
	name      string
	args      []string
	steps     []discoveryStep
	failure   bool
	reason    string
	count     int
	firstID   int64
	errorCode uxv1.Code
}

func TestDiscoveryExecutableContracts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX protocol fixture")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	const api = "api --method GET --hostname gitlab.com "
	project := discoveryRepo("team/sub/project", "group")
	group := map[string]any{"id": 17, "full_path": "team/sub", "web_url": "https://gitlab.com/groups/team/sub"}
	preflight := discoveryStep{api + "projects/team%2Fsub%2Fproject", project, false}
	groupflight := discoveryStep{api + "groups/team%2Fsub?with_projects=false", group, false}
	repos := func(item map[string]any) []any { return []any{item} }
	issue := func(kind, state string) map[string]any {
		resource := "issues"
		if kind == "mrs" {
			resource = "merge_requests"
		}
		return map[string]any{"id": 123, "iid": 7, "project_id": 42, "title": "result", "state": state, "web_url": "https://gitlab.com/team/sub/project/-/" + resource + "/7"}
	}
	tests := []discoveryCase{
		{name: "default", args: []string{"repo", "list"}, steps: []discoveryStep{{"repo list --output json --page 1 --per-page 31", repos(project), false}}, count: 1},
		{name: "view", args: []string{"repo", "view", "team/sub/project"}, steps: []discoveryStep{{"repo view team/sub/project --output json", project, false}}},
		{name: "group", args: []string{"repo", "list", "--group", "team/sub"}, steps: []discoveryStep{groupflight, {api + "groups/team%2Fsub/projects?include_subgroups=false&page=1&per_page=31&with_shared=false", repos(project), false}}, count: 1},
		{name: "subgroups", args: []string{"repo", "list", "--group", "team/sub", "--include-subgroups"}, steps: []discoveryStep{groupflight, {api + "groups/team%2Fsub/projects?include_subgroups=true&page=1&per_page=31&with_shared=false", repos(discoveryRepo("team/sub/child/project", "group")), false}}, count: 1},
		{name: "positional-owner", args: []string{"repo", "list", "alice"}, steps: []discoveryStep{{api + "users/alice/projects?page=1&per_page=31", repos(discoveryRepo("alice/project", "user")), false}}, count: 1},
		{name: "language", args: []string{"repo", "list", "--language", "C++"}, steps: []discoveryStep{{api + "projects?owned=true&page=1&per_page=31&with_programming_language=C%2B%2B", repos(project), false}}, count: 1},
		{name: "owner-language", args: []string{"repo", "list", "alice", "--language", "Go"}, steps: []discoveryStep{{api + "users/alice/projects?page=1&per_page=31&with_programming_language=Go", repos(discoveryRepo("alice/project", "user")), false}}, count: 1},
		{name: "search-repos-short-unsorted", args: []string{"search", "repos", "go"}, steps: []discoveryStep{{api + "search?page=1&per_page=31&scope=projects&search=go", repos(project), false}}, count: 1},
		{name: "search-repos", args: []string{"search", "repos", "cli"}, steps: []discoveryStep{{api + "search?page=1&per_page=31&scope=projects&search=cli", repos(project), false}}, count: 1},
		{name: "search-group-repos", args: []string{"search", "repos", "cli", "--group", "team/sub", "--sort", "created"}, steps: []discoveryStep{groupflight, {api + "groups/team%2Fsub/search?page=1&per_page=100&scope=projects&search=cli", repos(project), false}}, count: 1},
		{name: "search-language", args: []string{"search", "repos", "cli", "--language", "Go", "--sort", "created"}, steps: []discoveryStep{{api + "projects?page=1&per_page=100&search=cli&with_programming_language=Go", repos(project), false}}, count: 1},
		{name: "search-owner", args: []string{"search", "repos", "cli", "--owner", "alice"}, steps: []discoveryStep{{api + "users/alice/projects?page=1&per_page=31&search=cli", repos(discoveryRepo("alice/project", "user")), false}}, count: 1},
	}
	for _, command := range [][]string{{"repo", "list", "0xalice"}, {"search", "repos", "cli", "--owner", "0xalice"}} {
		query := "page=1&per_page=31"
		if command[0] == "search" {
			query += "&search=cli"
		}
		tests = append(tests, discoveryCase{name: command[0] + "-digit-leading-owner", args: command, steps: []discoveryStep{{api + "users/0xalice/projects?" + query, repos(discoveryRepo("0xalice/project", "user")), false}}, count: 1})
	}
	for _, language := range []string{"Ren'Py", "F*", "C&sort=asc"} {
		for _, command := range [][]string{{"repo", "list"}, {"search", "repos", "cli"}} {
			query := "page=1&per_page=31"
			if command[0] == "search" {
				query += "&search=cli"
			} else {
				query = "owned=true&" + query
			}
			query += "&with_programming_language=" + url.QueryEscape(language)
			tests = append(tests, discoveryCase{name: command[0] + "-language-" + language, args: append(command, "--language", language), steps: []discoveryStep{{api + "projects?" + query, repos(project), false}}, count: 1})
		}
	}
	for _, area := range []string{"host", "group"} {
		for _, query := range []string{`"go cli"`, `tools "go cli"`, `"go cli" "a b"`, `go`, `go cli`, `"go" cli`, `""`, `界`} {
			args := []string{"search", "repos", query, "--sort", "created"}
			endpoint := "search?page=1&per_page=100&scope=projects&search=" + url.QueryEscape(query)
			var steps []discoveryStep
			if area == "group" {
				args = append(args, "--group", "team/sub")
				endpoint = "groups/team%2Fsub/search?page=1&per_page=100&scope=projects&search=" + url.QueryEscape(query)
				steps = append(steps, groupflight)
			}
			steps = append(steps, discoveryStep{api + endpoint, repos(project), false})
			tests = append(tests, discoveryCase{name: "search-quoted-" + area + "-" + query, args: args, steps: steps, count: 1})
		}
	}
	for _, area := range []string{"host", "group", "owner", "language", "owner-language"} {
		args := []string{"search", "repos", "project", "--sort", "created"}
		prefix, suffix := "search?", "&scope=projects&search=project"
		var steps []discoveryStep
		user := area == "owner" || area == "owner-language"
		switch area {
		case "group":
			args = append(args, "--group", "team/sub")
			prefix = "groups/team%2Fsub/search?"
			steps = append(steps, groupflight)
		case "owner", "owner-language":
			args = append(args, "--owner", "alice")
			prefix, suffix = "users/alice/projects?", "&search=project"
		case "language":
			prefix, suffix = "projects?", "&search=project"
		}
		if area == "language" || area == "owner-language" {
			args = append(args, "--language", "Go")
			suffix += "&with_programming_language=Go"
		}
		page := make([]any, 100)
		for i := range page {
			p := discoveryRepo(fmt.Sprintf("team/sub/child/p%d", i), "group")
			if user {
				p = discoveryRepo(fmt.Sprintf("alice/p%d", i), "user")
			}
			p["id"] = i + 1
			p["created_at"] = fmt.Sprintf("2026-09-21T00:%02d:%02dZ", (99-i)/60, (99-i)%60)
			page[i] = p
		}
		// GitLab v18.3.0 API::Helpers#order_options_with_tie_breaker
		// rewrites created_at to id. Model that provider behavior, not the
		// requested ordering: imported projects can have opposing ID/date order.
		for i, j := 0, len(page)-1; i < j; i, j = i+1, j-1 {
			page[i], page[j] = page[j], page[i]
		}
		steps = append(steps, discoveryStep{api + prefix + "page=1&per_page=100" + suffix, page, false}, discoveryStep{api + prefix + "page=2&per_page=100" + suffix, []any{}, false})
		tests = append(tests, discoveryCase{name: "search-created-limit-" + area, args: append(append([]string{}, args...), "--limit", "1"), steps: append([]discoveryStep{}, steps...), count: 1, firstID: 1, reason: "display_limit"})
		tests = append(tests, discoveryCase{name: "search-created-pages-" + area, args: append(args, "--limit", "101"), steps: steps, count: 100, firstID: 1})
	}
	createdArgs := []string{"search", "repos", "project", "--sort", "created", "--limit", "1"}
	createdEndpoint := api + "search?page=1&per_page=100&scope=projects&search=project"
	for _, variant := range []string{"aligned-ids", "timezone", "equal-time", "missing-time", "bad-time", "duplicate-id"} {
		first, second := discoveryRepo("team/sub/older", "group"), discoveryRepo("team/sub/newer", "group")
		first["id"], second["id"] = 20, 10
		first["created_at"], second["created_at"] = "2026-09-21T12:00:00+02:00", "2026-09-21T11:30:00Z"
		wantID := int64(10)
		switch variant {
		case "aligned-ids":
			first["created_at"] = "2026-09-21T12:00:00Z"
			wantID = 20
		case "equal-time":
			first["created_at"] = second["created_at"]
			wantID = 20
		case "missing-time":
			delete(second, "created_at")
		case "bad-time":
			second["created_at"] = "not-a-date"
		case "duplicate-id":
			second["id"] = first["id"]
		}
		failed := variant == "missing-time" || variant == "bad-time" || variant == "duplicate-id"
		test := discoveryCase{name: "search-created-" + variant, args: createdArgs, steps: []discoveryStep{{createdEndpoint, []any{first, second}, false}}, count: 1, firstID: wantID, reason: "display_limit"}
		if failed {
			test.failure, test.reason, test.errorCode = true, "", uxv1.CodeUpstream
		}
		tests = append(tests, test)
	}
	for _, large := range []bool{false, true} {
		pages := 10
		name, reason := "search-created-incomplete", "hard_page_limit"
		if large {
			pages, name, reason = 5, "search-created-byte-bound", ""
		}
		var steps []discoveryStep
		for page := 1; page <= pages; page++ {
			body := make([]any, 100)
			for i := range body {
				id := (page-1)*100 + i + 1
				item := discoveryRepo(fmt.Sprintf("team/sub/project%d", id), "group")
				item["id"] = id
				if large {
					item["description"] = strings.Repeat("x", 17000)
				}
				body[i] = item
			}
			steps = append(steps, discoveryStep{api + fmt.Sprintf("search?page=%d&per_page=100&scope=projects&search=project", page), body, false})
		}
		tests = append(tests, discoveryCase{name: name, args: createdArgs, steps: steps, failure: true, reason: reason, errorCode: uxv1.CodeSafety})
	}
	tests = append(tests,
		discoveryCase{name: "search-created-empty", args: createdArgs, steps: []discoveryStep{{createdEndpoint, []any{}, false}}},
		discoveryCase{name: "search-created-null", args: createdArgs, steps: []discoveryStep{{createdEndpoint, nil, false}}, failure: true, errorCode: uxv1.CodeUpstream},
		discoveryCase{name: "search-created-disabled", args: createdArgs, steps: []discoveryStep{{createdEndpoint, "HTTP 403: search disabled", true}}, failure: true, errorCode: uxv1.CodeForbidden},
	)
	for _, visibility := range []string{"public", "internal", "private"} {
		p := discoveryRepo("team/sub/project", "group")
		p["visibility"] = visibility
		tests = append(tests, discoveryCase{name: "visibility-" + visibility, args: []string{"repo", "list", "--visibility", visibility}, steps: []discoveryStep{{api + "projects?owned=true&page=1&per_page=31&visibility=" + visibility, repos(p), false}}, count: 1})
	}
	archived := discoveryRepo("team/sub/project", "group")
	archived["archived"] = true
	tests = append(tests, discoveryCase{name: "archived", args: []string{"repo", "list", "--archived"}, steps: []discoveryStep{{api + "projects?archived=true&owned=true&page=1&per_page=31", repos(archived), false}}, count: 1})
	for _, filter := range []struct {
		name  string
		args  []string
		query url.Values
	}{
		{"visibility", []string{"--visibility", "public"}, url.Values{"visibility": {"public"}}},
		{"archived", []string{"--archived"}, url.Values{"archived": {"true"}}},
		{"language", []string{"--language", "Go"}, url.Values{"with_programming_language": {"Go"}}},
	} {
		var owned, accessible []any
		for i, owner := range []string{"bob", "bob", "alice", "alice"} {
			p := discoveryRepo(fmt.Sprintf("%s/project%d", owner, i), "user")
			p["id"] = i + 1
			p["visibility"] = "public"
			p["archived"] = true
			accessible = append(accessible, p)
			if owner == "alice" {
				owned = append(owned, p)
			}
		}
		filter.query.Set("owned", "true")
		filter.query.Set("page", "1")
		filter.query.Set("per_page", "2")
		tests = append(tests, discoveryCase{name: "default-owner-limit-" + filter.name, args: append([]string{"repo", "list", "--limit", "1"}, filter.args...), steps: []discoveryStep{{api + "projects?" + filter.query.Encode(), discoveryOwnershipPage{owned: owned[:2], accessible: accessible[:2]}, false}}, count: 1, firstID: 3, reason: "display_limit"})
	}
	for _, kind := range []string{"issues", "mrs"} {
		provider := kind
		if kind == "mrs" {
			provider = "merge_requests"
		}
		states := []string{"opened", "closed", "all"}
		if kind == "mrs" {
			states = append(states, "merged")
		}
		for _, state := range states {
			returnedState := state
			if state == "all" {
				returnedState = "opened"
			}
			tests = append(tests, discoveryCase{name: kind + "-state-" + state, args: []string{"search", kind, "bug fix", "-R", "team/sub/project", "--scope", "project", "--state", state, "--sort", "created"}, steps: []discoveryStep{preflight, {api + "projects/team%2Fsub%2Fproject/search?order_by=created_at&page=1&per_page=31&scope=" + provider + "&search=bug+fix&sort=desc&state=" + state, repos(issue(kind, returnedState)), false}}, count: 1})
		}
		for _, area := range []string{"host", "group"} {
			args := []string{"search", kind, "bug"}
			prefix := "search"
			steps := []discoveryStep{}
			if area == "host" {
				args = append(args, "--scope", "host")
			} else {
				args = append(args, "--group", "team/sub")
				prefix = "groups/team%2Fsub/search"
				steps = append(steps, groupflight)
			}
			steps = append(steps, discoveryStep{api + prefix + "?page=1&per_page=31&scope=" + provider + "&search=bug", repos(issue(kind, "opened")), false}, discoveryStep{api + "projects/42", project, false})
			tests = append(tests, discoveryCase{name: kind + "-" + area, args: args, steps: steps, count: 1})
		}
	}
	for _, kind := range []string{"commits", "code"} {
		provider := kind
		item := map[string]any{"id": strings.Repeat("a", 40), "project_id": 42, "title": "commit"}
		if kind == "code" {
			provider = "blobs"
			item = map[string]any{"project_id": 42, "path": "file.go", "data": strings.Repeat("a", 17000)}
		}
		reason := ""
		if kind == "code" {
			reason = "field_limit"
		}
		tests = append(tests, discoveryCase{name: kind, args: []string{"search", kind, "text", "-R", "team/sub/project"}, steps: []discoveryStep{preflight, {api + "projects/team%2Fsub%2Fproject/search?page=1&per_page=31&scope=" + provider + "&search=text", repos(item), false}}, count: 1, reason: reason})
	}
	// A second page must retain every filter, the stable width, and owner scope.
	page := make([]any, 100)
	for i := range page {
		p := discoveryRepo(fmt.Sprintf("alice/p%d", i), "user")
		p["id"] = i + 1
		p["archived"] = true
		page[i] = p
	}
	tests = append(tests, discoveryCase{name: "pagination", args: []string{"repo", "list", "alice", "--archived", "--visibility", "private", "--language", "Go", "--limit", "101"}, steps: []discoveryStep{{api + "users/alice/projects?archived=true&page=1&per_page=100&visibility=private&with_programming_language=Go", page, false}, {api + "users/alice/projects?archived=true&page=2&per_page=100&visibility=private&with_programming_language=Go", []any{}, false}}, count: 100})
	searchPage := make([]any, 100)
	for i := range searchPage {
		searchPage[i] = issue("issues", "opened")
	}
	tests = append(tests, discoveryCase{name: "search-pagination", args: []string{"search", "issues", "bug", "-R", "team/sub/project", "--state", "opened", "--sort", "created", "--limit", "101"}, steps: []discoveryStep{preflight, {api + "projects/team%2Fsub%2Fproject/search?order_by=created_at&page=1&per_page=100&scope=issues&search=bug&sort=desc&state=opened", searchPage, false}, {api + "projects/team%2Fsub%2Fproject/search?order_by=created_at&page=2&per_page=100&scope=issues&search=bug&sort=desc&state=opened", []any{}, false}}, count: 100})
	tests = append(tests, discoveryCase{name: "display-limit", args: []string{"repo", "list", "--limit", "1"}, steps: []discoveryStep{{"repo list --output json --page 1 --per-page 2", []any{project, project}, false}}, count: 1, reason: "display_limit"})
	steps := []discoveryStep{}
	for i := 1; i <= 10; i++ {
		steps = append(steps, discoveryStep{fmt.Sprintf("repo list --output json --page %d --per-page 100", i), page, false})
	}
	tests = append(tests, discoveryCase{name: "hard-pages", args: []string{"repo", "list", "--limit", "1000"}, steps: steps, count: 1000, reason: "hard_page_limit"})
	// Identity and upstream error cases must not emit successful partial data.
	for _, mutation := range []string{"host", "group", "owner-kind", "owner-path", "visibility", "archive", "missing-archive", "web-suffix"} {
		p := discoveryRepo("alice/project", "user")
		args := []string{"repo", "list", "alice"}
		endpoint := api + "users/alice/projects?page=1&per_page=31"
		switch mutation {
		case "host":
			p["web_url"] = "https://evil.example/alice/project"
		case "group":
			p = discoveryRepo("elsewhere/project", "group")
		case "owner-kind":
			p["namespace"].(map[string]any)["kind"] = "group"
		case "owner-path":
			p["namespace"].(map[string]any)["full_path"] = "bob"
		case "visibility":
			args = append(args, "--visibility", "public")
			endpoint += "&visibility=public"
		case "missing-archive":
			delete(p, "archived")
			args = append(args, "--archived")
			endpoint = api + "users/alice/projects?archived=true&page=1&per_page=31"
		case "archive":
			args = append(args, "--archived")
			endpoint = api + "users/alice/projects?archived=true&page=1&per_page=31"
		case "web-suffix":
			p["web_url"] = "https://gitlab.com/alice/project/-/issues"
		}
		tests = append(tests, discoveryCase{name: "reject-" + mutation, args: args, steps: []discoveryStep{{endpoint, repos(p), false}}, failure: true})
	}
	oversized := discoveryRepo("team/sub/project", "group")
	oversized["description"] = strings.Repeat("x", 2<<20)
	tests = append(tests, discoveryCase{name: "page-byte-bound", args: []string{"repo", "list"}, steps: []discoveryStep{{"repo list --output json --page 1 --per-page 31", repos(oversized), false}}, failure: true})
	largePage := make([]any, 100)
	for i := range largePage {
		item := discoveryRepo("team/sub/project", "group")
		item["description"] = strings.Repeat("x", 17000)
		largePage[i] = item
	}
	largeSteps := []discoveryStep{}
	for i := 1; i <= 5; i++ {
		largeSteps = append(largeSteps, discoveryStep{fmt.Sprintf("repo list --output json --page %d --per-page 100", i), largePage, false})
	}
	tests = append(tests, discoveryCase{name: "operation-byte-bound", args: []string{"repo", "list", "--limit", "1000"}, steps: largeSteps, failure: true})
	outside := issue("issues", "opened")
	tests = append(tests, discoveryCase{name: "search-wrong-group", args: []string{"search", "issues", "bug", "--group", "team/sub"}, steps: []discoveryStep{groupflight, {api + "groups/team%2Fsub/search?page=1&per_page=31&scope=issues&search=bug", repos(outside), false}, {api + "projects/42", discoveryRepo("other/project", "group"), false}}, failure: true})
	tests = append(tests, discoveryCase{name: "wrong-project-preflight", args: []string{"search", "code", "x", "-R", "team/sub/project"}, steps: []discoveryStep{{preflight.argv, discoveryRepo("other/project", "group"), false}}, failure: true})
	badGroup := map[string]any{"id": 17, "full_path": "team/other", "web_url": "https://gitlab.com/groups/team/other"}
	tests = append(tests, discoveryCase{name: "wrong-group-preflight", args: []string{"repo", "list", "--group", "team/sub"}, steps: []discoveryStep{{groupflight.argv, badGroup, false}}, failure: true}, discoveryCase{name: "wrong-view-project", args: []string{"repo", "view", "team/sub/project"}, steps: []discoveryStep{{"repo view team/sub/project --output json", discoveryRepo("wrong/project", "group"), false}}, failure: true})
	for _, mutation := range []string{"project-id", "host", "iid", "state", "missing-url"} {
		item := issue("issues", "opened")
		switch mutation {
		case "project-id":
			item["project_id"] = 99
		case "host":
			item["web_url"] = "https://evil.example/team/sub/project/-/issues/7"
		case "iid":
			item["iid"] = 8
		case "state":
			item["state"] = "closed"
		case "missing-url":
			delete(item, "web_url")
		}
		tests = append(tests, discoveryCase{name: "search-reject-" + mutation, args: []string{"search", "issues", "bug", "-R", "team/sub/project", "--state", "opened"}, steps: []discoveryStep{preflight, {api + "projects/team%2Fsub%2Fproject/search?page=1&per_page=31&scope=issues&search=bug&state=opened", repos(item), false}}, failure: true})
	}
	for _, message := range []string{"HTTP 403: search disabled or tier unavailable", "HTTP 429: rate limited", "HTTP 500: upstream failed"} {
		tests = append(tests, discoveryCase{name: message, args: []string{"search", "issues", "bug", "--scope", "host"}, steps: []discoveryStep{{api + "search?page=1&per_page=31&scope=issues&search=bug", message, true}}, failure: true})
	}
	tests = append(tests, discoveryCase{name: "dashboard", args: []string{"-R", "team/sub/project"}, steps: []discoveryStep{{"repo view team/sub/project --output json", project, false}, {"issue list --output json --page 1 --per-page 6 -R team/sub/project", []any{}, false}, {"mr list --output json --page 1 --per-page 6 -R team/sub/project", []any{}, false}, {"ci list --output json --page 1 --per-page 6 -R team/sub/project", []any{}, false}}})
	for _, test := range tests {
		switch test.name {
		case "default", "view", "group", "search-repos", "issues-host", "mrs-group", "dashboard":
		default:
			continue
		}
		for _, uppercaseInput := range []bool{false, true} {
			variant := test
			variant.name += fmt.Sprintf("-host-case-%t", uppercaseInput)
			variant.args = append([]string{}, test.args...)
			variant.steps = append([]discoveryStep{}, test.steps...)
			if uppercaseInput {
				variant.args = append(variant.args, "--hostname", "GITLAB.COM")
			}
			for i := range variant.steps {
				if uppercaseInput {
					variant.steps[i].argv = strings.ReplaceAll(variant.steps[i].argv, "--hostname gitlab.com", "--hostname GITLAB.COM")
				} else {
					body, err := json.Marshal(variant.steps[i].body)
					if err != nil {
						t.Fatal(err)
					}
					if err := json.Unmarshal([]byte(strings.ReplaceAll(string(body), "https://gitlab.com/", "https://GITLAB.COM/")), &variant.steps[i].body); err != nil {
						t.Fatal(err)
					}
				}
			}
			tests = append(tests, variant)
		}
	}
	invalid := [][]string{
		{"repo", "list", "--owner", "alice"}, {"repo", "list", "alice", "--owner", "alice"}, {"repo", "list", "alice", "--group", "team"}, {"repo", "list", "team/sub"}, {"repo", "list", "@me"}, {"repo", "list", "123"}, {"search", "repos", "cli", "--owner", "123"}, {"repo", "list", "--group", "../escape"}, {"repo", "list", "--group", "123"}, {"repo", "list", "--group", "team", "--language", "Go"}, {"repo", "list", "--include-subgroups"}, {"repo", "list", "--active"}, {"repo", "list", "--archived=true"}, {"repo", "list", "--visibility", "secret"}, {"repo", "list", "--fields", "clone_urls"}, {"repo", "view", "team/sub/project", "--fields", "clone_urls"}, {"repo", "list", "--language", strings.Repeat("x", 65)}, {"repo", "list", "--limit", "1001"}, {"repo", "list", "--limit", "0"},
		{"search", "issues", "x", "--state", "merged"}, {"search", "issues", "x", "--state", "open"}, {"search", "mrs", "x", "--state", "opened", "--state", "closed"}, {"search", "issues", "x", "--scope", "host", "-R", "team/project"}, {"search", "issues", "x", "--group", "team", "-R", "team/project"}, {"search", "issues", "x", "--group", "team", "--scope", "host"}, {"search", "issues", "x", "--scope", "global"}, {"search", "repos", "x", "--owner", "alice", "--group", "team"}, {"search", "issues", strings.Repeat("x", 1025)}, {"search", "issues", " "}, {"search", "issues", "x", "--hostname", "https://gitlab.com"},
		{"search", "issues", "x", "--label", "bug"}, {"search", "issues", "x", "--assignee", "alice"}, {"search", "issues", "x", "--author", "alice"}, {"search", "mrs", "x", "--draft"}, {"search", "mrs", "x", "--review", "approved"}, {"search", "issues", "x", "--sort", "updated"}, {"search", "repos", "x", "--stars", ">100"}, {"search", "code", "x", "--language", "Go"}, {"search", "code", "x", "--group", "team"}, {"search", "commits", "x", "--author", "alice"}, {"search", "commits", "x", "--scope", "host"},
	}
	for _, language := range []string{" F*", "F* ", "F\t*", "Ren'Py\n", strings.Repeat("x", 65)} {
		invalid = append(invalid, []string{"repo", "list", "--language", language}, []string{"search", "repos", "cli", "--language", language})
	}
	for i, args := range invalid {
		tests = append(tests, discoveryCase{name: fmt.Sprintf("invalid-%d", i), args: args, failure: true})
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, program)
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, output)
			}
			for _, test := range tests {
				t.Run(test.name, func(t *testing.T) { runDiscoveryCase(t, binary, test) })
			}
		})
	}
}

func runDiscoveryCase(t *testing.T, binary string, test discoveryCase) {
	t.Helper()
	dir := t.TempDir()
	for i, step := range test.steps {
		n := strconv.Itoa(i + 1)
		body, _ := json.Marshal(step.body)
		if page, ok := step.body.(discoveryOwnershipPage); ok {
			body, _ = json.Marshal(page.owned)
			unowned, _ := json.Marshal(page.accessible)
			if err := os.WriteFile(filepath.Join(dir, "unowned.body."+n), unowned, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "unowned.argv."+n), []byte(strings.Replace(step.argv, "owned=true&", "", 1)), 0600); err != nil {
				t.Fatal(err)
			}
		}
		if step.fail {
			body = []byte(step.body.(string))
			if err := os.WriteFile(filepath.Join(dir, "fail."+n), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(dir, "body."+n), body, 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "argv."+n), []byte(step.argv), 0600); err != nil {
			t.Fatal(err)
		}
	}
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$FIXTURE/record"
if [ "$*" = version ]; then printf 'glab 1.112.0 (816e3a52)\n'; exit 0; fi
n=0
if [ -f "$FIXTURE/count" ]; then n=$(cat "$FIXTURE/count"); fi
n=$((n + 1))
printf '%s' "$n" > "$FIXTURE/count"
if [ -f "$FIXTURE/unowned.argv.$n" ] && [ "$*" = "$(cat "$FIXTURE/unowned.argv.$n")" ]; then
  cat "$FIXTURE/unowned.body.$n"
  exit 0
fi
[ "$*" = "$(cat "$FIXTURE/argv.$n")" ] || { printf 'unexpected argv\n' >&2; exit 91; }
if [ -f "$FIXTURE/fail.$n" ]; then cat "$FIXTURE/body.$n" >&2; exit 1; fi
cat "$FIXTURE/body.$n"
`
	if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	args := append(append([]string{}, test.args...), "--hostname", "gitlab.com", "--format", "json")
	// Invalid explicit hosts are already part of the command under test.
	for _, arg := range test.args {
		if arg == "--hostname" {
			args = append(append([]string{}, test.args...), "--format", "json")
			break
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.WaitDelay = 2 * time.Second
	cmd.Dir = dir
	secret := strings.Join([]string{"synthetic", "discovery", "sentinel"}, "-")
	cmd.Env = []string{"GOMAXPROCS=2", "HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin", "FIXTURE=" + dir, "GITLAB_TOKEN=" + secret, "GLAB_CONFIG_DIR=" + filepath.Join(dir, "config")}
	output, err := cmd.CombinedOutput()
	if (err != nil) != test.failure {
		t.Fatalf("err=%v output=%s", err, output)
	}
	var env struct {
		OK    bool                       `json:"ok"`
		Data  map[string]json.RawMessage `json:"data"`
		Meta  uxv1.Meta                  `json:"meta"`
		Error struct {
			Code uxv1.Code `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(output, &env); err != nil {
		t.Fatalf("envelope: %v: %s", err, output)
	}
	if test.failure && (len(env.Data) != 0 || test.errorCode != "" && env.Error.Code != test.errorCode || test.reason != "" && env.Meta.Reason != test.reason) {
		t.Fatalf("refusal evidence: %s", output)
	}
	if env.OK == test.failure || test.failure && env.Meta.Complete {
		t.Fatalf("truthfulness: %s", output)
	}
	if !test.failure && (env.Meta.Count != test.count || env.Meta.Reason != test.reason || env.Meta.Truncated != (test.reason != "") || env.Meta.Complete != (test.reason != "display_limit" && test.reason != "hard_page_limit")) {
		t.Fatalf("bounds: %s", output)
	}
	if !test.failure {
		var repositories []map[string]any
		if body, ok := env.Data["repositories"]; ok {
			if err := json.Unmarshal(body, &repositories); err != nil {
				t.Fatal(err)
			}
		} else if body, ok := env.Data["repository"]; ok {
			var repository map[string]any
			if err := json.Unmarshal(body, &repository); err != nil {
				t.Fatal(err)
			}
			repositories = append(repositories, repository)
		}
		for _, repository := range repositories {
			for _, field := range []string{"http_url_to_repo", "ssh_url_to_repo"} {
				if _, ok := repository[field]; ok {
					t.Fatalf("unexpected repository field %s: %s", field, output)
				}
			}
		}
	}
	if !test.failure && test.firstID != 0 {
		var results []struct {
			ID int64 `json:"id"`
		}
		body := env.Data["results"]
		if repositories, ok := env.Data["repositories"]; ok {
			body = repositories
		}
		if err := json.Unmarshal(body, &results); err != nil || len(results) == 0 || results[0].ID != test.firstID {
			t.Fatalf("result order: %s", output)
		}
	}
	record, readErr := os.ReadFile(filepath.Join(dir, "record"))
	if len(test.steps) == 0 {
		if !os.IsNotExist(readErr) {
			t.Fatalf("invalid selector started child: %s", record)
		}
	} else {
		want := []string{"version"}
		for _, step := range test.steps {
			want = append(want, step.argv)
		}
		if readErr != nil || strings.TrimSpace(string(record)) != strings.Join(want, "\n") {
			t.Fatalf("requests: err=%v got=%s want=%v output=%s", readErr, record, want, output)
		}
	}
	if strings.Contains(string(record), secret) || strings.Contains(string(output), secret) {
		t.Fatal("synthetic credential leak")
	}
}
