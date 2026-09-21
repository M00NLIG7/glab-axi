package product

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	runtimepkg "gl-axi/internal/runtime"
)

func snippetFixture(id int64, project bool) upstreamSnippet {
	repo := ""
	var pid *int64
	if project {
		repo = "group/project"
		n := int64(101)
		pid = &n
	}
	base := "https://gitlab.com" + snippetBasePath(repo, id)
	return upstreamSnippet{ID: id, Title: "example", Description: "description", Visibility: "private", Author: snippetUserResponse{SnippetUser: SnippetUser{ID: 7, Username: "reader"}}, ProjectID: pid, WebURL: base, RawURL: base + "/raw", Files: []upstreamSnippetFile{{Path: "dir/a b.txt", RawURL: base + "/raw/main/dir/a%20b.txt"}}, CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-02T00:00:00Z"}
}
func snippetJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
func snippetFake() *fakeDelegate {
	return &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpSnippetUser:    {{Body: []byte(`{"id":7,"username":"reader","web_url":"https://gitlab.com/reader"}`), UpstreamVersion: glab.SupportedVersion}},
		glab.OpSnippetProject: {{Body: []byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`)}},
	}}
}

type snippetEnvelope struct {
	OK   bool `json:"ok"`
	Data struct {
		Snippet  Snippet     `json:"snippet"`
		Snippets []Snippet   `json:"snippets"`
		Scope    string      `json:"scope"`
		User     SnippetUser `json:"authenticated_user"`
	} `json:"data"`
	Meta  uxv1.Meta   `json:"meta"`
	Error *uxv1.Error `json:"error"`
}

func runSnippetTest(t *testing.T, f *fakeDelegate, args ...string) (snippetEnvelope, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	deps := Dependencies{Runtime: runtimepkg.Dependencies{Cwd: t.TempDir(), Stdout: &stdout, Stderr: &stderr, LookupEnv: func(string) (string, bool) { return "", false }}, NewDelegate: func() delegateClient { return f }}
	code := Run(context.Background(), append(args, "--format", "json"), deps)
	var result snippetEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode %v: %s stderr=%s", err, stdout.String(), stderr.String())
	}
	return result, code
}

var snippetFilenameCases = []struct {
	name, rawPath, content string
}{
	{":username.txt", ":username.txt", "literal colon filename\n"},
	{"reader.txt", "reader.txt", "different reader file\n"},
	{"dir/prefix:username.txt", "dir/prefix:username.txt", "nested colon filename\n"},
	{"notes(1).md", "notes(1).md", "parenthesized notes\n"},
	{"dir/!$&'()*+,;=:@ 世.txt", "dir/!$&'()*+,;=:@%20%E4%B8%96.txt", "punctuation and Unicode\n"},
}

func TestSnippetProviderFilenameEncoding(t *testing.T) {
	for _, scope := range []string{"personal", "project"} {
		for _, mode := range []string{"list", "view", "files", "content"} {
			for _, file := range snippetFilenameCases {
				t.Run(scope+"/"+mode+"/"+file.name, func(t *testing.T) {
					f := snippetFake()
					item := snippetFixture(42, scope == "project")
					item.Files = []upstreamSnippetFile{{Path: file.name, RawURL: item.WebURL + "/raw/main/" + file.rawPath}}
					f.responses[glab.OpSnippetList] = []glab.Response{{Body: snippetJSON([]upstreamSnippet{item})}}
					f.responses[glab.OpSnippetView] = []glab.Response{{Body: snippetJSON(item)}, {Body: snippetJSON(item)}}
					f.responses[glab.OpSnippetFile] = []glab.Response{{Body: []byte(file.content)}}
					args := []string{"snippet", "view", "42", "--scope", scope}
					if mode == "list" {
						args = []string{"snippet", "list", "--scope", scope}
					}
					if scope == "project" {
						args = append(args, "-R", "group/project")
					}
					if mode == "files" {
						args = append(args, "--files")
					}
					if mode == "content" {
						args = append(args, "--filename", file.name)
					}
					e, code := runSnippetTest(t, f, args...)
					if code != 0 || !e.OK || !e.Meta.Complete {
						t.Fatalf("code=%d result=%+v", code, e)
					}
					s := e.Data.Snippet
					if mode == "list" {
						if len(e.Data.Snippets) != 1 {
							t.Fatalf("snippets=%+v", e.Data.Snippets)
						}
						s = e.Data.Snippets[0]
					}
					if len(s.Files) != 1 || s.Files[0] != file.name {
						t.Fatalf("files=%v", s.Files)
					}
					if mode == "content" && (s.Content == nil || s.Content.Filename != file.name || s.Content.Text != file.content || s.Content.Ref != "main" || s.Content.Truncated) {
						t.Fatalf("content=%+v", s.Content)
					}
				})
			}
		}
	}
}

func TestSnippetRejectsNoncanonicalProviderURLs(t *testing.T) {
	for _, scope := range []string{"personal", "project"} {
		for name, change := range map[string]func(*upstreamSnippet){
			"legacy web": func(s *upstreamSnippet) { s.WebURL = strings.Replace(s.WebURL, "/-/snippets/", "/snippets/", 1) },
			"legacy raw": func(s *upstreamSnippet) { s.RawURL = strings.Replace(s.RawURL, "/-/snippets/", "/snippets/", 1) },
			"legacy file": func(s *upstreamSnippet) {
				s.Files[0].RawURL = strings.Replace(s.Files[0].RawURL, "/-/snippets/", "/snippets/", 1)
			},
			"file host": func(s *upstreamSnippet) {
				s.Files[0].RawURL = strings.Replace(s.Files[0].RawURL, "gitlab.com", "evil.example", 1)
			},
			"file scope": func(s *upstreamSnippet) {
				s.Files[0].RawURL = "https://gitlab.com/other/project/-/snippets/42/raw/main/dir/a%20b.txt"
			},
			"encoded slash": func(s *upstreamSnippet) { s.Files[0].RawURL = strings.Replace(s.Files[0].RawURL, "dir/", "dir%2F", 1) },
			"encoded boundary": func(s *upstreamSnippet) {
				s.Files[0].RawURL = strings.Replace(s.Files[0].RawURL, "/raw/main/", "/raw/main%2F", 1)
			},
			"encoded traversal": func(s *upstreamSnippet) { s.Files[0].RawURL = s.WebURL + "/raw/main/%2E%2E/dir/a%20b.txt" },
			"double encoding":   func(s *upstreamSnippet) { s.Files[0].RawURL = strings.Replace(s.Files[0].RawURL, "%20", "%2520", 1) },
			"encoded name": func(s *upstreamSnippet) {
				s.Files[0].RawURL = strings.Replace(s.Files[0].RawURL, "a%20b", "%61%20b", 1)
			},
			"fragment": func(s *upstreamSnippet) { s.Files[0].RawURL += "#fragment" },
			"query":    func(s *upstreamSnippet) { s.Files[0].RawURL += "?" },
		} {
			for _, mode := range []string{"list", "view"} {
				t.Run(scope+"/"+mode+"/"+name, func(t *testing.T) {
					f := snippetFake()
					item := snippetFixture(42, scope == "project")
					change(&item)
					f.responses[glab.OpSnippetList] = []glab.Response{{Body: snippetJSON([]upstreamSnippet{item})}}
					f.responses[glab.OpSnippetView] = []glab.Response{{Body: snippetJSON(item)}}
					args := []string{"snippet", "list", "--scope", scope}
					if mode == "view" {
						args = []string{"snippet", "view", "42", "--scope", scope, "--filename", "dir/a b.txt"}
					}
					wantRequests := 2
					if scope == "project" {
						args = append(args, "-R", "group/project")
						wantRequests++
					}
					e, code := runSnippetTest(t, f, args...)
					if code == 0 || e.OK || e.Meta.Complete || e.Error == nil || e.Error.Code != uxv1.CodeSafety || len(f.requests) != wantRequests {
						t.Fatalf("code=%d result=%+v requests=%v", code, e, f.requests)
					}
				})
			}
		}
	}
}

func TestSnippetInvalidInputDoesNoDelegateWork(t *testing.T) {
	cases := [][]string{
		{"list"}, {"list", "--scope", "all"}, {"list", "--scope", "personal", "-R", "group/project"}, {"list", "--scope", "project"},
		{"list", "--scope", "project", "-R", "https://evil/a"}, {"list", "--scope", "personal", "--visibility", "secret"},
		{"list", "--scope", "personal", "--secret"}, {"list", "--scope", "personal", "--fields", "owner"},
		{"list", "--scope", "personal", "--fields", "description,description"}, {"list", "--scope", "personal", "--fields", "description,"},
		{"list", "--scope", "personal", "--scope", "personal"}, {"list", "--scope", "personal", "--limit", "1001"},
		{"view", "0", "--scope", "personal"}, {"view", "01", "--scope", "personal"}, {"view", "-1", "--scope", "personal"},
		{"view", "1", "--scope", "personal", "--filename", "../a"}, {"view", "1", "--scope", "personal", "--filename", "a%2Fb"},
		{"view", "1", "--scope", "personal", "--filename", "a", "--files"}, {"view", "1", "--scope", "personal", "--files", "--fields", "description"},
		{"view", "1", "--scope", "personal", "--content-limit", "1"}, {"view", "1", "--scope", "personal", "--filename", "a", "--content-limit", "131073"},
		{"view", "1", "--scope", "personal", "--filename", "a", "--content-limit", "-1"}, {"view", "1", "--scope", "personal", "--full"}, {"view", "1", "--scope", "personal", "--raw"},
		{"view", "https://evil.example/-/snippets/1", "--scope", "personal"},
		{"view", "https://gitlab.com/snippets/1", "--scope", "personal"},
		{"view", "https://gitlab.com/group/project/snippets/1", "--scope", "project", "-R", "group/project"},
		{"view", "https://gitlab.com/group/other/-/snippets/1", "--scope", "project", "-R", "group/project"},
		{"view", "https://gitlab.com/group/project/-/snippets/1", "--scope", "personal"},
		{"view", "https://gitlab.com/-/snippets/1?raw=1", "--scope", "personal"},
		{"view", "https://gitlab.com/-/snippets/1#file", "--scope", "personal"},
		{"view", "https://gitlab.com/-/snippets/%31", "--scope", "personal"},
		{"view", "1", "--scope", "personal", "--hostname", "https://gitlab.com"},
		{"create", "--scope", "personal"}, {"clone", "1", "--scope", "personal"},
	}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			f := snippetFake()
			e, code := runSnippetTest(t, f, append([]string{"snippet"}, args...)...)
			if code == 0 || e.OK || len(f.requests) != 0 {
				t.Fatalf("code=%d response=%+v requests=%v", code, e, f.requests)
			}
		})
	}
}

func TestSnippetPersonalListRejectsLegacyProjectURLs(t *testing.T) {
	f := snippetFake()
	item := snippetFixture(42, true)
	item.WebURL = strings.Replace(item.WebURL, "/-/snippets/", "/snippets/", 1)
	f.responses[glab.OpSnippetList] = []glab.Response{{Body: snippetJSON([]upstreamSnippet{item})}}
	e, code := runSnippetTest(t, f, "snippet", "list", "--scope", "personal")
	if code == 0 || e.OK || e.Meta.Complete || e.Error == nil || e.Error.Code != uxv1.CodeSafety || len(f.requests) != 2 {
		t.Fatalf("code=%d result=%+v requests=%v", code, e, f.requests)
	}
}

func TestSnippetListScopesVisibilityAndFields(t *testing.T) {
	for _, scope := range []string{"personal", "project"} {
		for _, visibility := range []string{"public", "internal", "private"} {
			t.Run(scope+visibility, func(t *testing.T) {
				f := snippetFake()
				item := snippetFixture(42, scope == "project")
				item.Visibility = visibility
				other := snippetFixture(43, scope == "project")
				other.Visibility = "public"
				if visibility == "public" {
					other.Visibility = "private"
				}
				f.responses[glab.OpSnippetList] = []glab.Response{{Body: snippetJSON([]upstreamSnippet{item, other})}}
				args := []string{"snippet", "list", "--scope", scope, "--visibility", visibility, "--fields", "description,created_at,updated_at"}
				if scope == "project" {
					args = append(args, "-R", "group/project")
				}
				e, code := runSnippetTest(t, f, args...)
				if code != 0 || !e.OK || !e.Meta.Complete || e.Meta.Count != 1 || e.Data.Scope != scope || e.Data.User.ID != 7 || len(e.Data.Snippets) != 1 || e.Data.Snippets[0].Visibility != visibility || e.Data.Snippets[0].Description == nil || e.Data.Snippets[0].UpdatedAt == "" || e.Data.Snippets[0].CreatedAt == "" {
					t.Fatalf("code=%d result=%+v", code, e)
				}
			})
		}
	}
}

func TestSnippetViewSelectorsAndContent(t *testing.T) {
	for _, project := range []bool{false, true} {
		for _, selector := range []string{"id", "url"} {
			for _, mode := range []string{"default", "files", "content"} {
				t.Run(fmt.Sprintf("%v/%s/%s", project, selector, mode), func(t *testing.T) {
					f := snippetFake()
					item := snippetFixture(42, project)
					f.responses[glab.OpSnippetView] = []glab.Response{{Body: snippetJSON(item)}, {Body: snippetJSON(item)}}
					f.responses[glab.OpSnippetFile] = []glab.Response{{Body: []byte("a世🙂z")}}
					target := "42"
					if selector == "url" {
						target = item.WebURL
					}
					scope := "personal"
					if project {
						scope = "project"
					}
					args := []string{"snippet", "view", target, "--scope", scope}
					if project {
						args = append(args, "-R", "group/project")
					}
					switch mode {
					case "files":
						args = append(args, "--files")
					case "content":
						args = append(args, "--filename", "dir/a b.txt", "--content-limit", "5")
					}
					e, code := runSnippetTest(t, f, args...)
					if code != 0 || !e.OK || !e.Meta.Complete || e.Data.Snippet.ID != 42 || !e.Data.Snippet.FilesAvailable || len(e.Data.Snippet.Files) != 1 {
						t.Fatalf("code=%d result=%+v", code, e)
					}
					if mode == "content" {
						c := e.Data.Snippet.Content
						if c == nil || c.Text != "a世" || !c.Truncated || !e.Meta.Truncated || e.Meta.Reason != "field_limit" {
							t.Fatalf("content=%+v meta=%+v", c, e.Meta)
						}
						r := f.requests[len(f.requests)-2]
						if r.Operation != glab.OpSnippetFile || r.Ref != "main" || r.Filename != "dir/a b.txt" || r.ID != 42 {
							t.Fatalf("request=%+v", r)
						}
					} else if e.Data.Snippet.Content != nil {
						t.Fatal("unrequested content")
					}
					if mode == "files" && e.Data.Snippet.Description != nil {
						t.Fatal("files exposed description")
					}
				})
			}
		}
	}
}

func TestSnippetFailClosedProviderResponses(t *testing.T) {
	mutations := map[string]func(*upstreamSnippet){
		"numeric URL": func(s *upstreamSnippet) { s.WebURL = "42" },
		"wrong id":    func(s *upstreamSnippet) { s.ID = 99 }, "wrong host": func(s *upstreamSnippet) { s.WebURL = strings.Replace(s.WebURL, "gitlab.com", "evil.example", 1) },
		"wrong scope": func(s *upstreamSnippet) { n := int64(101); s.ProjectID = &n }, "unknown visibility": func(s *upstreamSnippet) { s.Visibility = "secret" },
		"missing owner": func(s *upstreamSnippet) { s.Author.ID = 0 }, "owner url": func(s *upstreamSnippet) { s.Author.WebURL = "https://evil.example/reader" },
		"raw wrong id": func(s *upstreamSnippet) { s.RawURL = "https://gitlab.com/-/snippets/99/raw" },
		"file wrong host": func(s *upstreamSnippet) {
			s.Files[0].RawURL = strings.Replace(s.Files[0].RawURL, "gitlab.com", "evil.example", 1)
		},
		"file wrong id": func(s *upstreamSnippet) { s.Files[0].RawURL = strings.Replace(s.Files[0].RawURL, "/42/", "/99/", 1) },
		"file wrong name": func(s *upstreamSnippet) {
			s.Files[0].RawURL = strings.Replace(s.Files[0].RawURL, "a%20b.txt", "other.txt", 1)
		},
		"file traversal": func(s *upstreamSnippet) { s.Files[0].Path = "../oops" }, "file query": func(s *upstreamSnippet) { s.Files[0].RawURL += "?token=bad" },
		"file duplicate": func(s *upstreamSnippet) { s.Files = append(s.Files, s.Files[0]) }, "invalid date": func(s *upstreamSnippet) { s.UpdatedAt = "yesterday" },
		"nul title": func(s *upstreamSnippet) { s.Title = "a\x00b" },
	}
	for name, change := range mutations {
		t.Run(name, func(t *testing.T) {
			f := snippetFake()
			item := snippetFixture(42, false)
			change(&item)
			f.responses[glab.OpSnippetView] = []glab.Response{{Body: snippetJSON(item)}}
			e, code := runSnippetTest(t, f, "snippet", "view", "42", "--scope", "personal")
			if code == 0 || e.OK || e.Meta.Complete {
				t.Fatalf("result=%+v", e)
			}
		})
	}
	for _, body := range []string{"null", "{}", "[]", "{bad", string([]byte{0xff}), strings.Replace(string(snippetJSON(snippetFixture(42, false))), `"project_id":null,`, "", 1), string(snippetJSON(snippetFixture(42, false))) + "{}"} {
		f := snippetFake()
		f.responses[glab.OpSnippetView] = []glab.Response{{Body: []byte(body)}}
		if _, code := runSnippetTest(t, f, "snippet", "view", "42", "--scope", "personal"); code == 0 {
			t.Fatalf("accepted %q", body)
		}
	}
	for _, body := range []string{`{"id":101,"path_with_namespace":"other/project","web_url":"https://gitlab.com/other/project"}`, `{"id":0,"path_with_namespace":"group/project","web_url":"https://gitlab.com/group/project"}`} {
		f := snippetFake()
		f.responses[glab.OpSnippetProject] = []glab.Response{{Body: []byte(body)}}
		if _, code := runSnippetTest(t, f, "snippet", "list", "--scope", "project", "-R", "group/project"); code == 0 || len(f.requests) != 2 {
			t.Fatal("unbound project accepted")
		}
	}
}

func TestSnippetUnavailableChangedAndBinaryFiles(t *testing.T) {
	for _, which := range []string{"missing", "inventory", "raw unavailable", "changed", "binary", "not utf8", "oversized"} {
		t.Run(which, func(t *testing.T) {
			f := snippetFake()
			item := snippetFixture(42, false)
			after := item
			content := []byte("hello")
			filename := "dir/a b.txt"
			switch which {
			case "missing":
				filename = "missing.txt"
			case "inventory":
				item.Files = nil
			case "raw unavailable":
				item.Files[0].RawURL = ""
			case "changed":
				after.Title = "changed"
			case "binary":
				content = []byte("a\x00b")
			case "not utf8":
				content = []byte{0xff}
			case "oversized":
				content = bytes.Repeat([]byte("x"), limits.MaxJSONPageBytes+1)
			}
			f.responses[glab.OpSnippetView] = []glab.Response{{Body: snippetJSON(item)}, {Body: snippetJSON(after)}}
			f.responses[glab.OpSnippetFile] = []glab.Response{{Body: content}}
			e, code := runSnippetTest(t, f, "snippet", "view", "42", "--scope", "personal", "--filename", filename)
			if code == 0 || e.OK || e.Meta.Complete {
				t.Fatalf("result=%+v", e)
			}
			if (which == "missing" || which == "inventory" || which == "raw unavailable") && len(f.requests) != 2 {
				t.Fatal("unavailable selection performed extra work")
			}
		})
	}
}

func TestSnippetPaginationCompletenessAndLocalFiltering(t *testing.T) {
	for _, which := range []string{"exact", "overflow", "filtered", "project exclusion", "hard bound", "duplicate", "too many", "null", "owner mismatch", "budget"} {
		t.Run(which, func(t *testing.T) {
			f := snippetFake()
			calls := 0
			f.doFunc = func(ctx context.Context, r glab.Request) (glab.Response, error, bool) {
				if r.Operation != glab.OpSnippetList {
					return glab.Response{}, nil, false
				}
				calls++
				if r.PerPage != 3 || r.Page != calls {
					t.Fatalf("unstable pagination: %+v", r)
				}
				items := []upstreamSnippet{snippetFixture(int64(calls*3), false), snippetFixture(int64(calls*3+1), false), snippetFixture(int64(calls*3+2), false)}
				switch which {
				case "exact":
					items = items[:2]
				case "filtered":
					for i := range items {
						items[i].Visibility = "public"
					}
					if calls == 2 {
						items = items[:1]
						items[0].Visibility = "private"
					}
				case "project exclusion":
					for i := range items {
						items[i] = snippetFixture(items[i].ID, true)
					}
					if calls == 2 {
						items = []upstreamSnippet{snippetFixture(99, false)}
					}
				case "hard bound":
					for i := range items {
						items[i].Visibility = "public"
					}
				case "duplicate":
					items[1] = items[0]
				case "too many":
					items = append(items, snippetFixture(99, false))
				case "null":
					items = nil
				case "owner mismatch":
					items[0].Author.ID = 9
				case "budget":
					for i := range items {
						items[i].Description = strings.Repeat("x", 500000)
						items[i].Visibility = "public"
					}
				}
				return glab.Response{Body: snippetJSON(items)}, nil, true
			}
			args := []string{"snippet", "list", "--scope", "personal", "--limit", "2"}
			if which == "filtered" || which == "hard bound" || which == "budget" {
				args = append(args, "--visibility", "private")
			}
			e, code := runSnippetTest(t, f, args...)
			switch which {
			case "duplicate", "too many", "null", "owner mismatch", "budget":
				if code == 0 || e.OK {
					t.Fatalf("accepted %+v", e)
				}
			case "overflow":
				if code != 0 || e.Meta.Complete || !e.Meta.Truncated || e.Meta.Reason != "display_limit" || e.Meta.Count != 2 {
					t.Fatalf("%+v", e)
				}
			case "hard bound":
				if code != 0 || e.Meta.Complete || e.Meta.Reason != "hard_page_limit" || calls != 10 || e.Meta.Count != 0 {
					t.Fatalf("%+v calls=%d", e, calls)
				}
			default:
				if code != 0 || !e.Meta.Complete || e.Meta.Truncated {
					t.Fatalf("%+v", e)
				}
			}
			if which == "filtered" || which == "project exclusion" {
				if calls != 2 || e.Meta.Count != 1 {
					t.Fatalf("filtered completeness: %+v calls=%d", e, calls)
				}
			}
		})
	}
}

func TestSnippetHardThousandItemBoundary(t *testing.T) {
	for _, total := range []int{999, 1000} {
		t.Run(strconv.Itoa(total), func(t *testing.T) {
			f := snippetFake()
			calls := 0
			f.doFunc = func(ctx context.Context, r glab.Request) (glab.Response, error, bool) {
				if r.Operation != glab.OpSnippetList {
					return glab.Response{}, nil, false
				}
				calls++
				if r.Page != calls || r.PerPage != 100 {
					t.Fatalf("request=%+v", r)
				}
				items := []upstreamSnippet{}
				for i := (r.Page-1)*100 + 1; i <= min(r.Page*100, total); i++ {
					items = append(items, snippetFixture(int64(i), false))
				}
				return glab.Response{Body: snippetJSON(items)}, nil, true
			}
			e, code := runSnippetTest(t, f, "snippet", "list", "--scope", "personal", "--limit", "1000")
			if code != 0 || e.Meta.Count != total || calls != 10 || e.Meta.Complete != (total == 999) || e.Meta.Truncated != (total == 1000) {
				t.Fatalf("result=%+v calls=%d", e.Meta, calls)
			}
		})
	}
}

func TestSnippetFieldSelectionIsAdditiveAndUTF8Bounded(t *testing.T) {
	for _, field := range []string{"", "description", "created_at", "updated_at"} {
		f := snippetFake()
		item := snippetFixture(42, false)
		item.Description = strings.Repeat("世", 12000)
		f.responses[glab.OpSnippetList] = []glab.Response{{Body: snippetJSON([]upstreamSnippet{item})}}
		args := []string{"snippet", "list", "--scope", "personal"}
		if field != "" {
			args = append(args, "--fields", field)
		}
		e, code := runSnippetTest(t, f, args...)
		if code != 0 || len(e.Data.Snippets) != 1 {
			t.Fatalf("result=%+v", e)
		}
		s := e.Data.Snippets[0]
		if (s.Description != nil) != (field == "description") || (s.CreatedAt != "") != (field == "created_at") || (s.UpdatedAt != "") != (field == "updated_at") || s.ID != 42 || s.Owner.ID != 7 || s.WebURL == "" {
			t.Fatalf("selection %s: %+v", field, s)
		}
		if field == "description" && (!e.Meta.Truncated || !e.Meta.Complete || len(*s.Description) > 32768 || !utf8.ValidString(*s.Description)) {
			t.Fatal("field truncation lost completeness or UTF-8 bound")
		}
	}
}

func TestSnippetUTF8LimitsAndControlledErrors(t *testing.T) {
	for _, limit := range []int{0, 1, 2, 3, 4, 5, 32768, 131072} {
		text, cut, err := snippetText(strings.Repeat("世🙂", 20000), limit)
		if err != nil || !cut || len(text) > limit || !utf8.ValidString(text) {
			t.Fatalf("limit=%d len=%d cut=%v err=%v", limit, len(text), cut, err)
		}
	}
	for _, code := range []uxv1.Code{uxv1.CodeAuthentication, uxv1.CodeNotFound, uxv1.CodeCanceled, uxv1.CodeUpstream} {
		f := snippetFake()
		f.errors = map[glab.Operation][]error{glab.OpSnippetUser: {uxv1.NewError(code, "controlled failure")}}
		e, exit := runSnippetTest(t, f, "snippet", "list", "--scope", "personal")
		if exit == 0 || e.OK || e.Meta.Complete || e.Error.Code != code || len(f.requests) != 1 {
			t.Fatalf("result=%+v", e)
		}
	}
}
