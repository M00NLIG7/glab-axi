package glab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSnippetEvidenceRoutesAndReadOnlyBuilder(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "official-glab", "v1.112.0", "snippet-reads.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Routes map[string]string `json:"routes"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, scope := range []string{"personal", "project"} {
		repo := ""
		if scope == "project" {
			repo = "group/sub/project"
		}
		for _, op := range []Operation{OpSnippetList, OpSnippetView, OpSnippetFile} {
			req := Request{Operation: op, Scope: scope, Host: "gitlab.example", Repo: repo, Page: 2, PerPage: 31, ID: 42, Ref: "main", Filename: "dir/a b.txt"}
			got, err := build(req)
			if err != nil {
				t.Fatal(err)
			}
			route := fixture.Routes[string(op)+"-"+scope]
			route = strings.NewReplacer("{escaped_repo}", "group%2Fsub%2Fproject", "{page}", "2", "{per_page}", "31", "{id}", "42", "{escaped_ref}", "main", "{escaped_filename}", "dir%2Fa%20b.txt").Replace(route)
			if route == "" || len(got.args) != 6 || strings.Join(got.args[:5], " ") != "api --method GET --hostname gitlab.example" || got.args[5] != route || got.write {
				t.Fatalf("request=%+v argv=%v route=%s", req, got.args, route)
			}
		}
	}
	for _, test := range []struct {
		op          Operation
		repo, route string
	}{{OpSnippetUser, "", "user"}, {OpSnippetProject, "group/project", "projects/group%2Fproject"}} {
		got, err := build(Request{Operation: test.op, Host: "gitlab.example", Repo: test.repo})
		if err != nil || got.write || len(got.args) != 6 || got.args[5] != test.route {
			t.Fatalf("argv=%v err=%v", got, err)
		}
	}
}

func TestSnippetBuilderInvalidBeforeExecutableLookup(t *testing.T) {
	base := Request{Operation: OpSnippetFile, Scope: "personal", Host: "gitlab.com", ID: 42, Ref: "main", Filename: "a.txt"}
	cases := []Request{}
	for _, change := range []func(*Request){
		func(r *Request) { r.Scope = "all" }, func(r *Request) { r.Repo = "group/project" }, func(r *Request) { r.Scope = "project" }, func(r *Request) { r.ID = 0 }, func(r *Request) { r.Filename = "../a" }, func(r *Request) { r.Ref = "../main" }, func(r *Request) { r.Host = "https://gitlab.com" }, func(r *Request) { r.Operation = OpSnippetList; r.Page = 11; r.PerPage = 100 }, func(r *Request) { r.Operation = OpSnippetList; r.Page = 1; r.PerPage = 101 },
	} {
		r := base
		change(&r)
		cases = append(cases, r)
	}
	for _, req := range cases {
		calls := 0
		client := NewClient(ClientConfig{LookPath: func(string) (string, error) { calls++; return "", nil }})
		if _, err := client.Do(context.Background(), req); err == nil || calls != 0 {
			t.Fatalf("request=%+v err=%v lookups=%d", req, err, calls)
		}
	}
}

func TestSnippetBuilderEscapesFilenamePlaceholders(t *testing.T) {
	for _, scope := range []string{"personal", "project"} {
		repo, prefix := "", ""
		if scope == "project" {
			repo, prefix = "group/project", "projects/group%2Fproject/"
		}
		for _, placeholder := range []string{"user", "username", "group", "namespace", "repo", "branch", "id", "fullpath"} {
			for _, dir := range []string{"", "dir/"} {
				name := dir + ":" + placeholder + ".txt"
				t.Run(scope+"/"+name, func(t *testing.T) {
					got, err := build(Request{Operation: OpSnippetFile, Scope: scope, Host: "gitlab.com", Repo: repo, ID: 42, Ref: "main", Filename: name})
					want := prefix + "snippets/42/files/main/" + strings.ReplaceAll(dir, "/", "%2F") + "%3A" + placeholder + ".txt/raw"
					if err != nil || len(got.args) != 6 || got.args[5] != want || got.write {
						t.Fatalf("argv=%v err=%v want=%s", got.args, err, want)
					}
				})
			}
		}
	}
}
