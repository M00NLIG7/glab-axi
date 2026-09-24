package glab

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
)

func writeIssueEditTestCAProfile(t *testing.T, home, host, caPath string) {
	t.Helper()
	// macOS ignores SSL_CERT_FILE when the platform verifier is active. This
	// isolated, credential-free official profile pins only our synthetic CA.
	dir := filepath.Join(home, "config")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	profile := fmt.Sprintf("hosts:\n  %s:\n    ca_cert: %q\n", host, caPath)
	if err := os.WriteFile(filepath.Join(dir, "config.yml"), []byte(profile), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Negative dependency evidence only. Product builders reject this mutation;
// native issue-edit tests must prove that these redirects cannot be followed.
func testPinnedOfficialGlabIssueEditMutationTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	const host = "gitlab.issue-edit-contract.example"
	cert, ca := selfManagedTestCertificate(t, host)
	for _, mode := range []string{"200", "400", "401", "403", "404", "409", "429", "500", "301", "302", "303", "307", "308", "malformed", "lost", "deadline", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			var attempts atomic.Int32
			records := make(chan capturedOfficialGlabMutation, 8)
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				attempt := attempts.Add(1)
				body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
				select {
				case records <- capturedOfficialGlabMutation{method: r.Method, host: r.Host, requestURI: r.RequestURI, contentType: r.Header.Get("Content-Type"), body: body, readErr: err}:
				default:
				}
				// A redirect regression must fail promptly, never trap cleanup in
				// an infinite redirect loop or a blocked evidence channel.
				if attempt > 1 {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				switch mode {
				case "200":
					_, _ = w.Write([]byte(`{"id":1001,"iid":42,"project_id":101,"title":"new title","labels":["keep","triage"]}`))
				case "malformed":
					_, _ = w.Write([]byte(`{"id":`))
				case "lost":
					conn, _, err := w.(http.Hijacker).Hijack()
					if err == nil {
						_ = conn.Close()
					}
				case "deadline", "cancel":
					<-r.Context().Done()
				default:
					status := 0
					_, _ = fmt.Sscan(mode, &status)
					if status >= 300 && status < 400 {
						w.Header().Set("Location", "https://"+host+"/api/v4/projects/101/issues/42/redirected")
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"synthetic upstream detail"}`))
				}
			}))
			server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
			server.StartTLS()
			defer server.Close()
			proxy := newTestTLSTunnelProxy(host+":443", server.Listener.Addr().String())
			defer proxy.Close()
			home := t.TempDir()
			caPath := filepath.Join(home, "ca.pem")
			if err := os.WriteFile(caPath, ca, 0o600); err != nil {
				t.Fatal(err)
			}
			writeIssueEditTestCAProfile(t, home, host, caPath)
			payload := []byte(`{"title":"new title","description":"","add_labels":"triage","remove_labels":"bug"}`)
			input := filepath.Join(home, "input.json")
			if err := os.WriteFile(input, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			token := strings.Join([]string{"synthetic", "issue", "mutation"}, "-")
			client := NewClient(ClientConfig{Path: binary, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + filepath.Join(home, "config"), "GITLAB_TOKEN=" + token, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caPath, "PATH=/usr/bin:/bin"}})
			// Verify version before shortening the mutation context.
			if _, err := client.Version(context.Background()); err != nil {
				t.Fatal(err)
			}
			duration := 5 * time.Second
			if mode == "deadline" {
				duration = time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), duration)
			defer cancel()
			if mode == "deadline" {
				// An operation deadline includes child startup. Requiring a PUT
				// before this timer fires was a scheduler-dependent assertion.
				// Pin expiry before launch and require exactly zero requests; the
				// cancel case separately pins termination after exactly one PUT.
				// Keep the one-second deadline, not a larger startup allowance.
				<-ctx.Done()
			}
			type result struct {
				response Response
				err      error
			}
			done := make(chan result, 1)
			go func() {
				args := []string{"api", "--method", "PUT", "--hostname", host, "projects/101/issues/42", "--input", input, "--header", "Content-Type: application/json"}
				body, err := client.runCapture(ctx, args, host, 2<<20, true, false, OpIssueEditUpdate)
				done <- result{Response{Body: body, Write: true, UpstreamVersion: SupportedVersion}, err}
			}()
			if mode != "deadline" {
				select {
				case record := <-records:
					if record.method != "PUT" || record.host != host || record.requestURI != "/api/v4/projects/101/issues/42" || record.contentType != "application/json" || record.readErr != nil || !bytes.Equal(record.body, payload) || strings.Contains(record.requestURI+string(record.body), token) {
						t.Fatalf("wrong wire request: %#v", record)
					}
					if mode == "cancel" {
						cancel()
					}
				case <-time.After(6 * time.Second):
					t.Fatal("no TLS request")
				}
			}
			select {
			case got := <-done:
				if !got.response.Write || (mode == "200" || mode == "malformed") != (got.err == nil) {
					t.Fatalf("response=%#v err=%v", got.response, got.err)
				}
				if mode == "deadline" && (!errors.Is(got.err, context.DeadlineExceeded) || uxv1.AsError(got.err).Code != uxv1.CodeUpstream) {
					t.Fatalf("pre-launch deadline not preserved: %v", got.err)
				}
				if mode == "cancel" && (!errors.Is(got.err, context.Canceled) || uxv1.AsError(got.err).Code != uxv1.CodeCanceled) {
					t.Fatalf("in-flight cancellation not preserved: %v", got.err)
				}
				if got.err != nil && (strings.Contains(got.err.Error(), token) || strings.Contains(got.err.Error(), "synthetic upstream detail")) {
					t.Fatal("raw error leaked")
				}
			case <-time.After(6 * time.Second):
				t.Fatal("unbounded child")
			}
			wantRequests := int32(1)
			if mode == "deadline" {
				wantRequests = 0
			}
			if mode == "301" || mode == "302" || mode == "303" {
				wantRequests = 2 // pinned defect, never an allowed product behavior
				select {
				case followup := <-records:
					if followup.method != "GET" || followup.requestURI != "/api/v4/projects/101/issues/42/redirected" || len(followup.body) != 0 {
						t.Fatalf("unexpected upstream redirect behavior: %#v", followup)
					}
				default:
					t.Fatal("pinned official redirect behavior changed; refresh the dependency evidence")
				}
			}
			if attempts.Load() != wantRequests {
				t.Fatalf("requests=%d want=%d", attempts.Load(), wantRequests)
			}
		})
	}
}
