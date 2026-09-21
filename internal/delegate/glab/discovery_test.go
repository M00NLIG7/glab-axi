package glab

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func discoveryRequests() []struct {
	request  Request
	endpoint string
} {
	return []struct {
		request  Request
		endpoint string
	}{
		{Request{Operation: OpRepoDiscovery, Discovery: DiscoverySelectors{Owner: "alice", Visibility: "private", Archived: "false", Language: "C++"}}, "users/alice/projects?archived=false&page=2&per_page=31&visibility=private&with_programming_language=C%2B%2B"},
		{Request{Operation: OpRepoDiscovery, Discovery: DiscoverySelectors{Group: "team/sub", Visibility: "public", Archived: "true", IncludeSubgroups: true}}, "groups/team%2Fsub/projects?archived=true&include_subgroups=true&page=2&per_page=31&visibility=public&with_shared=false"},
		{Request{Operation: OpRepoDiscovery, Query: "cli", Discovery: DiscoverySelectors{Language: "Go"}, Search: SearchSelectors{Sort: "created"}}, "projects?order_by=created_at&page=2&per_page=31&search=cli&sort=desc&with_programming_language=Go"},
		{Request{Operation: OpRepoDiscovery, Discovery: DiscoverySelectors{Owner: "0xalice", Language: "Ren'Py"}}, "users/0xalice/projects?page=2&per_page=31&with_programming_language=Ren%27Py"},
		{Request{Operation: OpRepoDiscovery, Query: "cli", Discovery: DiscoverySelectors{Language: "F*"}}, "projects?page=2&per_page=31&search=cli&with_programming_language=F%2A"},
		{Request{Operation: OpSearch, Scope: "repos", Query: "cli", Search: SearchSelectors{Sort: "created"}}, "projects?archived=false&order_by=created_at&page=2&per_page=31&search=cli&search_namespaces=true&sort=desc"},
		{Request{Operation: OpSearch, Scope: "repos", Query: "cli", Search: SearchSelectors{Group: "team/sub", Sort: "created"}}, "groups/team%2Fsub/projects?archived=false&include_subgroups=true&order_by=created_at&page=2&per_page=31&search=cli&search_namespaces=true&sort=desc&with_shared=false"},
		{Request{Operation: OpDiscoveryGroup, Discovery: DiscoverySelectors{Group: "team/sub"}}, "groups/team%2Fsub?with_projects=false"},
		{Request{Operation: OpDiscoveryProject, Repo: "team/sub/project"}, "projects/team%2Fsub%2Fproject"},
		{Request{Operation: OpDiscoveryProject, ID: 42}, "projects/42"},
		{Request{Operation: OpSearch, Scope: "issues", Query: "bug fix", Search: SearchSelectors{Area: "host", State: "closed", Sort: "created"}}, "search?order_by=created_at&page=2&per_page=31&scope=issues&search=bug+fix&sort=desc&state=closed"},
		{Request{Operation: OpSearch, Scope: "mrs", Query: "bug", Search: SearchSelectors{Group: "team/sub", State: "merged"}}, "groups/team%2Fsub/search?page=2&per_page=31&scope=merge_requests&search=bug&state=merged"},
		{Request{Operation: OpSearch, Repo: "team/sub/project", Scope: "issues", Query: "bug", Search: SearchSelectors{State: "opened"}}, "projects/team%2Fsub%2Fproject/search?page=2&per_page=31&scope=issues&search=bug&state=opened"},
	}
}

func TestDiscoveryTypedArgv(t *testing.T) {
	for _, test := range discoveryRequests() {
		r := test.request
		r.Host = "gitlab.com"
		r.Page = 2
		r.PerPage = 31
		invocation, err := build(r)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := strings.Join(invocation.args, " "), "api --method GET --hostname gitlab.com "+test.endpoint; got != want || invocation.write {
			t.Fatalf("got %s want %s write=%t", got, want, invocation.write)
		}
	}
	for _, r := range []Request{
		{Operation: OpRepoDiscovery, Page: 11, PerPage: 30}, {Operation: OpRepoDiscovery, Page: 1, PerPage: 101},
		{Operation: OpRepoDiscovery, Page: 1, PerPage: 30, Discovery: DiscoverySelectors{Owner: "@me"}},
		{Operation: OpRepoDiscovery, Page: 1, PerPage: 30, Discovery: DiscoverySelectors{Owner: "123"}},
		{Operation: OpRepoDiscovery, Page: 1, PerPage: 30, Discovery: DiscoverySelectors{Language: "F*\n"}},
		{Operation: OpRepoDiscovery, Page: 1, PerPage: 30, Discovery: DiscoverySelectors{Language: "F\t*"}},
		{Operation: OpRepoDiscovery, Page: 1, PerPage: 30, Discovery: DiscoverySelectors{Language: "\xff"}},
		{Operation: OpRepoDiscovery, Page: 1, PerPage: 30, Discovery: DiscoverySelectors{Language: strings.Repeat("x", 65)}},
		{Operation: OpRepoDiscovery, Page: 1, PerPage: 30, Discovery: DiscoverySelectors{Owner: "alice", Group: "team"}},
		{Operation: OpRepoDiscovery, Page: 1, PerPage: 30, Discovery: DiscoverySelectors{Group: "team", Language: "Go"}},
		{Operation: OpDiscoveryGroup, Discovery: DiscoverySelectors{Group: "../escape"}},
		{Operation: OpDiscoveryProject, ID: -1},
		{Operation: OpSearch, Page: 1, PerPage: 30, Scope: "code", Query: "x", Search: SearchSelectors{Area: "host"}},
		{Operation: OpSearch, Page: 1, PerPage: 30, Scope: "issues", Query: "x", Search: SearchSelectors{Area: "host", Sort: "updated"}},
		{Operation: OpSearch, Page: 1, PerPage: 30, Scope: "issues", Query: "x", Search: SearchSelectors{Area: "host", State: "merged"}},
		{Operation: OpSearch, Page: 1, PerPage: 30, Scope: "repos", Query: "go cli", Search: SearchSelectors{Sort: "created"}},
		{Operation: OpSearch, Page: 1, PerPage: 30, Scope: "repos", Query: "\"go\" cli", Search: SearchSelectors{Group: "team/sub", Sort: "created"}},
	} {
		r.Host = "gitlab.com"
		if _, err := build(r); err == nil {
			t.Fatalf("accepted %#v", r)
		}
	}
}

// With the official package supplied, execute the exact API argv against a TLS
// fake. No user's profile, live endpoint, or real credential is accessible.
func TestPinnedOfficialGlabDiscoveryTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	const host = "gitlab.discovery-contract.example"
	cert, ca := selfManagedTestCertificate(t, host)
	records := make(chan string, 32)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.Host != host || r.ContentLength > 0 {
			t.Errorf("unexpected request %s %s", r.Method, r.Host)
		}
		records <- r.RequestURI
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(host+":443", server.Listener.Addr().String())
	defer proxy.Close()
	home := t.TempDir()
	caPath := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	writeOfficialTestCAConfig(t, home, host, caPath)
	client := NewClient(ClientConfig{Path: binary, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + filepath.Join(home, "config"), "GITLAB_TOKEN=" + strings.Join([]string{"synthetic", "discovery", "token"}, "-"), "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caPath, "PATH=/usr/bin:/bin"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, test := range discoveryRequests() {
		r := test.request
		r.Host = host
		r.Page = 2
		r.PerPage = 31
		response, err := client.Do(ctx, r)
		if err != nil || response.Write || string(response.Body) != "[]" {
			t.Fatalf("%s: %v %#v", r.Operation, err, response)
		}
		select {
		case got := <-records:
			if got != "/api/v4/"+test.endpoint {
				t.Fatalf("got %s want %s", got, test.endpoint)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	select {
	case extra := <-records:
		t.Fatalf("extra request %s", extra)
	default:
	}
}
