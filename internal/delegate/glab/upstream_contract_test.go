package glab

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
)

// TestPinnedUpstreamPublicContract executes only version/help against the
// checksum-verified official package. CI supplies the binary; no profile,
// credential, repository, or GitLab API is involved.
func TestPinnedUpstreamPublicContract(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.Command(binary, args...)
		cmd.Env = []string{
			"HOME=" + home,
			"GLAB_CONFIG_DIR=" + filepath.Join(home, "config"),
			"GLAB_CHECK_UPDATE=false",
			"NO_COLOR=1",
			"TERM=dumb",
			"PATH=/usr/bin:/bin",
		}
		var stdout, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if err := cmd.Run(); err != nil {
			t.Fatalf("glab %v: %v\nstdout=%s\nstderr=%s", args, err, stdout.String(), stderr.String())
		}
		if stdout.Len()+stderr.Len() > 256<<10 {
			t.Fatalf("glab %v help exceeded bound", args)
		}
		return stdout.String() + stderr.String()
	}
	if got, want := strings.TrimSpace(run("version")), "glab "+SupportedVersion+" ("+SupportedBuild+")"; got != want {
		t.Fatalf("version=%q want=%q", got, want)
	}
	checks := []struct {
		args     []string
		required []string
	}{
		{[]string{"auth", "login", "--help"}, []string{"--hostname", "--insecure-storage", "--device", "--web"}},
		{[]string{"auth", "status", "--help"}, []string{"--hostname", "--show-token"}},
		{[]string{"issue", "list", "--help"}, []string{"--output", "--page", "--per-page", "--repo"}},
		{[]string{"issue", "view", "--help"}, []string{"--output", "--repo"}},
		{[]string{"mr", "list", "--help"}, []string{"--output", "--source-branch", "--target-branch"}},
		{[]string{"mr", "view", "--help"}, []string{"--output", "--repo"}},
		{[]string{"mr", "diff", "--help"}, []string{"--color", "--repo"}},
		{[]string{"mr", "merge", "--help"}, []string{"--sha", "--squash", "--remove-source-branch", "--auto-merge"}},
		{[]string{"ci", "list", "--help"}, []string{"--output", "--page", "--per-page"}},
		{[]string{"ci", "get", "--help"}, []string{"--merge-request", "--pipeline-id", "--output"}},
		{[]string{"release", "list", "--help"}, []string{"--output", "--page", "--per-page"}},
		{[]string{"release", "view", "--help"}, []string{"--output", "--repo"}},
		{[]string{"repo", "list", "--help"}, []string{"--output", "--page", "--per-page"}},
		{[]string{"repo", "view", "--help"}, []string{"--output"}},
		{[]string{"label", "list", "--help"}, []string{"--output", "--page", "--per-page"}},
		{[]string{"api", "--help"}, []string{"--method", "--hostname", "--input"}},
	}
	for _, check := range checks {
		output := run(check.args...)
		for _, required := range check.required {
			if !strings.Contains(output, required) {
				t.Fatalf("glab %v no longer advertises %q", check.args, required)
			}
		}
	}
}

func officialGlabTestBinary() string {
	if binary := os.Getenv("GL_AXI_OFFICIAL_GLAB_TEST_BINARY"); binary != "" {
		return binary
	}
	return os.Getenv("GLAB_AXI_OFFICIAL_GLAB_TEST_BINARY")
}

type capturedOfficialGlabMutation struct {
	method      string
	host        string
	requestURI  string
	contentType string
	body        []byte
	readErr     error
}

func selfManagedTestCertificate(t *testing.T, hostname string) (tls.Certificate, []byte) {
	t.Helper()
	now := time.Now()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "gl-axi official-glab test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: hostname},
		DNSNames:     []string{hostname},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	serverKeyDER, err := x509.MarshalPKCS8PrivateKey(serverKey)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER}),
		pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: serverKeyDER}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return certificate, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

func newTestTLSTunnelProxy(targetAuthority, targetAddress string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != targetAuthority {
			http.Error(w, "unexpected tunnel target", http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", targetAddress, 5*time.Second)
		if err != nil {
			http.Error(w, "fake server unavailable", http.StatusBadGateway)
			return
		}
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			downstream.Close()
			upstream.Close()
			return
		}
		if err := buffered.Flush(); err != nil {
			downstream.Close()
			upstream.Close()
			return
		}
		uploadDone := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, downstream)
			if tcp, ok := upstream.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
			}
			close(uploadDone)
		}()
		_, _ = io.Copy(downstream, upstream)
		_ = downstream.Close()
		_ = upstream.Close()
		<-uploadDone
	}))
}

// TestPinnedOfficialGlabEnsureCreateTLS exercises the actual pinned mutation
// argv against an isolated TLS server. Its environment starts empty and uses
// only a runtime synthetic token; no GitLab credential or live write is used.
func TestPinnedOfficialGlabEnsureCreateTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}

	logicalHost := "gitlab.self-managed.example"
	certificate, caPEM := selfManagedTestCertificate(t, logicalHost)
	statuses := []int{http.StatusCreated, http.StatusBadRequest, http.StatusForbidden, http.StatusUnprocessableEntity}
	records := make(chan capturedOfficialGlabMutation, len(statuses)*2)
	var statusMu sync.Mutex
	statusIndex := 0
	totalRequests := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		records <- capturedOfficialGlabMutation{
			method:      r.Method,
			host:        r.Host,
			requestURI:  r.RequestURI,
			contentType: r.Header.Get("Content-Type"),
			body:        body,
			readErr:     readErr,
		}
		statusMu.Lock()
		totalRequests++
		if statusIndex >= len(statuses) {
			statusMu.Unlock()
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		status := statuses[statusIndex]
		statusIndex++
		statusMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"status":%d,"sentinel":"provider-response-%d-sentinel"}`, status, status)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(logicalHost+":443", server.Listener.Addr().String())
	defer proxy.Close()

	home := t.TempDir()
	caBundle := filepath.Join(home, "fake-gitlab-ca.pem")
	if err := os.WriteFile(caBundle, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	payload := map[string]string{
		"description":   "body",
		"source_branch": "fm/test/slash",
		"target_branch": "main",
		"title":         "wanted",
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(home, "request.json")
	if err := os.WriteFile(input, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	syntheticToken := strings.Join([]string{"synthetic", "official", "glab", "token"}, "-")
	controlledEnv := []string{
		"HOME=" + home,
		"GLAB_CONFIG_DIR=" + filepath.Join(home, "config"),
		"GITLAB_TOKEN=" + syntheticToken,
		"HTTPS_PROXY=" + proxy.URL,
		"NO_PROXY=",
		"SSL_CERT_FILE=" + caBundle,
		"PATH=/usr/bin:/bin",
	}
	wantCodes := map[int]uxv1.Code{
		http.StatusBadRequest:          uxv1.CodeValidation,
		http.StatusForbidden:           uxv1.CodeForbidden,
		http.StatusUnprocessableEntity: uxv1.CodeValidation,
	}
	for _, status := range statuses {
		client := NewClient(ClientConfig{Path: binary, Env: controlledEnv})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		response, requestErr := client.Do(ctx, Request{
			Operation: OpEnsureCreate,
			Host:      logicalHost,
			Repo:      "group/project",
			InputFile: input,
		})
		cancel()
		if status == http.StatusCreated {
			if requestErr != nil || response.UpstreamVersion != SupportedVersion || !response.Write {
				t.Fatalf("201 response=%#v error=%v", response, requestErr)
			}
			var returned map[string]any
			if err := json.Unmarshal(response.Body, &returned); err != nil || returned["status"] != float64(status) {
				t.Fatalf("201 body=%q error=%v", response.Body, err)
			}
		} else {
			classified := uxv1.AsError(requestErr)
			if classified == nil || classified.Code != wantCodes[status] || classified.StatusCode != status {
				t.Fatalf("status %d classified error=%#v raw=%v", status, classified, requestErr)
			}
			if strings.Contains(requestErr.Error(), fmt.Sprintf("provider-response-%d-sentinel", status)) {
				t.Fatalf("status %d leaked provider response: %v", status, requestErr)
			}
		}

		select {
		case captured := <-records:
			if captured.readErr != nil {
				t.Fatalf("status %d request body: %v", status, captured.readErr)
			}
			mediaType, _, mediaErr := mime.ParseMediaType(captured.contentType)
			if captured.method != http.MethodPost || captured.host != logicalHost || captured.requestURI != "/api/v4/projects/group%2Fproject/merge_requests" || mediaErr != nil || mediaType != "application/json" {
				t.Fatalf("status %d request method=%q host=%q uri=%q content-type=%q parse_error=%v", status, captured.method, captured.host, captured.requestURI, captured.contentType, mediaErr)
			}
			var gotPayload map[string]string
			if err := json.Unmarshal(captured.body, &gotPayload); err != nil || !reflect.DeepEqual(gotPayload, payload) {
				t.Fatalf("status %d payload=%q decoded=%v error=%v", status, captured.body, gotPayload, err)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("status %d did not reach the TLS fake server", status)
		}
	}

	statusMu.Lock()
	responses, requests := statusIndex, totalRequests
	statusMu.Unlock()
	if responses != len(statuses) || requests != len(statuses) {
		t.Fatalf("TLS fake server requests=%d responses=%d want=%d", requests, responses, len(statuses))
	}
}

// TestPinnedOfficialGlabMRViewTLS executes the pinned official mr-view command
// against an isolated TLS endpoint and proves the adapter normalizes the
// client's diff_refs source-head shape without a live GitLab read.
func TestPinnedOfficialGlabMRViewTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}

	logicalHost := "gitlab.view-contract.example"
	certificate, caPEM := selfManagedTestCertificate(t, logicalHost)
	const expectedHead = "0123456789012345678901234567890123456789"
	var requestMu sync.Mutex
	var requests []string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestMu.Lock()
		requests = append(requests, r.Method+" "+r.URL.RequestURI())
		requestMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/merge_requests/11/approval_state"):
			_, _ = w.Write([]byte(`{"rules":[]}`))
		case strings.HasSuffix(r.URL.Path, "/merge_requests/11"):
			_, _ = w.Write([]byte(`{"id":1011,"iid":11,"project_id":101,"title":"new","description":"new body","state":"opened","web_url":"https://gitlab.view-contract.example/group/project/-/merge_requests/11","source_branch":"feature","target_branch":"main","source_project_id":101,"target_project_id":101,"sha":"","diff_refs":{"base_sha":"1123456789012345678901234567890123456789","head_sha":"` + expectedHead + `","start_sha":"2123456789012345678901234567890123456789"},"detailed_merge_status":"checking"}`))
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(logicalHost+":443", server.Listener.Addr().String())
	defer proxy.Close()

	home := t.TempDir()
	caBundle := filepath.Join(home, "fake-gitlab-ca.pem")
	if err := os.WriteFile(caBundle, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	syntheticToken := strings.Join([]string{"synthetic", "view", "contract", "token"}, "-")
	client := NewClient(ClientConfig{Path: binary, Env: []string{
		"HOME=" + home,
		"GLAB_CONFIG_DIR=" + filepath.Join(home, "config"),
		"GITLAB_TOKEN=" + syntheticToken,
		"HTTPS_PROXY=" + proxy.URL,
		"NO_PROXY=",
		"SSL_CERT_FILE=" + caBundle,
		"PATH=/usr/bin:/bin",
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	response, err := client.Do(ctx, Request{Operation: OpMRView, Host: logicalHost, Repo: "group/project", IID: 11})
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err := json.Unmarshal(response.Body, &normalized); err != nil {
		t.Fatal(err)
	}
	if normalized["sha"] != expectedHead || normalized["base_sha"] != "1123456789012345678901234567890123456789" || normalized["id"] != float64(1011) || normalized["source_project_id"] != float64(101) || normalized["target_project_id"] != float64(101) {
		t.Fatalf("normalized official view=%s", response.Body)
	}
	requestMu.Lock()
	captured := append([]string(nil), requests...)
	requestMu.Unlock()
	mrReads := 0
	for _, request := range captured {
		if !strings.HasPrefix(request, "GET ") {
			t.Fatalf("official MR view made a non-GET request: %q", request)
		}
		if strings.HasPrefix(request, "GET /api/v4/projects/group%2Fproject/merge_requests/11?") {
			mrReads++
			if !strings.Contains(request, "include_diverged_commits_count=true") || !strings.Contains(request, "include_rebase_in_progress=true") || !strings.Contains(request, "render_html=true") || strings.Contains(request, syntheticToken) {
				t.Fatalf("unexpected official view request=%q", request)
			}
		}
	}
	if mrReads != 1 {
		t.Fatalf("official exact MR reads=%d requests=%q", mrReads, captured)
	}
}

// TestPinnedOfficialGlabMRDiscussionsTLS proves that discussion pages and both
// canonical project-identity lookups use only their fixed GET routes. It uses a
// synthetic runtime token and an isolated local TLS endpoint.
func TestPinnedOfficialGlabMRDiscussionsTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}

	logicalHost := "gitlab.discussions-contract.example"
	certificate, caPEM := selfManagedTestCertificate(t, logicalHost)
	records := make(chan capturedOfficialGlabMutation, 4)
	responses := map[string]string{
		"/api/v4/projects/group%2Fproject/merge_requests/42/discussions?page=2&per_page=31": `[{"id":"thread-1","individual_note":true,"notes":[]}]`,
		"/api/v4/projects/group%2Fproject":                                                  `{"id":99,"path_with_namespace":"group/project","web_url":"https://gitlab.discussions-contract.example/group/project"}`,
		"/api/v4/projects/199":                                                              `{"id":199,"path_with_namespace":"fork/project","web_url":"https://gitlab.discussions-contract.example/fork/project"}`,
	}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
		records <- capturedOfficialGlabMutation{
			method: r.Method, host: r.Host, requestURI: r.RequestURI,
			contentType: r.Header.Get("Content-Type"), body: body, readErr: readErr,
		}
		response, ok := responses[r.RequestURI]
		if !ok {
			http.Error(w, "unexpected request", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(response))
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(logicalHost+":443", server.Listener.Addr().String())
	defer proxy.Close()

	home := t.TempDir()
	caBundle := filepath.Join(home, "fake-gitlab-ca.pem")
	if err := os.WriteFile(caBundle, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	syntheticToken := strings.Join([]string{"synthetic", "discussion", "contract", "token"}, "-")
	client := NewClient(ClientConfig{Path: binary, Env: []string{
		"HOME=" + home,
		"GLAB_CONFIG_DIR=" + filepath.Join(home, "config"),
		"GITLAB_TOKEN=" + syntheticToken,
		"HTTPS_PROXY=" + proxy.URL,
		"NO_PROXY=",
		"SSL_CERT_FILE=" + caBundle,
		"PATH=/usr/bin:/bin",
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	tests := []struct {
		request  Request
		wantURI  string
		wantBody string
	}{
		{
			request:  Request{Operation: OpMRDiscussions, Host: logicalHost, Repo: "group/project", IID: 42, Page: 2, PerPage: 31},
			wantURI:  "/api/v4/projects/group%2Fproject/merge_requests/42/discussions?page=2&per_page=31",
			wantBody: `[{"id":"thread-1","individual_note":true,"notes":[]}]`,
		},
		{
			request:  Request{Operation: OpMRDiscussionsTargetProject, Host: logicalHost, Repo: "group/project"},
			wantURI:  "/api/v4/projects/group%2Fproject",
			wantBody: responses["/api/v4/projects/group%2Fproject"],
		},
		{
			request:  Request{Operation: OpMRDiscussionsSourceProject, Host: logicalHost, ID: 199},
			wantURI:  "/api/v4/projects/199",
			wantBody: responses["/api/v4/projects/199"],
		},
	}
	for _, test := range tests {
		response, err := client.Do(ctx, test.request)
		if err != nil || response.Write || response.UpstreamVersion != SupportedVersion || string(response.Body) != test.wantBody {
			t.Fatalf("operation=%s response=%#v error=%v", test.request.Operation, response, err)
		}
		select {
		case captured := <-records:
			if captured.readErr != nil || captured.method != http.MethodGet || captured.host != logicalHost || captured.requestURI != test.wantURI || len(captured.body) != 0 {
				t.Fatalf("operation=%s request=%#v", test.request.Operation, captured)
			}
			if strings.Contains(captured.requestURI, syntheticToken) || bytes.Contains(captured.body, []byte(syntheticToken)) {
				t.Fatal("discussion evidence request exposed its credential outside the authorization header")
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("operation=%s did not reach the TLS fake server", test.request.Operation)
		}
	}
	select {
	case extra := <-records:
		t.Fatalf("discussion evidence operation made an extra request: %#v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestPinnedOfficialGlabIssueEditTLS proves that exact issue-edit validation
// delegates only its fixed project, issue, and label GET routes. No synthetic
// credential reaches a URI or body, and no issue PUT exists in the adapter.
func TestPinnedOfficialGlabIssueEditTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}

	logicalHost := "gitlab.issue-edit-contract.example"
	certificate, caPEM := selfManagedTestCertificate(t, logicalHost)
	records := make(chan capturedOfficialGlabMutation, 3)
	const issueBefore = `{"id":1001,"iid":42,"project_id":101,"title":"old","description":"body","state":"opened","web_url":"https://gitlab.issue-edit-contract.example/group/project/-/issues/42","labels":["keep"],"updated_at":"2026-08-15T12:00:00Z"}`
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 64<<10))
		records <- capturedOfficialGlabMutation{
			method: r.Method, host: r.Host, requestURI: r.RequestURI,
			contentType: r.Header.Get("Content-Type"), body: body, readErr: readErr,
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method + " " + r.RequestURI {
		case "GET /api/v4/projects/group%2Fproject":
			_, _ = w.Write([]byte(`{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.issue-edit-contract.example/group/project"}`))
		case "GET /api/v4/projects/group%2Fproject/issues/42":
			_, _ = w.Write([]byte(issueBefore))
		case "GET /api/v4/projects/group%2Fproject/labels?include_ancestor_groups=true&page=2&per_page=100":
			_, _ = w.Write([]byte(`[{"id":10,"name":"triage"}]`))
		default:
			http.Error(w, "unexpected request", http.StatusNotFound)
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(logicalHost+":443", server.Listener.Addr().String())
	defer proxy.Close()

	home := t.TempDir()
	caBundle := filepath.Join(home, "fake-gitlab-ca.pem")
	if err := os.WriteFile(caBundle, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	syntheticToken := strings.Join([]string{"synthetic", "issue", "edit", "token"}, "-")
	client := NewClient(ClientConfig{Path: binary, Env: []string{
		"HOME=" + home,
		"GLAB_CONFIG_DIR=" + filepath.Join(home, "config"),
		"GITLAB_TOKEN=" + syntheticToken,
		"HTTPS_PROXY=" + proxy.URL,
		"NO_PROXY=",
		"SSL_CERT_FILE=" + caBundle,
		"PATH=/usr/bin:/bin",
	}})
	requests := []struct {
		request  Request
		wantURI  string
		wantBody string
	}{
		{request: Request{Operation: OpIssueEditProject, Host: logicalHost, Repo: "group/project"}, wantURI: "/api/v4/projects/group%2Fproject", wantBody: `{"id":101,"path_with_namespace":"group/project","web_url":"https://gitlab.issue-edit-contract.example/group/project"}`},
		{request: Request{Operation: OpIssueEditView, Host: logicalHost, Repo: "group/project", IID: 42}, wantURI: "/api/v4/projects/group%2Fproject/issues/42", wantBody: issueBefore},
		{request: Request{Operation: OpIssueEditLabelList, Host: logicalHost, Repo: "group/project", Page: 2, PerPage: 100}, wantURI: "/api/v4/projects/group%2Fproject/labels?include_ancestor_groups=true&page=2&per_page=100", wantBody: `[{"id":10,"name":"triage"}]`},
	}
	for _, test := range requests {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		response, requestErr := client.Do(ctx, test.request)
		cancel()
		if requestErr != nil || response.Write || response.UpstreamVersion != SupportedVersion || string(response.Body) != test.wantBody {
			t.Fatalf("operation=%s response=%#v error=%v", test.request.Operation, response, requestErr)
		}
		select {
		case captured := <-records:
			if captured.readErr != nil || captured.host != logicalHost || captured.requestURI != test.wantURI || captured.method != http.MethodGet || len(captured.body) != 0 || strings.Contains(captured.requestURI, syntheticToken) || bytes.Contains(captured.body, []byte(syntheticToken)) {
				t.Fatalf("operation=%s unsafe request=%#v", test.request.Operation, captured)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("operation=%s did not reach TLS fake server", test.request.Operation)
		}
	}
	select {
	case extra := <-records:
		t.Fatalf("issue-edit operation made an extra request: %#v", extra)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestPinnedOfficialGlabMRMergeTLS proves that the exact fixed merge argv sends
// one PUT with the four-key private payload for success and rejection paths.
// It uses only a synthetic runtime token and an isolated local TLS endpoint.
func TestPinnedOfficialGlabMRMergeTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}

	logicalHost := "gitlab.merge-contract.example"
	certificate, caPEM := selfManagedTestCertificate(t, logicalHost)
	statuses := []int{http.StatusOK, http.StatusMethodNotAllowed, http.StatusNotAcceptable, http.StatusConflict, http.StatusInternalServerError, http.StatusTemporaryRedirect}
	records := make(chan capturedOfficialGlabMutation, len(statuses))
	var statusMu sync.Mutex
	statusIndex := 0
	totalRequests := 0
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		records <- capturedOfficialGlabMutation{
			method: r.Method, host: r.Host, requestURI: r.RequestURI,
			contentType: r.Header.Get("Content-Type"), body: body, readErr: readErr,
		}
		statusMu.Lock()
		totalRequests++
		if statusIndex >= len(statuses) {
			statusMu.Unlock()
			http.Error(w, "unexpected request", http.StatusInternalServerError)
			return
		}
		status := statuses[statusIndex]
		statusIndex++
		statusMu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if status == http.StatusTemporaryRedirect {
			w.Header().Set("Location", "https://"+logicalHost+"/api/v4/projects/group%2Fproject/merge_requests/42/redirected")
		}
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"status":%d,"sentinel":"merge-response-%d-sentinel"}`, status, status)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(logicalHost+":443", server.Listener.Addr().String())
	defer proxy.Close()

	home := t.TempDir()
	caBundle := filepath.Join(home, "fake-gitlab-ca.pem")
	if err := os.WriteFile(caBundle, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	expectedHead := "0123456789abcdef0123456789abcdef01234567"
	payload := map[string]any{
		"sha": expectedHead, "squash": true,
		"should_remove_source_branch": false, "auto_merge": false,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(home, "merge-request.json")
	if err := os.WriteFile(input, encoded, 0o600); err != nil {
		t.Fatal(err)
	}

	syntheticToken := strings.Join([]string{"synthetic", "merge", "contract", "token"}, "-")
	controlledEnv := []string{
		"HOME=" + home,
		"GLAB_CONFIG_DIR=" + filepath.Join(home, "config"),
		"GITLAB_TOKEN=" + syntheticToken,
		"HTTPS_PROXY=" + proxy.URL,
		"NO_PROXY=",
		"SSL_CERT_FILE=" + caBundle,
		"PATH=/usr/bin:/bin",
	}
	for _, status := range statuses {
		client := NewClient(ClientConfig{Path: binary, Env: controlledEnv})
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		response, requestErr := client.Do(ctx, Request{
			Operation: OpMRMerge, Host: logicalHost, Repo: "group/project", IID: 42, InputFile: input,
		})
		cancel()
		switch status {
		case http.StatusOK:
			if requestErr != nil || !response.Write || response.UpstreamVersion != SupportedVersion {
				t.Fatalf("200 response=%#v error=%v", response, requestErr)
			}
		case http.StatusMethodNotAllowed, http.StatusNotAcceptable, http.StatusConflict:
			classified := uxv1.AsError(requestErr)
			if classified.Code != uxv1.CodeConflict || classified.StatusCode != status || strings.Contains(classified.Error(), fmt.Sprintf("merge-response-%d-sentinel", status)) {
				t.Fatalf("status %d classification=%#v raw=%v", status, classified, requestErr)
			}
		case http.StatusInternalServerError, http.StatusTemporaryRedirect:
			classified := uxv1.AsError(requestErr)
			if classified.Code != uxv1.CodeUpstream || classified.StatusCode != 0 {
				t.Fatalf("status %d became a definite outcome: %#v raw=%v", status, classified, requestErr)
			}
		}

		select {
		case captured := <-records:
			mediaType, _, mediaErr := mime.ParseMediaType(captured.contentType)
			if captured.readErr != nil || captured.method != http.MethodPut || captured.host != logicalHost || captured.requestURI != "/api/v4/projects/group%2Fproject/merge_requests/42/merge" || mediaErr != nil || mediaType != "application/json" {
				t.Fatalf("status %d request=%#v media_error=%v", status, captured, mediaErr)
			}
			var gotPayload map[string]any
			if err := json.Unmarshal(captured.body, &gotPayload); err != nil || !reflect.DeepEqual(gotPayload, payload) {
				t.Fatalf("status %d payload=%q decoded=%v error=%v", status, captured.body, gotPayload, err)
			}
			if strings.Contains(captured.requestURI, syntheticToken) || bytes.Contains(captured.body, []byte(syntheticToken)) {
				t.Fatalf("status %d exposed credential in URI/body", status)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("status %d did not reach the TLS fake server", status)
		}
	}

	statusMu.Lock()
	responses, requests := statusIndex, totalRequests
	statusMu.Unlock()
	if responses != len(statuses) || requests != len(statuses) {
		t.Fatalf("merge TLS fake requests=%d responses=%d want=%d", requests, responses, len(statuses))
	}
	if info, err := os.Stat(input); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("merge private input mode=%v error=%v", info, err)
	}
}
