package glab

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
)

func TestCollaborationTypedRoutesMatchVersionedFixtures(t *testing.T) {
	var fixture struct {
		Operations []struct {
			Name string
			Argv []string
		}
	}
	body, err := os.ReadFile("../../../contracts/collaboration-reads/v1.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, operation := range fixture.Operations {
		r := Request{Operation: Operation(operation.Name), Host: "gitlab.example", Repo: "group/sub/project", IID: 7, Page: 2, PerPage: 31}
		inv, err := build(r)
		if err != nil {
			t.Fatal(err)
		}
		replacer := strings.NewReplacer("{host}", r.Host, "{escaped_repo}", "group%2Fsub%2Fproject", "{iid}", "7", "{page}", "2", "{per_page}", "31")
		want := make([]string, len(operation.Argv))
		for i, arg := range operation.Argv {
			want[i] = replacer.Replace(arg)
		}
		if !reflect.DeepEqual(inv.args, want) || inv.write {
			t.Fatalf("%s: %#v want=%v", operation.Name, inv, want)
		}
		for _, invalid := range []Request{
			{Operation: r.Operation, Host: r.Host, Repo: r.Repo, IID: 0, Page: 1, PerPage: 31},
			{Operation: r.Operation, Host: "https://evil.invalid", Repo: r.Repo, IID: 7, Page: 1, PerPage: 31},
			{Operation: r.Operation, Host: r.Host, Repo: "group/../project", IID: 7, Page: 1, PerPage: 31},
		} {
			if _, err := build(invalid); err == nil {
				t.Fatalf("accepted %#v", invalid)
			}
		}
	}
	for _, r := range []Request{{Operation: OpIssueDiscussions, Host: "gitlab.example", Repo: "group/project", IID: 7, Page: 11, PerPage: 31}, {Operation: OpIssueDiscussions, Host: "gitlab.example", Repo: "group/project", IID: 7, Page: 1, PerPage: 101}} {
		if _, err := build(r); err == nil {
			t.Fatal("invalid page accepted")
		}
	}
}

func TestApprovalAvailabilityNeedsPinnedHTTPFraming(t *testing.T) {
	for _, test := range []struct {
		text   string
		status int
	}{{"glab: 403 Forbidden (HTTP 403)", 403}, {"HTTP 404", 404}, {"403 forbidden", 0}, {"404 not found", 0}, {"provider said permission denied", 0}} {
		err := uxv1.AsError(classifyChildFailure([]byte(test.text), errors.New("child"), false, OpMRApprovals))
		if err.StatusCode != test.status {
			t.Fatalf("%q: %#v", test.text, err)
		}
	}
}

// Runs the real pinned official glab against TLS fixtures, with an empty profile
// and synthetic token only. CI's existing official package job enables this.
func TestPinnedOfficialGlabCollaborationReadsTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	host := "gitlab.collaboration.example"
	certificate, ca := selfManagedTestCertificate(t, host)
	var mu sync.Mutex
	var requests []string
	status := 200
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.RequestURI)
		current := status
		mu.Unlock()
		if r.Host != host || r.Method != "GET" || r.Header.Get("PRIVATE-TOKEN") != strings.Join([]string{"synthetic", "collaboration", "tls"}, "-") {
			http.Error(w, "unexpected authority", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if current != 200 {
			w.WriteHeader(current)
			fmt.Fprint(w, `{"message":"provider-secret"}`)
			return
		}
		switch r.RequestURI {
		case "/api/v4/projects/group%2Fproject/issues/7/discussions?page=2&per_page=31":
			fmt.Fprint(w, `[]`)
		case "/api/v4/projects/group%2Fproject/merge_requests/7/approvals":
			fmt.Fprint(w, `{"id":7007,"iid":7,"project_id":99,"approved":true,"approved_by":[]}`)
		default:
			http.Error(w, "unexpected route", 400)
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(host+":443", server.Listener.Addr().String())
	defer proxy.Close()
	home := t.TempDir()
	caFile := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(caFile, ca, 0o600); err != nil {
		t.Fatal(err)
	}
	// SSL_CERT_FILE is not a portable override of macOS system roots. The
	// pinned client's explicit ca_cert option adds this test CA on all hosts.
	configDir := filepath.Join(home, "config")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	config := fmt.Sprintf("hosts:\n  %s:\n    ca_cert: %q\n", host, caFile)
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(ClientConfig{Path: binary, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + filepath.Join(home, "config"), "GITLAB_TOKEN=" + strings.Join([]string{"synthetic", "collaboration", "tls"}, "-"), "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caFile, "PATH=/usr/bin:/bin"}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, op := range []Operation{OpIssueDiscussions, OpMRApprovals} {
		response, err := client.Do(ctx, Request{Operation: op, Host: host, Repo: "group/project", IID: 7, Page: 2, PerPage: 31})
		if err != nil || response.Write || !json.Valid(response.Body) {
			t.Fatalf("%s: response=%#v err=%v", op, response, err)
		}
	}
	for _, s := range []int{403, 404, 401, 429} {
		mu.Lock()
		status = s
		mu.Unlock()
		_, err := client.Do(ctx, Request{Operation: OpMRApprovals, Host: host, Repo: "group/project", IID: 7})
		classified := uxv1.AsError(err)
		if classified == nil || classified.StatusCode != s || strings.Contains(classified.Message, "provider-secret") {
			t.Fatalf("status=%d err=%#v", s, classified)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 6 || requests[0] != "GET /api/v4/projects/group%2Fproject/issues/7/discussions?page=2&per_page=31" {
		t.Fatalf("requests=%v", requests)
	}
}
