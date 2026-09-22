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
	"strconv"
	"strings"
	"testing"
	"time"
)

// The public pinned CLI executes each actual issue mutation. Two isolated TLS
// authorities (different hostnames) prove that a redirected GET is still an
// unauthorized request, even if the CLI does not replay the original body.
// A missing/censored mutation response cannot undo credential forwarding.
// This characterizes the unsafe pinned dependency, NOT a supported issue-write
// backend. Public issue writes require the separate explicit native path.
func TestPinnedOfficialGlabIssueWritesRedirectCharacterization(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	var err error
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range []Operation{OpIssueCreate, OpIssueNoteCreate, OpIssueState} {
		for _, status := range []int{301, 302, 303, 307, 308} {
			t.Run(string(op)+"/"+strconv.Itoa(status), func(t *testing.T) {
				token := strings.Join([]string{"synthetic", "issue", "redirect", "only"}, "-")
				type requestEvidence struct {
					Method, Path      string
					HasSyntheticToken bool
					BodyBytes         int
				}
				original := make(chan requestEvidence, 8)
				redirected := make(chan requestEvidence, 8)
				capture := func(r *http.Request) requestEvidence {
					body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
					if err != nil {
						t.Error(err)
					}
					return requestEvidence{r.Method, r.URL.Path, r.Header.Get("Private-Token") == token, len(body)}
				}
				const sourceHost = "gitlab.issue-redirect-contract.example"
				const destinationHost = "other.issue-redirect-contract.example"
				sourceCertificate, sourceCA := selfManagedTestCertificate(t, sourceHost)
				destinationCertificate, destinationCA := selfManagedTestCertificate(t, destinationHost)
				destination := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					redirected <- capture(r)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"unapproved_destination":true}`)
				}))
				destination.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{destinationCertificate}}
				destination.StartTLS()
				defer destination.Close()
				source := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					original <- capture(r)
					w.Header().Set("Location", "https://"+destinationHost+"/api/v4/projects/999/issues/900/notes")
					w.WriteHeader(status)
				}))
				source.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{sourceCertificate}}
				source.StartTLS()
				defer source.Close()
				sourceTunnel := newTestTLSTunnelProxy(sourceHost+":443", source.Listener.Addr().String())
				defer sourceTunnel.Close()
				destinationTunnel := newTestTLSTunnelProxy(destinationHost+":443", destination.Listener.Addr().String())
				defer destinationTunnel.Close()
				proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch r.Host {
					case sourceHost + ":443":
						sourceTunnel.Config.Handler.ServeHTTP(w, r)
					case destinationHost + ":443":
						destinationTunnel.Config.Handler.ServeHTTP(w, r)
					default:
						http.Error(w, "unapproved fixture authority", http.StatusBadRequest)
					}
				}))
				defer proxy.Close()
				home := t.TempDir()
				config := filepath.Join(home, "config")
				if err := os.Mkdir(config, 0700); err != nil {
					t.Fatal(err)
				}
				ca := filepath.Join(home, "local-only-ca.pem")
				roots := append(sourceCA, destinationCA...)
				if err := os.WriteFile(ca, roots, 0600); err != nil {
					t.Fatal(err)
				}
				profile := fmt.Sprintf("hosts:\n  %s:\n    ca_cert: %s\n", strconv.Quote(sourceHost), strconv.Quote(ca))
				if err := os.WriteFile(filepath.Join(config, "config.yml"), []byte(profile), 0600); err != nil {
					t.Fatal(err)
				}
				input := filepath.Join(home, "payload.json")
				body := `{"body":"plain issue comment"}`
				method := "POST"
				endpoint := "/api/v4/projects/101/issues/42/notes"
				switch op {
				case OpIssueCreate:
					body = `{"title":"ordinary issue","description":"plain body","issue_type":"issue"}`
					endpoint = "/api/v4/projects/101/issues"
				case OpIssueState:
					body = `{"state_event":"close"}`
					method = "PUT"
					endpoint = "/api/v4/projects/101/issues/42"
				}
				if err := os.WriteFile(input, []byte(body), 0600); err != nil {
					t.Fatal(err)
				}
				client := NewClient(ClientConfig{Path: binary, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + token, "PATH=/usr/bin:/bin", "HTTPS_PROXY=" + proxy.URL, "NO_PROXY="}})
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				response, requestErr := client.Do(ctx, Request{Operation: op, Host: sourceHost, Repo: "group/project", ProjectID: 101, IID: 42, InputFile: input})
				select {
				case got := <-original:
					if got.Method != method || got.Path != endpoint || !got.HasSyntheticToken || got.BodyBytes != len(body) {
						t.Fatalf("original request did not exercise pinned mutation: %+v error=%v", got, requestErr)
					}
				default:
					t.Fatalf("original mutation did not arrive: write=%t error=%v", response.Write, requestErr)
				}
				select {
				case got := <-original:
					t.Errorf("original mutation repeated: %+v", got)
				default:
				}
				if status == 301 || status == 302 || status == 303 {
					select {
					case got := <-redirected:
						if got.Method != http.MethodGet || got.Path != "/api/v4/projects/999/issues/900/notes" || !got.HasSyntheticToken || got.BodyBytes != 0 || requestErr != nil {
							t.Fatalf("pinned unsafe redirect characterization changed: status=%d evidence=%+v error=%v", status, got, requestErr)
						}
					default:
						t.Fatal("pinned dependency no longer reproduced the documented unsafe redirect; refresh its evidence before changing the product boundary")
					}
				}
				select {
				case got := <-redirected:
					t.Fatalf("unexpected additional redirect request: status=%d evidence=%+v", status, got)
				default:
				}
			})
		}
	}
}
