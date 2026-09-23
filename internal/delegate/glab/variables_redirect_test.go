package glab

import (
	"context"
	"crypto/tls"
	"encoding/json"
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

// Retains negative evidence of the pinned upstream, not a product route.
// Product CI-variable operations require native auth and have separate passing
// no-redirect/no-replay regressions. No official-profile equivalent is claimed.
func TestPinnedOfficialGlabVariableRedirectCounterevidenceTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	host, trapHost := "gitlab.variable-origin.example", "gitlab.variable-trap.example"
	cert, ca := selfManagedTestCertificate(t, host)
	trapCert, trapCA := selfManagedTestCertificate(t, trapHost)
	token := strings.Join([]string{"synthetic", "redirect", "auth"}, "_")
	value := strings.Join([]string{"synthetic", "redirect", "value"}, "_")
	type observation struct {
		method, path      string
		credential, value bool
	}
	var mu sync.Mutex
	var observed []observation
	status := 302
	location := ""
	capture := func(r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 65536))
		mu.Lock()
		observed = append(observed, observation{r.Method, r.URL.Path, r.Header.Get("PRIVATE-TOKEN") == token, strings.Contains(string(body), value)})
		mu.Unlock()
	}
	trap := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture(r)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{}`)
	}))
	trap.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{trapCert}}
	trap.StartTLS()
	defer trap.Close()
	source := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capture(r)
		if r.URL.Path == "/api/v4/projects/999/variables" {
			fmt.Fprint(w, `{}`)
			return
		}
		mu.Lock()
		s, l := status, location
		mu.Unlock()
		w.Header().Set("Location", l)
		w.WriteHeader(s)
	}))
	source.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
	source.StartTLS()
	defer source.Close()
	targets := map[string]string{host + ":443": source.Listener.Addr().String(), trapHost + ":443": trap.Listener.Addr().String()}
	// The only reachable authorities are these two synthetic loopback services.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		address, ok := targets[r.Host]
		if r.Method != http.MethodConnect || !ok {
			http.Error(w, "unexpected target", 400)
			return
		}
		up, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", 502)
			return
		}
		down, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			up.Close()
			return
		}
		defer down.Close()
		defer up.Close()
		if _, err = buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if buffered.Flush() != nil {
			return
		}
		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(up, down)
			if tcp, ok := up.(*net.TCPConn); ok {
				_ = tcp.CloseWrite()
			}
			close(done)
		}()
		_, _ = io.Copy(down, up)
		_ = down.Close()
		_ = up.Close()
		<-done
	}))
	defer proxy.Close()
	home := t.TempDir()
	config := filepath.Join(home, "config")
	if err := os.Mkdir(config, 0o700); err != nil {
		t.Fatal(err)
	}
	caFile := filepath.Join(home, "ca.pem")
	if err := os.WriteFile(caFile, append(ca, trapCA...), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, "config.yml"), []byte(fmt.Sprintf("hosts:\n  %s:\n    ca_cert: %q\n", host, caFile)), 0o600); err != nil {
		t.Fatal(err)
	}
	input := filepath.Join(home, "input.json")
	payload, _ := json.Marshal(map[string]string{"value": value})
	if err := os.WriteFile(input, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + token, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "PATH=/usr/bin:/bin", "GLAB_CHECK_UPDATE=false", "GLAB_DEBUG=false", "GLAB_DEBUG_HTTP=false"}
	client := NewClient(ClientConfig{Path: binary, Env: env})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, versionErr := client.Version(ctx)
	cancel()
	if versionErr != nil {
		t.Fatal("pinned official glab version unavailable")
	}
	for _, op := range []string{"POST", "PUT", "DELETE"} {
		for _, code := range []int{301, 302, 303, 307, 308} {
			for _, cross := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s-%d-cross-origin-%t", op, code, cross), func(t *testing.T) {
					mu.Lock()
					observed = nil
					status = code
					redirectHost := host
					if cross {
						redirectHost = trapHost
					}
					location = "https://" + redirectHost + "/api/v4/projects/999/variables"
					mu.Unlock()
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					route := "projects/101/variables"
					if op != "POST" {
						route += "/KEY?filter%5Benvironment_scope%5D=production"
					}
					args := []string{"api", "--method", op, "--hostname", host, route}
					if op != "DELETE" {
						args = append(args, "--input", input, "--header", "Content-Type: application/json")
					}
					command := exec.CommandContext(ctx, binary, args...)
					command.Env = env
					command.Stdout = io.Discard
					command.Stderr = io.Discard
					_ = command.Run()
					mu.Lock()
					got := append([]observation(nil), observed...)
					mu.Unlock()
					want := 2
					if op != "DELETE" && code >= 307 {
						want = 1
					}
					if len(got) != want {
						t.Fatalf("pinned upstream redirect behavior changed: requests=%d want=%d", len(got), want)
					}
					if want == 2 {
						method := "GET"
						if code >= 307 {
							method = "DELETE"
						}
						if got[1].method != method || got[1].path != "/api/v4/projects/999/variables" || !got[1].credential || got[1].value {
							t.Fatal("pinned upstream authority counterevidence changed")
						}
					}
				})
			}
		}
	}
}
