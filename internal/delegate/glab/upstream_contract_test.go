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

	"glab-axi/internal/contract/uxv1"
)

// TestPinnedUpstreamPublicContract executes only version/help against the
// checksum-verified official package. CI supplies the binary; no profile,
// credential, repository, or GitLab API is involved.
func TestPinnedUpstreamPublicContract(t *testing.T) {
	binary := os.Getenv("GLAB_AXI_OFFICIAL_GLAB_TEST_BINARY")
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
		Subject:               pkix.Name{CommonName: "glab-axi official-glab test CA"},
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
	binary := os.Getenv("GLAB_AXI_OFFICIAL_GLAB_TEST_BINARY")
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
