package glab

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// Diagnostic only, not an approved product download route or safety acceptance
// test. The assertions characterize the exact pinned executable. All provider
// traffic uses two local TLS fixtures, a fresh profile, and runtime sentinels.
func TestDownloadTransportProbe(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("supply pinned binary for the download transport diagnostic")
	}
	for _, args := range [][]string{{"version"}, {"api", "--help"}, {"job", "artifact", "--help"}, {"ci", "artifact", "--help"}, {"release", "download", "--help"}} {
		home := t.TempDir()
		cmd := exec.Command(binary, args...)
		cmd.Env = sanitizedEnv([]string{"HOME=" + home, "GLAB_CONFIG_DIR=" + filepath.Join(home, "config"), "PATH=/usr/bin:/bin"}, "", false)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("help/version %v: %v", args, err)
		}
		t.Logf("offline command=%v\n%s", args, out)
	}
	for _, tc := range []downloadProbeCase{
		{name: "api-artifact-pat-direct", command: "api-artifact", auth: "pat"},
		{name: "api-artifact-pat-cross", command: "api-artifact", auth: "pat", redirect: true},
		{name: "api-release-pat-cross", command: "api-release", auth: "pat", redirect: true},
		{name: "job-artifact-pat-direct", command: "job", auth: "pat"},
		{name: "job-artifact-pat-cross", command: "job", auth: "pat", redirect: true},
		{name: "ci-artifact-pat-cross", command: "ci", auth: "pat", redirect: true},
		{name: "release-pat-direct", command: "release", auth: "pat"},
		{name: "release-pat-cross", command: "release", auth: "pat", redirect: true},
		{name: "api-artifact-oauth-cross", command: "api-artifact", auth: "oauth", redirect: true},
		{name: "job-artifact-oauth-cross", command: "job", auth: "oauth", redirect: true},
		{name: "release-oauth-cross", command: "release", auth: "oauth", redirect: true},
		{name: "api-public-cross", command: "api-artifact", redirect: true},
		{name: "job-public-cross", command: "job", redirect: true},
		{name: "release-public-cross", command: "release", redirect: true},
		{name: "native-no-redirect-control", command: "no-redirect", auth: "pat", redirect: true},
		{name: "api-no-redirect-flag", command: "api-artifact", auth: "pat", redirect: true, flag: "--no-redirect"},
		{name: "job-no-redirect-flag", command: "job", auth: "pat", redirect: true, flag: "--no-redirect"},
		{name: "release-no-redirect-flag", command: "release", auth: "pat", redirect: true, flag: "--no-redirect"},
		{name: "job-stdout-flag", command: "job", auth: "pat", flag: "--output=-"},
		{name: "release-stdout-flag", command: "release", auth: "pat", flag: "--output=-"},
		{name: "job-existing-file", command: "job", auth: "pat", existing: true},
		{name: "release-existing-file", command: "release", auth: "pat", existing: true},
		{name: "job-parent-symlink", command: "job", auth: "pat", symlink: true},
		{name: "release-partial-transfer", command: "release", auth: "pat", partial: true},
	} {
		t.Run(tc.name, func(t *testing.T) { runDownloadProbeCase(t, binary, tc) })
	}
}

type downloadProbeCase struct {
	name, command, auth, flag            string
	redirect, existing, symlink, partial bool
}

type downloadProbeRequest struct {
	Origin        string
	URI           string
	PrivateToken  bool
	JobToken      bool
	Authorization bool
}

func runDownloadProbeCase(t *testing.T, binary string, tc downloadProbeCase) {
	t.Helper()
	if tc.symlink && runtime.GOOS == "windows" {
		t.Skip("symlink fixture requires unprivileged symlinks")
	}
	token := strings.Join([]string{"synthetic", "download", "probe"}, "-")
	var zipped bytes.Buffer
	zw := zip.NewWriter(&zipped)
	entryName := "ok.txt"
	if tc.symlink {
		entryName = "sub/ok.txt"
	}
	entry, err := zw.Create(entryName)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, "synthetic-download"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var records []downloadProbeRequest
	record := func(origin string, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		records = append(records, downloadProbeRequest{origin, r.RequestURI, r.Header.Get("Private-Token") == token, r.Header.Get("Job-Token") == token, strings.Contains(r.Header.Get("Authorization"), token)})
	}
	writeBody := func(w http.ResponseWriter) {
		w.Header().Set("Content-Type", "application/zip")
		if tc.partial {
			w.Header().Set("Content-Length", fmt.Sprint(zipped.Len()+100))
		}
		_, _ = w.Write(zipped.Bytes())
	}
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record("target", r)
		writeBody(w)
	}))
	defer target.Close()
	logicalHost := "gitlab.download-probe.example"
	certificate, caPEM := selfManagedTestCertificate(t, logicalHost)
	origin := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record("origin", r)
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		switch r.RequestURI {
		case "/api/v4/projects/group%2Fproject/releases/v1%2E0":
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"tag_name":"v1.0","name":"release","assets":{"links":[{"id":7,"name":"asset.zip","url":"https://%s/api/v4/projects/group%%2Fproject/releases/v1.0/downloads/asset.zip","link_type":"package"}],"sources":[]}}`, logicalHost)
		case "/api/v4/projects/group%2Fproject/jobs/42/artifacts", "/api/v4/projects/group%2Fproject/releases/v1.0/downloads/asset.zip", "/api/v4/projects/group%2Fproject/jobs/artifacts/main/download?job=build":
			if tc.redirect {
				http.Redirect(w, r, target.URL+"/synthetic.zip", http.StatusFound)
				return
			}
			writeBody(w)
		default:
			http.Error(w, "unexpected route", http.StatusBadRequest)
		}
	}))
	origin.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	origin.StartTLS()
	defer origin.Close()
	proxy := newTestTLSTunnelProxy(logicalHost+":443", origin.Listener.Addr().String())
	defer proxy.Close()
	home := t.TempDir()
	bundle := append(caPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: target.Certificate().Raw})...)
	caPath := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(caPath, bundle, 0o600); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(home, "config")
	if err := os.Mkdir(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// macOS uses platform trust rather than SSL_CERT_FILE. The new fixture
	// supplies only a public test CA, never a real profile or credential.
	config := fmt.Sprintf("hosts:\n  %s:\n    ca_cert: %q\n", logicalHost, caPath)
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(home, "destination")
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	fileName := "ok.txt"
	if tc.command == "release" {
		fileName = "asset.zip"
	}
	if tc.existing {
		if err := os.WriteFile(filepath.Join(destination, fileName), []byte("caller-file"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(home, "outside-destination")
	if tc.symlink {
		if err := os.Mkdir(outside, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, filepath.Join(destination, "sub")); err != nil {
			t.Fatal(err)
		}
	}
	args := []string{"api", "--method", "GET", "--hostname", logicalHost, "projects/group%2Fproject/jobs/42/artifacts"}
	switch tc.command {
	case "api-release":
		args[len(args)-1] = "projects/group%2Fproject/releases/v1.0/downloads/asset.zip"
	case "job", "ci":
		args = []string{tc.command, "artifact", "main", "build", "--repo", "https://" + logicalHost + "/group/project", "--path", destination}
	case "release":
		args = []string{"release", "download", "v1.0", "--repo", "https://" + logicalHost + "/group/project", "--asset-name", "asset.zip", "--dir", destination}
	}
	if tc.flag != "" {
		args = append(args, tc.flag)
	}
	env := []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + configDir, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=127.0.0.1", "SSL_CERT_FILE=" + caPath, "PATH=/usr/bin:/bin"}
	if tc.auth != "" {
		env = append(env, "GITLAB_TOKEN="+token)
	}
	if tc.auth == "oauth" {
		env = append(env, "GLAB_IS_OAUTH2=true")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	var runErr error
	if tc.command == "no-redirect" {
		args = []string{"test-only-http-client", "GET", "origin/artifacts", "CheckRedirect=ErrUseLastResponse"}
		// Independent proven control: the same local TLS origin and sentinel,
		// but stop before following Location. This is NOT a proposed product
		// credential path; no opaque profile is consulted or extracted.
		client := origin.Client()
		client.Transport.(*http.Transport).TLSClientConfig.ServerName = logicalHost
		client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		req, err := http.NewRequestWithContext(ctx, "GET", origin.URL+"/api/v4/projects/group%2Fproject/jobs/42/artifacts", nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Private-Token", token)
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusFound {
			t.Fatalf("control status=%d", res.StatusCode)
		}
	} else {
		cmd := exec.CommandContext(ctx, binary, args...)
		cmd.Dir = home
		cmd.Env = sanitizedEnv(env, logicalHost, false)
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		runErr = cmd.Run()
	}
	mu.Lock()
	captured := append([]downloadProbeRequest(nil), records...)
	mu.Unlock()
	t.Logf("command=%v success=%t requests=%+v", args, runErr == nil, captured)
	if strings.Contains(stdout.String(), token) || strings.Contains(stderr.String(), token) {
		t.Fatal("sentinel appeared in CLI output")
	}
	if tc.flag != "" {
		if runErr == nil || len(captured) != 0 || !strings.Contains(strings.ToLower(stdout.String()+stderr.String()), "unknown flag") {
			t.Fatalf("unsupported flag was not rejected before network: err=%v stdout=%s stderr=%s", runErr, stdout.String(), stderr.String())
		}
		return
	}
	if tc.command == "no-redirect" {
		if len(captured) != 1 || captured[0].Origin != "origin" || !captured[0].PrivateToken {
			t.Fatalf("control requests=%+v", captured)
		}
		return
	}
	if tc.partial || tc.command == "release" && tc.existing {
		if runErr == nil {
			t.Fatal("expected dedicated release failure")
		}
	} else if runErr != nil {
		t.Fatalf("unexpected command failure: %v stderr=%s", runErr, stderr.String())
	} else if !tc.symlink {
		if strings.HasPrefix(tc.command, "api") {
			if !bytes.Equal(stdout.Bytes(), zipped.Bytes()) {
				t.Fatal("raw archive bytes mismatch")
			}
		} else {
			body, err := os.ReadFile(filepath.Join(destination, fileName))
			want := []byte("synthetic-download")
			if tc.command == "release" {
				want = zipped.Bytes()
			}
			if err != nil || !bytes.Equal(body, want) {
				t.Fatalf("downloaded bytes mismatch: err=%v", err)
			}
		}
	}
	if tc.redirect {
		if len(captured) == 0 || captured[len(captured)-1].Origin != "target" {
			t.Fatal("no redirected request")
		}
		got := captured[len(captured)-1]
		// Both paths send only their auth source's header. PAT survives the
		// cross-origin redirect; OAuth's Authorization is stripped here.
		want := downloadProbeRequest{Origin: "target", URI: "/synthetic.zip"}
		if tc.auth == "pat" {
			want.PrivateToken = true
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("redirect observation changed: got=%+v want=%+v", got, want)
		}
	}
	if tc.existing {
		body, err := os.ReadFile(filepath.Join(destination, fileName))
		if err != nil {
			t.Fatal(err)
		}
		preserved := string(body) == "caller-file"
		t.Logf("caller_file_preserved=%t", preserved)
		if preserved != (tc.command == "release") {
			t.Fatal("unexpected existing-file behavior")
		}
	}
	if tc.symlink {
		body, err := os.ReadFile(filepath.Join(outside, "ok.txt"))
		if err != nil || string(body) != "synthetic-download" {
			t.Fatalf("symlink probe body=%q err=%v", body, err)
		}
		t.Log("intermediate_symlink_followed=true external_write_within_test_home=true")
	}
	if tc.partial {
		body, err := os.ReadFile(filepath.Join(destination, "asset.zip"))
		if err != nil || !bytes.Equal(body, zipped.Bytes()) {
			t.Fatalf("partial file=%d err=%v", len(body), err)
		}
		t.Logf("failed_transfer_left_partial_file=true bytes=%d", len(body))
	}
}
