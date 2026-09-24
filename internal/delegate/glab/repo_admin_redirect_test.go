package glab

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// This pins negative evidence of the unchanged delegated dependency, NOT a
// production route or an upstream fix. Native feature tests require zero
// redirected requests. Both origins and credentials here are synthetic.
func TestPinnedOfficialGlabRepoAdminRedirectEvidence(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name         string
		op           string
		method, path string
		status       int
		foreign      bool
	}{
		{"create-post-302-cross-origin", "create", "POST", "/api/v4/projects", 302, true},
		{"edit-put-301-other-project", "edit", "PUT", "/api/v4/projects/101", 301, false},
		{"fork-post-307-cross-origin", "fork", "POST", "/api/v4/projects/101/fork", 307, true},
		{"create-post-308-other-project", "create", "POST", "/api/v4/projects", 308, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sourceHost, otherHost := "gitlab.selected.example", "gitlab.unapproved.example"
			sourceCert, sourceCA := selfManagedTestCertificate(t, sourceHost)
			otherCert, otherCA := selfManagedTestCertificate(t, otherHost)
			secret := strings.Join([]string{"synthetic", "project", "redirect", "sentinel"}, "-")
			var mu sync.Mutex
			sourceRequests, unapprovedRequests := 0, 0
			unapprovedMethod := ""
			tokenForwarded := false
			unapproved := func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				unapprovedRequests++
				unapprovedMethod = r.Method
				tokenForwarded = tokenForwarded || r.Header.Get("PRIVATE-TOKEN") == secret
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":999}`)
			}
			other := httptest.NewUnstartedServer(http.HandlerFunc(unapproved))
			other.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{otherCert}}
			other.StartTLS()
			defer other.Close()
			source := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != test.path {
					unapproved(w, r)
					return
				}
				mu.Lock()
				sourceRequests++
				mu.Unlock()
				if r.Method != test.method {
					t.Errorf("initial method=%s want=%s", r.Method, test.method)
				}
				host := sourceHost
				if test.foreign {
					host = otherHost
				}
				w.Header().Set("Location", "https://"+host+"/api/v4/projects/999")
				w.WriteHeader(test.status)
			}))
			source.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{sourceCert}}
			source.StartTLS()
			defer source.Close()
			proxy := repoAdminRedirectProxy(map[string]string{sourceHost + ":443": source.Listener.Addr().String(), otherHost + ":443": other.Listener.Addr().String()})
			defer proxy.Close()
			home := t.TempDir()
			caPath := filepath.Join(home, "ca.pem")
			if err := os.WriteFile(caPath, append(sourceCA, otherCA...), 0600); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(home, "config")
			if err := os.Mkdir(config, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(config, "config.yml"), []byte(fmt.Sprintf("hosts:\n  %s:\n    ca_cert: %s\n", sourceHost, caPath)), 0600); err != nil {
				t.Fatal(err)
			}
			payload := filepath.Join(home, "input.json")
			input := `{"namespace_id":21,"path":"project","name":"project","visibility":"private"}`
			if test.op == "edit" {
				input = `{"description":"new"}`
			} else if test.op == "create" {
				input = `{"namespace_id":21,"path":"project","name":"project","visibility":"private","initialize_with_readme":false}`
			}
			if err := os.WriteFile(payload, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			fixtureEnv := []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + secret, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caPath, "PATH=/usr/bin:/bin"}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			client := NewClient(ClientConfig{Path: binary, Dir: home, Env: fixtureEnv})
			if _, err := client.Version(ctx); err != nil {
				t.Fatal(err)
			}
			// Test-only direct execution retains exact historical evidence without
			// reopening these methods in the production delegated allowlist.
			cmd := exec.CommandContext(ctx, binary, "api", "--method", test.method, "--hostname", sourceHost, strings.TrimPrefix(test.path, "/api/v4/"), "--input", payload, "--header", "Content-Type: application/json")
			cmd.Dir, cmd.Env = home, sanitizedEnv(fixtureEnv, sourceHost, false)
			requestErr := cmd.Run()
			mu.Lock()
			defer mu.Unlock()
			wantForward := test.status == 301 || test.status == 302
			wantRequests := 0
			if wantForward {
				wantRequests = 1
			}
			if sourceRequests != 1 || unapprovedRequests != wantRequests || tokenForwarded != wantForward || wantForward && (unapprovedMethod != "GET" || requestErr != nil) {
				t.Fatalf("pinned redirect evidence changed: initial=%d unapproved=%d redirected_method=%s synthetic_token_forwarded=%t cross_origin=%t command_error=%v", sourceRequests, unapprovedRequests, unapprovedMethod, tokenForwarded, test.foreign, requestErr)
			}
		})
	}
}

func repoAdminRedirectProxy(addresses map[string]string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		address, ok := addresses[r.Host]
		if r.Method != http.MethodConnect || !ok {
			http.Error(w, "unapproved fixture tunnel", 400)
			return
		}
		upstream, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", 502)
			return
		}
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			upstream.Close()
			downstream.Close()
			return
		}
		if err := buffered.Flush(); err != nil {
			upstream.Close()
			downstream.Close()
			return
		}
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(upstream, downstream)
			if tcp, ok := upstream.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
			}
			close(done)
		}()
		_, _ = io.Copy(downstream, upstream)
		_ = downstream.Close()
		_ = upstream.Close()
		<-done
	}))
}
