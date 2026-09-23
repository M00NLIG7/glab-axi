package glab

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPinnedOfficialGlabDeleteRedirectDiagnosticTLS characterizes the UNSAFE
// pinned dependency, not the product-native deletion path. Diagnostic PASS
// records that delegated DELETE still replays to a sibling on 307/308. Native
// feature tests require the opposite. No real resources or credentials exist.
func TestPinnedOfficialGlabDeleteRedirectDiagnosticTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	host := "gitlab.delete-contract.example"
	certificate, ca := selfManagedTestCertificate(t, host)
	for _, status := range []int{http.StatusNoContent, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.Method+" "+r.RequestURI)
				mu.Unlock()
				if strings.HasSuffix(r.URL.Path, "/42") && status != http.StatusNoContent {
					w.Header().Set("Location", "https://"+host+"/api/v4/projects/999/issues/43")
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusNoContent)
			}))
			server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
			server.StartTLS()
			defer server.Close()
			proxy := newTestTLSTunnelProxy(host+":443", server.Listener.Addr().String())
			defer proxy.Close()
			home := t.TempDir()
			caPath := filepath.Join(home, "ca.pem")
			if err := os.WriteFile(caPath, ca, 0600); err != nil {
				t.Fatal(err)
			}
			config := filepath.Join(home, "config")
			if err := os.Mkdir(config, 0700); err != nil {
				t.Fatal(err)
			}
			// Only a synthetic CA profile, no stored token or real credential access.
			if err := os.WriteFile(filepath.Join(config, "config.yml"), []byte("hosts:\n  "+host+":\n    api_protocol: https\n    ca_cert: "+caPath+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			client := NewClient(ClientConfig{Path: binary, Dir: home, Env: []string{
				"HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + strings.Join([]string{"synthetic", "delete", "token"}, "-"),
				"HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "PATH=/usr/bin:/bin",
			}})
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if _, err := client.Version(ctx); err != nil {
				t.Fatal(err)
			}
			_, requestErr := client.runCapture(ctx, []string{"api", "--method", "DELETE", "--hostname", host, "projects/101/issues/42", "--include"}, host, 65536, true, false, "")
			mu.Lock()
			got := append([]string(nil), requests...)
			mu.Unlock()
			want := 1
			if status != http.StatusNoContent {
				want = 2
			}
			if len(got) != want || got[0] != "DELETE /api/v4/projects/101/issues/42" || want == 2 && got[1] != "DELETE /api/v4/projects/999/issues/43" || requestErr != nil {
				t.Fatalf("pinned delegated diagnostic changed: requests=%q error=%v", got, requestErr)
			}
		})
	}
}
