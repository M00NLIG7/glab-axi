package glab

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
)

type issueWriteFixture struct {
	Schema       string `json:"schema"`
	OfficialGlab string `json:"official_glab"`
	ProjectID    int64  `json:"project_id"`
	IssueID      int64  `json:"issue_id"`
	IID          int64  `json:"iid"`
	Operations   []struct {
		Name           Operation      `json:"name"`
		Method         string         `json:"method"`
		Endpoint       string         `json:"endpoint"`
		Payload        map[string]any `json:"payload"`
		ResponseFields []string       `json:"response_fields"`
	} `json:"operations"`
}

func loadIssueWriteFixture(t *testing.T) issueWriteFixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "official-glab", "v1.112.0", "issue-writes.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture issueWriteFixture
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "glab-axi/issue-write-provider-contract/v1" || fixture.OfficialGlab != SupportedVersion || len(fixture.Operations) != 4 || fixture.ProjectID < 1 || fixture.IssueID < 1 || fixture.IID < 1 {
		t.Fatalf("fixture=%+v", fixture)
	}
	return fixture
}

func TestIssueWritesPinnedProviderBuilders(t *testing.T) {
	fixture := loadIssueWriteFixture(t)
	input := filepath.Join(t.TempDir(), "private.json")
	for _, op := range fixture.Operations {
		request := Request{Operation: op.Name, Host: "gitlab.example.invalid", Repo: "group/project", ProjectID: fixture.ProjectID, IID: fixture.IID, InputFile: input}
		invocation, err := build(request)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"api", "--method", op.Method, "--hostname", request.Host, op.Endpoint, "--input", input, "--header", "Content-Type: application/json"}
		if !reflect.DeepEqual(invocation.args, want) || !invocation.write || invocation.maxStdout != limits.MaxJSONPageBytes {
			t.Fatalf("invocation=%+v", invocation)
		}
		for _, invalid := range []string{"project ID", "IID", "file", "host", "repo"} {
			bad := request
			switch invalid {
			case "project ID":
				bad.ProjectID = 0
			case "IID":
				if op.Name == OpIssueCreate {
					continue
				}
				bad.IID = 0
			case "file":
				bad.InputFile = "relative"
			case "host":
				bad.Host = "https://gitlab.com"
			case "repo":
				bad.Repo = "a/../b"
			}
			if _, err := build(bad); err == nil {
				t.Fatalf("accepted invalid request=%+v", bad)
			}
		}
	}
	// Consume the authoritative argv manifest, including the fixed preflight and
	// numeric-ID view reads, rather than treating source text as evidence.
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "official-glab", "v1.112.0", "capabilities.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Operations []struct {
			Name        Operation `json:"name"`
			Argv        []string  `json:"argv"`
			InputFields []string  `json:"input_fields"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	seen := map[Operation]bool{}
	replacer := strings.NewReplacer("{host}", "gitlab.com", "{escaped_repo}", "group%2Fproject", "{project_id}", "101", "{iid}", "42", "{private_json_file}", input)
	for _, operation := range manifest.Operations {
		switch operation.Name {
		case OpIssueWriteProject, OpIssueWriteView, OpIssueCreate, OpIssueNoteCreate, OpIssueState:
		default:
			continue
		}
		inv, err := build(Request{Operation: operation.Name, Host: "gitlab.com", Repo: "group/project", ProjectID: 101, IID: 42, InputFile: input})
		if err != nil {
			t.Fatal(err)
		}
		args := append([]string{}, operation.Argv...)
		for i := range args {
			args[i] = replacer.Replace(args[i])
		}
		if !reflect.DeepEqual(args, inv.args) {
			t.Fatalf("manifest does not match executable %s: %v vs %v", operation.Name, args, inv.args)
		}
		for _, op := range fixture.Operations {
			if op.Name == operation.Name {
				if len(op.Payload) != len(operation.InputFields) {
					t.Fatal("input field count mismatch")
				}
				for _, field := range operation.InputFields {
					if _, ok := op.Payload[field]; !ok {
						t.Fatalf("unproven input field %s", field)
					}
				}
			}
		}
		seen[operation.Name] = true
	}
	if len(seen) != 5 {
		t.Fatalf("missing issue-write operations: %v", seen)
	}
}

// Actual pinned glab, local TLS and a runtime synthetic credential. Every
// response class must generate exactly one request, including rate limits,
// server failures and redirects (which must not be followed).
func TestPinnedOfficialGlabIssueWritesTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	var err error
	binary, err = filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	fixture := loadIssueWriteFixture(t)
	host := "gitlab.issue-writes-contract.example"
	certificate, caPEM := selfManagedTestCertificate(t, host)
	for index, operation := range fixture.Operations {
		t.Run(fmt.Sprintf("%s-%d", operation.Name, index), func(t *testing.T) {
			for _, status := range []int{200, 201, 400, 401, 403, 404, 409, 422, 429, 500, 307, -1, -2} {
				t.Run(strconv.Itoa(status), func(t *testing.T) {
					requests := make(chan capturedOfficialGlabMutation, 8)
					arrived := make(chan struct{}, 8)
					token := strings.Join([]string{"synthetic", "issue-write", "tls"}, "-")
					server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, limits.MaxDescriptionBytes+4096))
						requests <- capturedOfficialGlabMutation{method: r.Method, host: r.Host, requestURI: r.RequestURI, contentType: r.Header.Get("Content-Type"), body: body, readErr: readErr}
						arrived <- struct{}{}
						if status == -1 {
							<-r.Context().Done() // cancel only after the request arrived
							return
						}
						if status == -2 {
							connection, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = connection.Close() // response lost after accepting the body
							return
						}
						if r.Header.Get("Private-Token") != token {
							t.Error("missing synthetic authentication")
						}
						w.Header().Set("Content-Type", "application/json")
						if status == 307 {
							w.Header().Set("Location", "https://"+host+"/api/v4/forbidden-redirect")
						}
						w.WriteHeader(status)
						_, _ = w.Write([]byte(`{"marker":"untrusted-provider-detail"}`))
					}))
					server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
					server.StartTLS()
					defer server.Close()
					proxy := newTestTLSTunnelProxy(host+":443", server.Listener.Addr().String())
					defer proxy.Close()
					home := t.TempDir()
					caBundle := filepath.Join(home, "ca.pem")
					if err := os.WriteFile(caBundle, caPEM, 0600); err != nil {
						t.Fatal(err)
					}
					config := filepath.Join(home, "config")
					if err := os.Mkdir(config, 0700); err != nil {
						t.Fatal(err)
					}
					// ca_cert is pinned official configuration, required on macOS where Go
					// system trust ignores SSL_CERT_FILE-only CA profiles.
					profile := fmt.Sprintf("hosts:\n  %s:\n    ca_cert: %s\n", host, strconv.Quote(caBundle))
					if err := os.WriteFile(filepath.Join(config, "config.yml"), []byte(profile), 0600); err != nil {
						t.Fatal(err)
					}
					payload, err := json.Marshal(operation.Payload)
					if err != nil {
						t.Fatal(err)
					}
					input := filepath.Join(home, "payload.json")
					if err := os.WriteFile(input, payload, 0600); err != nil {
						t.Fatal(err)
					}
					client := NewClient(ClientConfig{Path: binary, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + token, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "PATH=/usr/bin:/bin"}})
					ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
					defer cancel()
					if status == -1 {
						go func() {
							select {
							case <-arrived:
								cancel()
							case <-ctx.Done():
							}
						}()
					}
					response, err := client.Do(ctx, Request{Operation: operation.Name, Host: host, Repo: "group/project", ProjectID: fixture.ProjectID, IID: fixture.IID, InputFile: input})
					if !response.Write {
						t.Fatalf("mutation not delegated: %v", err)
					}
					if (status == 200 || status == 201) && err != nil {
						t.Fatalf("success failed: %v", err)
					}
					if (status >= 400 || status < 0) && err == nil {
						t.Fatalf("rejection accepted: %s", response.Body)
					}
					if err != nil && strings.Contains(err.Error(), "untrusted-provider-detail") {
						t.Fatalf("provider error escaped: %v", err)
					}
					if rejection, ok := uxv1.NewHTTPRejection(status); ok {
						if err == nil || uxv1.AsError(err).StatusCode != status || uxv1.AsError(err).Code != rejection.Code {
							t.Fatalf("unclassified status %d: %v", status, err)
						}
					}
					select {
					case got := <-requests:
						if got.readErr != nil || got.method != operation.Method || got.host != host || got.requestURI != "/api/v4/"+operation.Endpoint || got.contentType != "application/json" || !bytes.Equal(got.body, payload) || bytes.Contains(got.body, []byte(token)) || strings.Contains(got.requestURI, token) {
							t.Fatalf("unsafe request=%+v", got)
						}
					case <-time.After(time.Second):
						t.Fatal("no request reached local TLS service")
					}
					select {
					case got := <-requests:
						t.Fatalf("mutation retried/redirected: %+v", got)
					case <-time.After(20 * time.Millisecond):
					}
				})
			}
		})
	}
}
