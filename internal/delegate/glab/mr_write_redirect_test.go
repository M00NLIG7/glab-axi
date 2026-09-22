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

// Negative dependency evidence, not a safety test for the product. The new MR
// commands cannot delegate this unsafe route. Product native tests require zero
// redirected requests; this pins why the official profile is not a fallback.
func TestPinnedOfficialGlabMRWriteRedirectEvidenceTLS(t *testing.T) {
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
	if _, err := client.Version(ctx); err != nil {
		t.Fatal(err)
	}
	// Fixed test-only argv: no corresponding operation is exposed by build.
	_, err := client.runCapture(ctx, []string{"api", "--method", "POST", "--hostname", host, "projects/group%2Fproject/merge_requests/42/notes", "--input", input, "--header", "Content-Type: application/json"}, host, 4096, true, false, Operation("mr-note-redirect-evidence"))
	mu.Lock()
	defer mu.Unlock()
	if !redirected || !forwarded || err != nil {
		t.Fatalf("pinned negative evidence changed: redirected=%t synthetic_credential_forwarded=%t error=%v; refresh the dependency contract (no real credential used)", redirected, forwarded, err)
	}
}
