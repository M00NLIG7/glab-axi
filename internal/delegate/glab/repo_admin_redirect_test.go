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
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// This is a fail-closed regression, not permission to follow redirects. Both
// origins, certificate roots and credentials are synthetic and task-local.
func TestPinnedOfficialGlabRepoAdminRedirectBoundary(t *testing.T) {
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
		op           Operation
		method, path string
		status       int
		foreign      bool
	}{
		{"create-post-302-cross-origin", OpAdminCreate, "POST", "/api/v4/projects", 302, true},
		{"edit-put-301-other-project", OpAdminEdit, "PUT", "/api/v4/projects/101", 301, false},
		{"fork-post-307-cross-origin", OpAdminFork, "POST", "/api/v4/projects/101/fork", 307, true},
		{"create-post-308-other-project", OpAdminCreate, "POST", "/api/v4/projects", 308, false},
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
			if test.op == OpAdminEdit {
				input = `{"description":"new"}`
			} else if test.op == OpAdminCreate {
				input = `{"namespace_id":21,"path":"project","name":"project","visibility":"private","initialize_with_readme":false}`
			}
			if err := os.WriteFile(payload, []byte(input), 0600); err != nil {
				t.Fatal(err)
			}
			client := NewClient(ClientConfig{Path: binary, Dir: home, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + secret, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caPath, "PATH=/usr/bin:/bin"}})
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, requestErr := client.Do(ctx, Request{Operation: test.op, Host: sourceHost, Repo: "team/sub/project", ID: 101, InputFile: payload})
			mu.Lock()
			defer mu.Unlock()
			if sourceRequests != 1 || unapprovedRequests != 0 {
				t.Fatalf("redirect boundary violated: initial=%d unapproved=%d redirected_method=%s synthetic_token_forwarded=%t cross_origin=%t adapter_error=%v", sourceRequests, unapprovedRequests, unapprovedMethod, tokenForwarded, test.foreign, requestErr)
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
