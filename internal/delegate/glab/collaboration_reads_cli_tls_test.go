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
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Run both shipped entrypoints and the pinned upstream executable, not a child
// script. Only the provider is synthetic; no live account or credential is used.
func TestPinnedOfficialGlabCollaborationCLIReadsTLS(t *testing.T) {
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
	const host = "gitlab.collaboration-cli.example"
	const head = "2222222222222222222222222222222222222222"
	user := `{"id":1,"username":"alice","name":"Alice","email":"unapproved-user-field"}`
	mr := fmt.Sprintf(`{"id":7007,"iid":7,"project_id":99,"source_project_id":99,"target_project_id":99,"source_branch":"feature","target_branch":"main","web_url":"https://%s/group/project/-/merge_requests/7","updated_at":"2024-02-03T04:05:06Z","diff_refs":{"base_sha":"1111111111111111111111111111111111111111","head_sha":%q},"reviewers":[%s]}`, host, head, user)
	issue := fmt.Sprintf(`{"id":7007,"iid":7,"project_id":99,"web_url":"https://%s/group/project/-/issues/7","updated_at":"2024-02-03T04:05:06Z"}`, host)
	approval := fmt.Sprintf(`{"id":7007,"iid":7,"project_id":99,"approved":true,"approvals_left":0,"approved_by":[{"user":%s}]}`, user)
	certificate, ca := selfManagedTestCertificate(t, host)
	secret := strings.Join([]string{"synthetic", "collaboration", "cli", "token"}, "-")
	var mu sync.Mutex
	var scenario string
	var requests []string
	var identityReads int
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.Method+" "+r.RequestURI)
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.Host != host || r.Header.Get("PRIVATE-TOKEN") != secret {
			http.Error(w, "unexpected authority or mutation", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/api/v4/projects/group/project":
			fmt.Fprintf(w, `{"id":99,"path_with_namespace":"group/project","web_url":"https://%s/group/project"}`, host)
		case "/api/v4/projects/group/project/issues/7":
			identityReads++
			body := issue
			if scenario == "issue changed" && identityReads > 1 {
				body = strings.Replace(body, `"id":7007`, `"id":7008`, 1)
			}
			fmt.Fprint(w, body)
		case "/api/v4/projects/group/project/issues/7/discussions":
			if scenario == "issue denied" {
				w.WriteHeader(403)
				fmt.Fprint(w, `{"message":"provider-secret"}`)
				return
			}
			page := r.URL.Query().Get("page")
			width := "31"
			if scenario == "issue display limit" {
				width = "2"
			} else if scenario == "issue pagination" {
				width = "100"
			}
			if r.URL.Query().Get("per_page") != width || (page != "1" && (scenario != "issue pagination" || page != "2")) {
				http.Error(w, "unexpected pagination", 400)
				return
			}
			count, first := 1, 0
			if scenario == "issue empty" {
				count = 0
			} else if scenario == "issue display limit" {
				count = 2
			} else if scenario == "issue pagination" {
				if page == "1" {
					count = 100
				} else {
					first = 100
				}
			}
			threads := make([]any, 0, count)
			for i := first; i < first+count; i++ {
				notes := make([]any, 0, 2)
				for reply := 0; reply < 2; reply++ {
					target := 7007
					if scenario == "wrong note target" {
						target++
					}
					notes = append(notes, map[string]any{
						"id":            10000 + i*2 + reply,
						"type":          nil,
						"body":          []string{"User comment", "System reply"}[reply],
						"author":        map[string]any{"id": 1, "username": "alice", "name": "Alice"},
						"created_at":    "2024-01-02T03:04:05Z",
						"updated_at":    "2024-01-02T03:04:06Z",
						"system":        reply == 1,
						"noteable_id":   target,
						"noteable_type": "Issue",
						"project_id":    99,
						"noteable_iid":  7,
						"resolvable":    false,
						"resolved":      false,
					})
				}
				threads = append(threads, map[string]any{"id": "thread-" + strconv.Itoa(i), "individual_note": false, "notes": notes})
			}
			if err := json.NewEncoder(w).Encode(threads); err != nil {
				t.Error(err)
			}
		case "/api/v4/projects/group/project/merge_requests/7":
			identityReads++
			body := mr
			if identityReads > 1 {
				switch scenario {
				case "head changed":
					body = strings.Replace(body, head, strings.Repeat("3", 40), 1)
				case "reviewer changed":
					body = strings.Replace(body, `"alice"`, `"bob"`, 1)
				}
			}
			fmt.Fprint(w, body)
		case "/api/v4/projects/group/project/merge_requests/7/approval_state":
			fmt.Fprint(w, `{"rules":[]}`)
		case "/api/v4/projects/group/project/merge_requests/7/approvals":
			body := approval
			switch scenario {
			case "unknown approval":
				body = strings.Replace(body, `"approved":true,`, "", 1)
			case "not approved":
				body = strings.Replace(body, `"approved":true`, `"approved":false`, 1)
			case "malformed approval":
				body = strings.Replace(body, `"approved":true`, `"approved":"yes"`, 1)
			case "wrong approval identity":
				body = strings.Replace(body, `"iid":7`, `"iid":8`, 1)
			}
			fmt.Fprint(w, body)
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
				name        string
				issue       bool
				limit, exit int
				want        []string
				args        []string
			}{
				{name: "issue comments and replies", issue: true, want: []string{`"issue_id":7007`, `"body":"User comment"`, `"body":"System reply"`, `"system":true`, `"complete":true`}},
				{name: "issue empty", issue: true, want: []string{`"discussions":[]`, `"complete":true`}},
				{name: "issue display limit", issue: true, limit: 1, want: []string{`"count":1`, `"complete":false`, `"reason":"display_limit"`}},
				{name: "issue pagination", issue: true, limit: 101, want: []string{`"count":101`, `"id":"thread-100"`, `"complete":true`}},
				{name: "issue denied", issue: true, exit: 4, want: []string{`"code":"forbidden"`}},
				{name: "wrong note target", issue: true, exit: 9, want: []string{`"code":"safety_violation"`}},
				{name: "issue changed", issue: true, exit: 6, want: []string{`"code":"conflict"`, `"reason":"snapshot_changed"`}},
				{name: "reviewers and approvers", want: []string{`"reviewers":{"availability":"available","users":[{"id":1,"username":"alice","name":"Alice"}]}`, `"state":"approved"`, `"approved_by":[{"id":1,"username":"alice","name":"Alice"}]`, `"tier":"unknown"`, `"complete":true`}},
				{name: "unknown approval", want: []string{`"state":"unknown"`, `"complete":false`, `"reason":"state_unknown"`}},
				{name: "not approved", want: []string{`"state":"not_approved"`, `"complete":true`}},
				{name: "malformed approval", exit: 8, want: []string{`"code":"upstream_error"`}},
				{name: "wrong approval identity", exit: 9, want: []string{`"code":"safety_violation"`}},
				{name: "head changed", exit: 6, want: []string{`"code":"conflict"`, `"reason":"snapshot_changed"`}},
				{name: "reviewer changed", exit: 6, want: []string{`"code":"conflict"`, `"reason":"snapshot_changed"`}},
				{name: "removed expected head flag", exit: 2, args: []string{"--expected-head", head}, want: []string{`"code":"unsupported"`}},
				{name: "mutation flag", exit: 2, args: []string{"--approve"}, want: []string{`"ok":false`}},
			} {
				t.Run(test.name, func(t *testing.T) {
					mu.Lock()
					scenario, requests, identityReads = test.name, nil, 0
					mu.Unlock()
					group, leaf := "mr", "approvals"
					if test.issue {
						group, leaf = "issue", "discussions"
					}
					limit := test.limit
					if limit == 0 {
						limit = 30
					}
					args := []string{group, leaf, "7", "-R", "group/project", "--hostname", host, "--format", "json", "--limit", strconv.Itoa(limit)}
					args = append(args, test.args...)
					ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
					defer cancel()
					command := exec.CommandContext(ctx, cli, args...)
					command.Dir = home
					command.Env = []string{"PATH=" + home + ":/usr/bin:/bin", "HOME=" + home, "GLAB_CONFIG_DIR=" + configDir, "GITLAB_TOKEN=" + secret, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caPath, "GOMAXPROCS=2"}
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
					if ctx.Err() != nil || exit != test.exit || stderr.Len() != 0 || !json.Valid(stdout.Bytes()) {
						t.Fatalf("exit=%d want=%d context=%v stderr=%s stdout=%s", exit, test.exit, ctx.Err(), stderr.String(), stdout.String())
					}
					for _, want := range test.want {
						if !strings.Contains(stdout.String(), want) {
							t.Fatalf("missing %s: %s", want, stdout.String())
						}
					}
					for _, forbidden := range []string{secret, "provider-secret", "unapproved-user-field"} {
						if strings.Contains(stdout.String(), forbidden) {
							t.Fatal("private or unapproved provider field leaked")
						}
					}
					if exit != 0 && strings.Contains(stdout.String(), `"data":`) {
						t.Fatalf("failed read exposed partial evidence: %s", stdout.String())
					}
					mu.Lock()
					defer mu.Unlock()
					if len(test.args) != 0 {
						if len(requests) != 0 {
							t.Fatalf("refused flag contacted provider: %v", requests)
						}
					} else if len(requests) == 0 {
						t.Fatal("no real provider requests")
					}
					for _, request := range requests {
						if !strings.HasPrefix(request, "GET /api/v4/projects/group%2Fproject") {
							t.Fatalf("unexpected request: %s", request)
						}
					}
					t.Logf("$ %s %s\nexit=%d\nstdout=%s\nstderr=%s\nprovider requests=%v", program, strings.Join(args, " "), exit, stdout.String(), stderr.String(), requests)
				})
			}
		})
	}
}
