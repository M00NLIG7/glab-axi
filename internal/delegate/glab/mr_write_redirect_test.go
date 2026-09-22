package glab

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Synthetic two-origin regression: a provider redirect must not forward its
// authentication to another HTTPS authority, even on the same hostname.
func TestPinnedOfficialGlabMRWriteRedirectAuthorityTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	host := "gitlab.mr-write-redirect.example"
	certificate, caPEM := selfManagedTestCertificate(t, host)
	home := t.TempDir()
	ca := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(ca, caPEM, 0600); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, "config")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "config.yml"), []byte(fmt.Sprintf("hosts:\n  %s:\n    api_protocol: https\n    ca_cert: %s\n", host, ca)), 0600); err != nil {
		t.Fatal(err)
	}
	token := strings.Join([]string{"synthetic", "redirect", "sentinel"}, "-")
	var mu sync.Mutex
	redirected, forwarded := false, false
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == host {
			w.Header().Set("Location", "https://"+host+":444/redirected")
			w.WriteHeader(http.StatusFound)
			return
		}
		mu.Lock()
		redirected = true
		forwarded = r.Header.Get("Private-Token") == token
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
	server.StartTLS()
	defer server.Close()
	proxy := newTestTLSTunnelProxy(host+":443", server.Listener.Addr().String(), host+":444")
	defer proxy.Close()
	input := filepath.Join(home, "note.json")
	if err := os.WriteFile(input, []byte(`{"body":"Ordinary synthetic note."}`), 0600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(ClientConfig{Path: binary, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + token, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + ca, "PATH=/usr/bin:/bin"}})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := client.Do(ctx, Request{Operation: OpMRNoteCreate, Host: host, Repo: "group/project", IID: 42, InputFile: input})
	mu.Lock()
	defer mu.Unlock()
	if redirected || forwarded {
		t.Fatalf("pinned note POST crossed HTTPS authority after HTTP 302: redirected=%t synthetic_credential_forwarded=%t (no real credential used)", redirected, forwarded)
	}
	if err == nil {
		t.Fatal("redirect was not rejected")
	}
}
