package glab

import (
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
)

func TestListFiltersBuildClosedArgv(t *testing.T) {
	for _, test := range []struct {
		op      Operation
		filters ListFilters
		want    []string
	}{
		{OpIssueList, ListFilters{State: "open"}, nil},
		{OpMRList, ListFilters{State: "open"}, nil},
		{OpIssueList, ListFilters{State: "closed", Labels: []string{"triage", "needs review"}, Author: "alice", Assignee: "bob", Milestone: "release 2", Sort: "updated"}, []string{"--closed", "--label=triage", "--label=needs review", "--author=alice", "--assignee=bob", "--milestone=release 2", "--order=updated_at", "--sort=desc"}},
		{OpMRList, ListFilters{State: "merged", SourceBranch: "feature/topic", TargetBranch: "main"}, []string{"--merged", "--source-branch=feature/topic", "--target-branch=main"}},
		{OpMRList, ListFilters{State: "all", Draft: true}, []string{"--all", "--draft"}},
	} {
		request := Request{Operation: test.op, Host: "gitlab.com", Repo: "group/project", Page: 2, PerPage: 31, Filters: test.filters}
		inv, err := build(request)
		if err != nil {
			t.Fatal(err)
		}
		group := "issue"
		if test.op == OpMRList {
			group = "mr"
		}
		want := append([]string{group, "list", "--output", "json"}, test.want...)
		want = append(want, "--page", "2", "--per-page", "31", "-R", "group/project")
		if !reflect.DeepEqual(inv.args, want) || inv.write {
			t.Fatalf("invocation=%#v want=%v", inv, want)
		}
	}
	for _, filters := range []ListFilters{
		{State: "bad"}, {Labels: []string{"a,b"}}, {Labels: []string{"any"}}, {Labels: []string{"x", "x"}},
		{Author: "--all"}, {Assignee: "@me"}, {Milestone: "None"}, {Sort: "comments"},
		{SourceBranch: "../main"}, {TargetBranch: "main.lock"},
	} {
		if _, err := build(Request{Operation: OpMRList, Host: "gitlab.com", Repo: "group/project", Page: 1, PerPage: 31, Filters: filters}); err == nil {
			t.Fatalf("invalid filters accepted: %#v", filters)
		}
	}
}

func TestListFiltersRejectUnsupportedSelectorsBeforeDependencyWork(t *testing.T) {
	for _, op := range []Operation{OpIssueList, OpMRList} {
		filters := []ListFilters{{State: "opened"}}
		for _, milestone := range []string{"None", "Any", "Upcoming", "Started", "#started", "#upcoming", "No Milestone", "Any Milestone", "#STARTED", "#UPCOMING", "no milestone", "ANY MILESTONE"} {
			filters = append(filters, ListFilters{Milestone: milestone})
		}
		if op == OpMRList {
			filters = append(filters, ListFilters{Milestone: "release 2"})
		}
		for _, filter := range filters {
			t.Run(string(op)+"/"+filter.State+filter.Milestone, func(t *testing.T) {
				client := NewClient(ClientConfig{Env: []string{}, LookPath: func(string) (string, error) {
					t.Fatal("invalid filters reached dependency lookup")
					return "", nil
				}})
				_, err := client.Do(context.Background(), Request{Operation: op, Host: "gitlab.com", Repo: "group/project", Page: 1, PerPage: 31, Filters: filter})
				if err == nil || uxv1.AsError(err).Code != uxv1.CodeValidation {
					t.Fatalf("error=%v", err)
				}
			})
		}
	}
}

// TestPinnedOfficialGlabReadFiltersTLS pins actual provider query semantics,
// including the username-resolution reads done by official glab. No live host,
// real profile, credential store, or write request is reachable through the proxy.
func TestPinnedOfficialGlabReadFiltersTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	logicalHost := "gitlab.read-filter-contract.example"
	certificate, caPEM := selfManagedTestCertificate(t, logicalHost)
	var mu sync.Mutex
	var records []string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		records = append(records, r.Method+" "+r.RequestURI)
		mu.Unlock()
		if r.Method != http.MethodGet || r.Host != logicalHost {
			http.Error(w, "unexpected method or host", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.EscapedPath() {
		case "/api/v4/users":
			switch r.URL.Query().Get("username") {
			case "alice":
				_, _ = io.WriteString(w, `[{"id":7,"username":"alice"}]`)
			case "bob":
				_, _ = io.WriteString(w, `[{"id":8,"username":"bob"}]`)
			default:
				http.Error(w, "unexpected username", 400)
			}
		case "/api/v4/projects/group%2Fproject":
			_, _ = io.WriteString(w, `{"id":99,"path_with_namespace":"group/project"}`)
		case "/api/v4/projects/group%2Fproject/issues", "/api/v4/projects/group%2Fproject/merge_requests":
			_, _ = io.WriteString(w, `[]`)
		default:
			http.Error(w, "unexpected route", 404)
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(logicalHost+":443", server.Listener.Addr().String())
	defer proxy.Close()
	home := t.TempDir()
	caBundle := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(caBundle, caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	// Official glab's non-secret per-host CA setting also works on macOS,
	// where Go's system trust loader does not use SSL_CERT_FILE.
	configDir := filepath.Join(home, "config")
	if err := os.Mkdir(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte("hosts:\n  "+logicalHost+":\n    ca_cert: "+caBundle+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(ClientConfig{Path: binary, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + filepath.Join(home, "config"), "GITLAB_TOKEN=" + strings.Join([]string{"synthetic", "filter", "token"}, "-"), "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caBundle, "PATH=/usr/bin:/bin"}})
	// Keep dependency startup distinct from provider request timing. Version
	// retains the client's five-second bound; failures must not be retried.
	started := time.Now()
	version, err := client.Version(context.Background())
	t.Logf("official glab version verification: %s", time.Since(started))
	mu.Lock()
	startupRequests := append([]string(nil), records...)
	mu.Unlock()
	if err != nil || version != SupportedVersion || len(startupRequests) != 0 {
		t.Fatalf("version=%q error=%v startup requests=%v", version, err, startupRequests)
	}
	for _, test := range []struct {
		op          Operation
		state, sort string
		draft       bool
	}{
		{OpIssueList, "open", "created", false},
		{OpMRList, "open", "", false},
		{OpIssueList, "closed", "updated", false},
		{OpIssueList, "all", "created", false},
		{OpMRList, "all", "", true},
		{OpMRList, "merged", "", false},
		{OpMRList, "closed", "", false},
	} {
		op := test.op
		filters := ListFilters{State: test.state, Sort: test.sort, Draft: test.draft, Labels: []string{"triage", "needs review"}, Author: "alice", Assignee: "bob"}
		if op == OpMRList {
			filters.SourceBranch = "feature/topic"
			filters.TargetBranch = "main"
		} else {
			filters.Milestone = "release 2"
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		started := time.Now()
		response, err := client.Do(ctx, Request{Operation: op, Host: logicalHost, Repo: "group/project", Page: 2, PerPage: 31, Filters: filters})
		cancel()
		t.Logf("op=%s state=%s request duration: %s", op, test.state, time.Since(started))
		mu.Lock()
		captured := append([]string(nil), records...)
		records = nil
		mu.Unlock()
		if err != nil || response.Write || strings.TrimSpace(string(response.Body)) != "[]" {
			t.Fatalf("op=%s error=%v body=%s requests=%v", op, err, response.Body, captured)
		}
		resource := "issues"
		if op == OpMRList {
			resource = "merge_requests"
		}
		var query url.Values
		for _, record := range captured {
			u, err := url.Parse(strings.TrimPrefix(record, "GET "))
			if err != nil {
				t.Fatal(err)
			}
			if u.EscapedPath() == "/api/v4/projects/group%2Fproject/"+resource {
				if query != nil {
					t.Fatal("unexpected second list request")
				}
				query = u.Query()
			}
		}
		want := url.Values{"page": {"2"}, "per_page": {"31"}, "state": {filters.State}, "labels": {"triage,needs review"}, "author_id": {"7"}, "assignee_id": {"8"}}
		if filters.State == "open" {
			want.Set("state", "opened")
		}
		if op == OpIssueList {
			want.Set("milestone", "release 2")
			want.Set("order_by", test.sort+"_at")
			want.Set("sort", "desc")
			want.Set("in", "title,description")
		} else {
			want.Set("source_branch", "feature/topic")
			want.Set("target_branch", "main")
			if test.draft {
				want.Set("wip", "yes")
			}
		}
		if !reflect.DeepEqual(query, want) || len(captured) != 3 {
			t.Fatalf("op=%s query=%v want=%v requests=%v", op, query, want, captured)
		}
		for _, record := range captured {
			if strings.Contains(record, "/users?") && record != "GET /api/v4/users?per_page=30&username=alice" && record != "GET /api/v4/users?per_page=30&username=bob" {
				t.Fatalf("unexpected username resolution: %s", record)
			}
		}
	}
}
