package glab

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
)

// Exercise each public executable through the actual pinned glab and a local
// TLS provider. This catches child-environment warnings that process fixtures
// and direct classification tests cannot reproduce on their own.
func TestPinnedOfficialGlabApprovalCLIAvailabilityTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	if runtime.GOOS == "windows" {
		t.Skip("PATH fixture uses a symlink")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	const host = "gitlab.approval-cli.example"
	certificate, ca := selfManagedTestCertificate(t, host)
	secret := strings.Join([]string{"synthetic", "approval", "cli", "token"}, "-")
	var mu sync.Mutex
	status := http.StatusForbidden
	body := `{"message":"provider-secret"}`
	var requests []string
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests = append(requests, r.Method+" "+r.URL.Path)
		currentStatus, currentBody := status, body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.Host != host || r.Header.Get("PRIVATE-TOKEN") != secret {
			http.Error(w, "unexpected authority or mutation", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/api/v4/projects/group/project":
			fmt.Fprintf(w, `{"id":99,"path_with_namespace":"group/project","web_url":"https://%s/group/project"}`, host)
		case "/api/v4/projects/group/project/merge_requests/7":
			fmt.Fprintf(w, `{"id":7007,"iid":7,"project_id":99,"source_project_id":99,"target_project_id":99,"source_branch":"feature","target_branch":"main","web_url":"https://%s/group/project/-/merge_requests/7","updated_at":"2024-02-03T04:05:06Z","diff_refs":{"base_sha":"1111111111111111111111111111111111111111","head_sha":"2222222222222222222222222222222222222222"},"reviewers":[]}`, host)
		case "/api/v4/projects/group/project/merge_requests/7/approval_state":
			fmt.Fprint(w, `{"rules":[]}`)
		case "/api/v4/projects/group/project/merge_requests/7/approvals":
			w.WriteHeader(currentStatus)
			fmt.Fprint(w, currentBody)
		default:
			http.Error(w, "unexpected route", http.StatusBadRequest)
		}
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(host+":443", server.Listener.Addr().String())
	defer proxy.Close()

	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			home := t.TempDir()
			configDir := filepath.Join(home, "config")
			if err := os.Mkdir(configDir, 0o700); err != nil {
				t.Fatal(err)
			}
			caPath := filepath.Join(home, "ca.pem")
			if err := os.WriteFile(caPath, ca, 0o600); err != nil {
				t.Fatal(err)
			}
			config := fmt.Sprintf("hosts:\n  %s:\n    ca_cert: %q\n", host, caPath)
			if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(config), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(binary, filepath.Join(home, "glab")); err != nil {
				t.Fatal(err)
			}
			cli := filepath.Join(home, program)
			build := exec.Command("go", "build", "-p", "1", "-trimpath", "-o", cli, "./cmd/"+program)
			build.Dir = root
			if output, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v: %s", err, output)
			}
			for _, test := range []struct {
				name, body, reason string
				status, exit       int
				available          bool
			}{
				{name: "denied", status: 403, body: `{"message":"provider-secret"}`, reason: "access_denied"},
				{name: "unavailable", status: 404, body: `{"message":"provider-secret"}`, reason: "not_found_or_unsupported"},
				{name: "misleading message", status: 500, body: `{"message":"Upstream returned HTTP 403 provider-secret"}`, exit: 8},
				{name: "singleton errors", status: 500, body: `{"errors":["provider-secret (HTTP 403)"]}`, exit: 4},
				{name: "approved", status: 200, body: `{"id":7007,"iid":7,"project_id":99,"approved":true,"approved_by":[]}`, available: true},
			} {
				t.Run(test.name, func(t *testing.T) {
					mu.Lock()
					status, body, requests = test.status, test.body, nil
					mu.Unlock()
					ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, cli, "mr", "approvals", "7", "-R", "group/project", "--hostname", host, "--format", "json")
					command.Dir = home
					command.Env = []string{"PATH=" + home + ":/usr/bin:/bin", "HOME=" + home, "GLAB_CONFIG_DIR=" + configDir, "GITLAB_TOKEN=" + secret, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caPath, "GOMAXPROCS=2", "NO_PROMPT=1", "PROMPT_DISABLED=1", "GLAB_NO_PROMPT=false"}
					var stdout, stderr bytes.Buffer
					command.Stdout, command.Stderr = &stdout, &stderr
					runErr := command.Run()
					exit := 0
					if runErr != nil {
						if exitErr, ok := runErr.(*exec.ExitError); ok {
							exit = exitErr.ExitCode()
						} else {
							t.Fatal(runErr)
						}
					}
					if ctx.Err() != nil || exit != test.exit || stderr.Len() != 0 {
						t.Fatalf("exit=%d want=%d context=%v stderr=%s stdout=%s", exit, test.exit, ctx.Err(), stderr.String(), stdout.String())
					}
					var envelope struct {
						OK   bool `json:"ok"`
						Data struct {
							Approvals struct{ Availability, State, Tier, Reason string } `json:"approvals"`
						} `json:"data"`
						Meta uxv1.Meta `json:"meta"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
						t.Fatal(err)
					}
					if envelope.OK != (test.exit == 0) || strings.Contains(stdout.String(), secret) || strings.Contains(stdout.String(), "provider-secret") {
						t.Fatalf("unexpected envelope: %s", stdout.String())
					}
					if test.exit == 0 {
						approval := envelope.Data.Approvals
						if test.available {
							if approval.Availability != "available" || approval.State != "approved" || !envelope.Meta.Complete {
								t.Fatalf("ordinary success changed: %s", stdout.String())
							}
						} else if approval.Availability != "unavailable" || approval.State != "unknown" || approval.Reason != test.reason || approval.Tier != "unknown" || envelope.Meta.Complete {
							t.Fatalf("unavailable evidence changed: %s", stdout.String())
						}
					}
					mu.Lock()
					defer mu.Unlock()
					if len(requests) == 0 {
						t.Fatal("no real provider requests")
					}
					for _, request := range requests {
						if !strings.HasPrefix(request, "GET /api/v4/projects/group/project") {
							t.Fatalf("unexpected request: %s", request)
						}
					}
				})
			}
		})
	}
}
