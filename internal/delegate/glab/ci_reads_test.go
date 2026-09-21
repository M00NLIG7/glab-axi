package glab

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCIReadTypedArgvAndEvidence(t *testing.T) {
	sha := strings.Repeat("a", 40)
	filters := PipelineFilters{Ref: "feature/ref", Status: "failed", Source: "push", User: "alice", SHA: sha}
	got, err := build(Request{Operation: OpPipelineList, Host: "gitlab.com", Repo: "group/project", Page: 2, PerPage: 100, PipelineFilters: filters})
	want := []string{"ci", "list", "--output", "json", "--ref", "feature/ref", "--status", "failed", "--source", "push", "--username", "alice", "--sha", sha, "--page", "2", "--per-page", "100", "-R", "group/project"}
	if err != nil || !reflect.DeepEqual(got.args, want) {
		t.Fatalf("args=%v err=%v", got.args, err)
	}
	got, err = build(Request{Operation: OpJobList, Host: "gitlab.com", Repo: "group/project", PipelineID: 55, Page: 2, PerPage: 100, JobStatus: "failed"})
	if err != nil || got.args[len(got.args)-1] != "projects/group%2Fproject/pipelines/55/jobs?page=2&per_page=100&scope%5B%5D=failed" {
		t.Fatalf("args=%v err=%v", got.args, err)
	}
	for _, bad := range []PipelineFilters{{Ref: "../x"}, {Status: "green"}, {Source: "workflow"}, {User: "alice,bob"}, {SHA: "abc"}} {
		if _, err := build(Request{Operation: OpPipelineList, Host: "gitlab.com", Repo: "group/project", Page: 1, PerPage: 31, PipelineFilters: bad}); err == nil {
			t.Fatalf("accepted %#v", bad)
		}
	}
	if _, err := build(Request{Operation: OpJobList, Host: "gitlab.com", Repo: "group/project", PipelineID: 55, Page: 1, PerPage: 31, JobStatus: "failed&foo=bar"}); err == nil {
		t.Fatal("status query injection accepted")
	}
	fixturePath := filepath.Join("..", "..", "..", "contracts", "read-parity", "ci-reads.json")
	body, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Digest    string `json:"provider_evidence_sha256"`
		CLIDigest string `json:"provider_cli_source_sha256"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "official-glab", "v1.112.0", "ci-reads-source.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(source)
	if hex.EncodeToString(digest[:]) != fixture.Digest {
		t.Fatal("CI read provider source evidence drifted")
	}
	cliSource, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "official-glab", "v1.112.0", "ci-list-source.go.txt"))
	if err != nil {
		t.Fatal(err)
	}
	cliDigest := sha256.Sum256(cliSource)
	if hex.EncodeToString(cliDigest[:]) != fixture.CLIDigest {
		t.Fatal("CI list source evidence drifted")
	}
}

func TestCIReadRemainingCaptureBudgetTerminatesChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fixture uses a POSIX script")
	}
	script, record := fakeGlab(t)
	client := NewClient(ClientConfig{Path: script, Env: []string{"PATH=/usr/bin:/bin", "GLAB_AXI_FAKE_RECORD=" + record, "GLAB_AXI_FAKE_BODY=" + strings.Repeat("x", 100)}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := client.Do(ctx, Request{Operation: OpPipelineView, Host: "gitlab.com", Repo: "group/project", ID: 55, MaxResponseBytes: 10})
	if err == nil || len(response.Body) != 0 || !strings.Contains(err.Error(), "safety limit") {
		t.Fatalf("response=%#v err=%v", response, err)
	}
}

// Actual pinned official glab against TLS only, with a synthetic token and an
// empty profile. This covers ci list's translation to GitLab query parameters
// as well as every fixed pipeline/job/trace route used by view and watch.
func TestPinnedOfficialGlabCIReadsTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	logicalHost := "gitlab.ci-read-contract.example"
	cert, caPEM := selfManagedTestCertificate(t, logicalHost)
	var mu sync.Mutex
	var records []string
	secret := strings.Join([]string{"synthetic", "ci", "contract", "token"}, "-")
	pipeline := fmt.Sprintf(`{"id":55,"iid":7,"project_id":99,"status":"failed","source":"push","ref":"feature/ref","sha":"%s","web_url":"https://%s/group/project/-/pipelines/55","updated_at":"2026-01-02T03:04:05Z"}`, strings.Repeat("a", 40), logicalHost)
	job := fmt.Sprintf(`{"id":9,"name":"test","stage":"test","status":"failed","pipeline":%s,"web_url":"https://%s/group/project/-/jobs/9"}`, pipeline, logicalHost)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		records = append(records, r.Method+" "+r.RequestURI)
		mu.Unlock()
		if r.Method != http.MethodGet || r.Header.Get("PRIVATE-TOKEN") != secret || strings.Contains(r.RequestURI, secret) {
			http.Error(w, "invalid read authority", 403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.EscapedPath() {
		case "/api/v4/projects/group%2Fproject/pipelines":
			want := url.Values{"order_by": {"id"}, "sort": {"desc"}, "ref": {"feature/ref"}, "status": {"failed"}, "source": {"push"}, "username": {"alice"}, "sha": {strings.Repeat("a", 40)}, "page": {"2"}, "per_page": {"100"}}
			if !reflect.DeepEqual(r.URL.Query(), want) {
				http.Error(w, "incorrect typed filters", 400)
				return
			}
			_, _ = fmt.Fprint(w, "["+pipeline+"]")
		case "/api/v4/projects/group%2Fproject/pipelines/55":
			_, _ = fmt.Fprint(w, pipeline)
		case "/api/v4/projects/group%2Fproject/pipelines/55/jobs":
			want := url.Values{"scope[]": {"failed"}, "page": {"2"}, "per_page": {"100"}}
			if !reflect.DeepEqual(r.URL.Query(), want) {
				http.Error(w, "incorrect scope filter", 400)
				return
			}
			_, _ = fmt.Fprint(w, "["+job+"]")
		case "/api/v4/projects/group%2Fproject/jobs/9":
			_, _ = fmt.Fprint(w, job)
		case "/api/v4/projects/group%2Fproject/jobs/9/trace":
			w.Header().Set("Content-Type", "text/plain")
			_, _ = fmt.Fprint(w, "failed tail\n")
		default:
			http.Error(w, "unexpected route", 404)
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(logicalHost+":443", server.Listener.Addr().String())
	defer proxy.Close()
	home := t.TempDir()
	caPath := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	writeOfficialTestCAConfig(t, home, logicalHost, caPath)
	client := NewClient(ClientConfig{Path: binary, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + filepath.Join(home, "config"), "GITLAB_TOKEN=" + secret, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caPath, "PATH=/usr/bin:/bin"}})
	requests := []Request{
		{Operation: OpPipelineList, Page: 2, PerPage: 100, PipelineFilters: PipelineFilters{Ref: "feature/ref", Status: "failed", Source: "push", User: "alice", SHA: strings.Repeat("a", 40)}},
		{Operation: OpPipelineView, ID: 55},
		{Operation: OpJobList, PipelineID: 55, Page: 2, PerPage: 100, JobStatus: "failed"},
		{Operation: OpJobView, ID: 9},
		{Operation: OpJobTrace, ID: 9},
	}
	for _, request := range requests {
		request.Host = logicalHost
		request.Repo = "group/project"
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		response, err := client.Do(ctx, request)
		cancel()
		if err != nil || response.Write || response.UpstreamVersion != SupportedVersion || len(response.Body) == 0 || strings.Contains(string(response.Body), secret) {
			mu.Lock()
			captured := append([]string(nil), records...)
			mu.Unlock()
			t.Fatalf("%s: err=%v response=%#v routes=%v", request.Operation, err, response, captured)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(records) != len(requests) {
		t.Fatalf("unexpected extra provider request: %v", records)
	}
}
