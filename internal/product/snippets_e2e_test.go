package product

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
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

	"gl-axi/internal/contract/uxv1"
)

// Same bounded CONNECT pattern as delegate/glab's upstream contract fixture.
func snippetTLSProxy(authority, address string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect || r.Host != authority {
			http.Error(w, "unexpected tunnel target", http.StatusBadRequest)
			return
		}
		upstream, err := net.DialTimeout("tcp", address, 5*time.Second)
		if err != nil {
			http.Error(w, "fixture unavailable", http.StatusBadGateway)
			return
		}
		downstream, buffered, err := w.(http.Hijacker).Hijack()
		if err != nil {
			upstream.Close()
			return
		}
		defer upstream.Close()
		defer downstream.Close()
		if _, err := buffered.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			return
		}
		if err := buffered.Flush(); err != nil {
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

func TestSnippetExecutableAliases(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX child argv fixture")
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			dir := t.TempDir()
			binary := productTestExecutable(t, program, true)
			record := filepath.Join(dir, "record")
			fixture := snippetJSON(snippetFixture(42, false))
			script := fmt.Sprintf(`#!/bin/sh
set -eu
printf '%%s\n' "$*" >> "$SNIPPET_RECORD"
case "$*" in
 'version') printf 'glab 1.112.0 (816e3a52)\n';;
 'api --method GET --hostname gitlab.com user') printf '%%s' '{"id":7,"username":"reader"}';;
 'api --method GET --hostname gitlab.com snippets/42') printf '%%s' '%s';;
 'api --method GET --hostname gitlab.com snippets/42/files/main/dir%%2Fa%%20b.txt/raw') printf 'hello 世界';;
 *) printf 'unexpected argv' >&2; exit 91;;
esac
`, fixture)
			if err := os.WriteFile(filepath.Join(dir, "glab"), []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			secret := strings.Join([]string{"synthetic", "snippet", "e2e", "token"}, "-")
			env := []string{"HOME=" + dir, "PATH=" + dir + ":/usr/bin:/bin", "SNIPPET_RECORD=" + record, "GITLAB_TOKEN=" + secret, "GLAB_CONFIG_DIR=" + filepath.Join(dir, "config")}
			for _, format := range []string{"json", "toon"} {
				cmd := exec.Command(binary, "snippet", "view", "42", "--scope", "personal", "--filename", "dir/a b.txt", "--content-limit", "8", "--format", format)
				cmd.Env = env
				cmd.Dir = dir
				var out, stderr bytes.Buffer
				cmd.Stdout = &out
				cmd.Stderr = &stderr
				if err := cmd.Run(); err != nil || stderr.Len() != 0 || strings.Contains(out.String(), secret) {
					t.Fatalf("run=%v out=%s stderr=%s", err, out.String(), stderr.String())
				}
				if format == "json" {
					var e snippetEnvelope
					if err := json.Unmarshal(out.Bytes(), &e); err != nil || !e.OK || e.Data.Snippet.Content == nil || e.Data.Snippet.Content.Text != "hello " || !e.Meta.Truncated {
						t.Fatalf("output=%s err=%v", out.String(), err)
					}
				} else if !strings.Contains(out.String(), "snippet") || !strings.Contains(out.String(), "hello ") {
					t.Fatalf("output=%s", out.String())
				}
			}
			before, err := os.ReadFile(record)
			if err != nil {
				t.Fatal(err)
			}
			for _, args := range [][]string{
				{"view", "42", "--scope", "personal", "--secret"},
				{"view", "42", "--scope", "personal", "--unlisted"},
				{"view", "42", "--scope", "personal", "--full"},
				{"view", "42", "--scope", "personal", "--raw"},
				{"list"}, {"list", "--scope", "all"}, {"list", "--scope", "project"},
				{"list", "--scope", "personal", "-R", "group/project"},
				{"list", "--scope", "personal", "--fields", "unknown"},
				{"view", "01", "--scope", "personal"},
				{"view", "42", "--scope", "personal", "--filename", "../x"},
				{"view", "42", "--scope", "personal", "--filename", "a", "--files"},
				{"view", "42", "--scope", "personal", "--content-limit", "1"},
				{"view", "42", "--scope", "personal", "--scope", "personal"},
				{"view", "42", "--scope", "personal", "--hostname", "https://gitlab.com"},
				{"view", "https://wrong.example/-/snippets/42", "--scope", "personal"},
				{"view", "https://gitlab.com/group/other/-/snippets/42", "--scope", "project", "-R", "group/project"},
			} {
				cmd := exec.Command(binary, append(append([]string{"snippet"}, args...), "--format", "json")...)
				cmd.Env = env
				cmd.Dir = dir
				if err := cmd.Run(); err == nil {
					t.Fatalf("accepted %v", args)
				}
			}
			after, err := os.ReadFile(record)
			if err != nil || !bytes.Equal(before, after) {
				t.Fatal("invalid input reached child")
			}
			lines := strings.Split(strings.TrimSpace(string(after)), "\n")
			if len(lines) != 10 {
				t.Fatalf("unexpected calls: %q", lines)
			}
			for _, line := range lines {
				if strings.Contains(line, secret) || strings.Contains(line, "PUT") || strings.Contains(line, "POST") {
					t.Fatalf("unsafe argv: %q", line)
				}
			}
		})
	}
}

// TestSnippetCLIWithPinnedOfficialGlabTLS is the real user executable -> pinned
// official glab -> local TLS protocol path. The optional binary is supplied by
// the existing offline upstream gate; no live GitLab or real credential exists.
func TestSnippetCLIWithPinnedOfficialGlabTLS(t *testing.T) {
	official := os.Getenv("GL_AXI_OFFICIAL_GLAB_TEST_BINARY")
	if official == "" {
		official = os.Getenv("GLAB_AXI_OFFICIAL_GLAB_TEST_BINARY")
	}
	if official == "" {
		t.Skip("pinned official-glab fixture not supplied")
	}
	if runtime.GOOS == "windows" {
		t.Skip("fixture binary link requires POSIX")
	}
	official, err := filepath.Abs(official)
	if err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) { snippetOfficialTLS(t, official, program) })
	}
}

func snippetOfficialTLS(t *testing.T, official, program string) {
	validateSchema := snippetSchemaValidator(t)
	dir := t.TempDir()
	binary := productTestExecutable(t, program, true)
	if err := os.Symlink(official, filepath.Join(dir, "glab")); err != nil {
		t.Fatal(err)
	}
	secret := strings.Join([]string{"synthetic", "snippet", "tls", "token"}, "-")
	var mu sync.Mutex
	mode := ""
	// Go's httptest certificate includes example.com. CONNECT below maps that
	// logical authority only to this TLS fixture; there is no public routing.
	host := "example.com"
	requests := []string{}
	unsafeRequest := false
	cancelReadStarted := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests = append(requests, r.RequestURI)
		if r.Method != "GET" || r.Host != host || r.Header.Get("PRIVATE-TOKEN") != secret || r.ContentLength > 0 || strings.Contains(r.RequestURI, secret) {
			unsafeRequest = true
			http.Error(w, "unsafe request", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v4/user" {
			if mode == "unauthenticated" {
				http.Error(w, "provider secret detail", 401)
				return
			}
			fmt.Fprint(w, `{"id":7,"username":"reader"}`)
			return
		}
		if r.URL.Path == "/api/v4/projects/group/project" {
			fmt.Fprintf(w, `{"id":101,"path_with_namespace":"group/project","web_url":"https://%s/group/project"}`, host)
			return
		}
		project := strings.Contains(r.URL.Path, "/projects/")
		item := snippetFixture(42, project)
		item.WebURL = strings.Replace(item.WebURL, "gitlab.com", host, 1)
		item.RawURL = strings.Replace(item.RawURL, "gitlab.com", host, 1)
		item.Files[0].RawURL = strings.Replace(item.Files[0].RawURL, "gitlab.com", host, 1)
		ref, encodedRef := "main", "main"
		if strings.HasPrefix(mode, "slash-ref-") {
			ref, encodedRef = "release/next", "release%2Fnext"
			item.Files[0].RawURL = strings.Replace(item.Files[0].RawURL, "/raw/main/", "/raw/"+encodedRef+"/", 1)
			if strings.Contains(r.URL.Path, "/files/") && !strings.Contains(r.RequestURI, "/files/release%2Fnext/") {
				unsafeRequest = true
				http.Error(w, "root ref was not encoded as one API segment", http.StatusBadRequest)
				return
			}
		}
		for _, file := range snippetFilenameCases {
			item.Files = append(item.Files, upstreamSnippetFile{Path: file.name, RawURL: item.WebURL + "/raw/" + encodedRef + "/" + file.rawPath})
			if strings.HasSuffix(r.URL.Path, "/files/"+ref+"/"+file.name+"/raw") {
				w.Header().Set("Content-Type", "text/plain")
				fmt.Fprint(w, file.content)
				return
			}
		}
		if mode == "wrong-id" {
			item.ID = 99
		}
		if mode == "wrong-host" {
			item.WebURL = strings.Replace(item.WebURL, host, "evil.example", 1)
		}
		if mode == "wrong-project" {
			n := int64(999)
			item.ProjectID = &n
		}
		if mode == "wrong-owner" {
			item.Author.ID = 99
		}
		if mode == "wrong-owner-url" {
			item.Author.WebURL = "https://evil.example/reader"
		}
		if mode == "wrong-raw-url" {
			item.RawURL = "https://evil.example/-/snippets/42/raw"
		}
		if mode == "wrong-file-url" {
			item.Files[0].RawURL = item.WebURL + "/raw/main/different.txt"
		}
		if strings.HasPrefix(mode, "visibility-") {
			item.Visibility = strings.TrimPrefix(mode, "visibility-")
		}
		if mode == "long-title" {
			item.Title = strings.Repeat("世界", 1000)
		}
		if mode == "unavailable" {
			item.Files = nil
		}
		if strings.HasSuffix(r.URL.Path, "/files/"+ref+"/dir/a b.txt/raw") {
			w.Header().Set("Content-Type", "text/plain")
			switch mode {
			case "missing-content":
				http.Error(w, "provider secret detail", 404)
			case "binary":
				w.Write([]byte{0xff, 0})
			default:
				fmt.Fprint(w, "a世🙂z")
			}
			return
		}
		if strings.HasSuffix(r.URL.Path, "/snippets/42") {
			if mode == "malformed" {
				fmt.Fprint(w, "{bad")
				return
			}
			if mode == "canceled" {
				close(cancelReadStarted)
				mu.Unlock()
				<-r.Context().Done()
				mu.Lock()
				return
			}
			w.Write(snippetJSON(item))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/snippets") {
			if mode == "page-overflow" {
				items := make([]upstreamSnippet, 33)
				for i := range items {
					items[i] = item
				}
				w.Write(snippetJSON(items))
				return
			}
			if mode == "filtered-pages" {
				page, _ := strconv.Atoi(r.URL.Query().Get("page"))
				if r.URL.Query().Get("per_page") != "3" {
					unsafeRequest = true
				}
				if page == 1 {
					items := []upstreamSnippet{}
					for i := int64(1); i <= 3; i++ {
						x := item
						x.ID = i
						x.Visibility = "public"
						x.WebURL = strings.ReplaceAll(item.WebURL, "/42", "/"+strconv.FormatInt(i, 10))
						x.RawURL = x.WebURL + "/raw"
						x.Files = []upstreamSnippetFile{}
						items = append(items, x)
					}
					w.Write(snippetJSON(items))
					return
				}
			}
			w.Write(snippetJSON([]upstreamSnippet{item}))
			return
		}
		http.Error(w, "unexpected route", 404)
	}))
	defer server.Close()
	if err := server.Certificate().VerifyHostname(host); err != nil {
		t.Fatalf("TLS fixture certificate lacks logical host: %v", err)
	}
	proxy := snippetTLSProxy(host+":443", server.Listener.Addr().String())
	defer proxy.Close()
	cert := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	configDir := filepath.Join(dir, "config")
	if err := os.MkdirAll(configDir, 0700); err != nil {
		t.Fatal(err)
	}
	// Darwin's native roots do not use SSL_CERT_FILE alone. This isolated
	// non-credential profile pins the fixture CA with TLS verification enabled.
	config := fmt.Sprintf("hosts:\n  %s:\n    api_host: %s\n    api_protocol: https\n    ca_cert: %s\n", host, host, cert)
	if err := os.WriteFile(filepath.Join(configDir, "config.yml"), []byte(config), 0600); err != nil {
		t.Fatal(err)
	}
	// Preserve the caller-selected Git executable: /usr/bin/git on macOS is
	// a launcher that can block on Xcode checks under an isolated HOME.
	gitPath, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"HOME=" + dir, "GLAB_CONFIG_DIR=" + configDir, "PATH=" + dir + ":" + filepath.Dir(gitPath) + ":/usr/bin:/bin", "GITLAB_TOKEN=" + secret, "SSL_CERT_FILE=" + cert, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY="}
	type testCase struct {
		name    string
		args    []string
		failure bool
	}
	tests := []testCase{
		{"personal-list", []string{"list", "--scope", "personal"}, false},
		{"project-list", []string{"list", "--scope", "project", "-R", "group/project"}, false},
		{"fields", []string{"list", "--scope", "personal", "--fields", "description,created_at,updated_at"}, false},
		{"filtered-pages", []string{"list", "--scope", "personal", "--visibility", "private", "--limit", "2"}, false},
		{"personal-url", []string{"view", "https://" + host + "/-/snippets/42", "--scope", "personal", "--files"}, false},
		{"project-url", []string{"view", "https://" + host + "/group/project/-/snippets/42", "--scope", "project", "-R", "group/project"}, false},
		{"content", []string{"view", "42", "--scope", "personal", "--filename", "dir/a b.txt", "--content-limit", "5"}, false},
		{"project-content", []string{"view", "42", "--scope", "project", "-R", "group/project", "--filename", "dir/a b.txt"}, false},
		{"empty-content", []string{"view", "42", "--scope", "personal", "--filename", "dir/a b.txt", "--content-limit", "0"}, false},
		{"long-title", []string{"view", "42", "--scope", "personal"}, false},
		{"unauthenticated", []string{"list", "--scope", "project", "-R", "group/project"}, true},
		{"missing-file", []string{"view", "42", "--scope", "personal", "--filename", "absent"}, true},
		{"unavailable", []string{"view", "42", "--scope", "personal", "--files"}, true},
		{"missing-content", []string{"view", "42", "--scope", "personal", "--filename", "dir/a b.txt"}, true},
		{"binary", []string{"view", "42", "--scope", "personal", "--filename", "dir/a b.txt"}, true},
		{"wrong-id", []string{"view", "42", "--scope", "personal"}, true},
		{"wrong-host", []string{"view", "42", "--scope", "personal"}, true},
		{"wrong-project", []string{"view", "42", "--scope", "project", "-R", "group/project"}, true},
		{"wrong-owner", []string{"list", "--scope", "personal"}, true},
		{"wrong-owner-url", []string{"view", "42", "--scope", "personal"}, true},
		{"wrong-raw-url", []string{"view", "42", "--scope", "personal"}, true},
		{"wrong-file-url", []string{"view", "42", "--scope", "personal", "--filename", "dir/a b.txt"}, true},
		{"visibility-public", []string{"list", "--scope", "personal", "--visibility", "public"}, false},
		{"visibility-internal", []string{"list", "--scope", "personal", "--visibility", "internal"}, false},
		{"visibility-private", []string{"list", "--scope", "personal", "--visibility", "private"}, false},
		{"malformed", []string{"view", "42", "--scope", "personal"}, true},
		{"page-overflow", []string{"list", "--scope", "personal"}, true},
		{"invalid-host", []string{"view", "https://wrong.example/-/snippets/42", "--scope", "personal"}, true},
		{"invalid-project", []string{"view", "https://" + host + "/group/other/-/snippets/42", "--scope", "project", "-R", "group/project"}, true},
		{"invalid-legacy-personal", []string{"view", "https://" + host + "/snippets/42", "--scope", "personal"}, true},
		{"invalid-legacy-project", []string{"view", "https://" + host + "/group/project/snippets/42", "--scope", "project", "-R", "group/project"}, true},
	}
	wantContent := map[string]SnippetContent{}
	for _, scope := range []string{"personal", "project"} {
		for _, selection := range []string{"list", "view", "files", ":username.txt", "reader.txt", "dir/a b.txt"} {
			name := "slash-ref-" + scope + "-" + selection
			args := []string{"view", "42", "--scope", scope}
			switch selection {
			case "list":
				args = []string{"list", "--scope", scope}
			case "view":
			case "files":
				args = append(args, "--files")
			default:
				args = append(args, "--filename", selection)
				text := "a世🙂z"
				for _, file := range snippetFilenameCases {
					if file.name == selection {
						text = file.content
					}
				}
				wantContent[name] = SnippetContent{Filename: selection, Ref: "release/next", Text: text}
			}
			if scope == "project" {
				args = append(args, "-R", "group/project")
			}
			tests = append(tests, testCase{name, args, false})
		}
		for _, file := range snippetFilenameCases {
			name := "filename-" + scope + "-" + file.name
			args := []string{"view", "42", "--scope", scope, "--filename", file.name}
			if scope == "project" {
				args = append(args, "-R", "group/project")
			}
			tests = append(tests, testCase{name, args, false})
			wantContent[name] = SnippetContent{Filename: file.name, Ref: "main", Text: file.content}
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			mu.Lock()
			mode = test.name
			requests = nil
			mu.Unlock()
			args := append([]string{"snippet"}, test.args...)
			args = append(args, "--hostname", host, "--format", "json")
			cmd := exec.Command(binary, args...)
			cmd.Env = env
			cmd.Dir = dir
			var out, stderr bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &stderr
			runErr := cmd.Run()
			var e snippetEnvelope
			if err := json.Unmarshal(out.Bytes(), &e); err != nil {
				t.Fatalf("decode=%v out=%s stderr=%s run=%v", err, out.String(), stderr.String(), runErr)
			}
			validateSchema(t, test.args[0], out.Bytes())
			if (runErr != nil) != test.failure || e.OK == test.failure || e.Meta.Complete == test.failure {
				t.Fatalf("run=%v out=%s stderr=%s", runErr, out.String(), stderr.String())
			}
			if strings.Contains(out.String()+stderr.String(), secret) || strings.Contains(out.String(), "provider secret detail") {
				t.Fatal("credential/provider error leaked")
			}
			mu.Lock()
			count := len(requests)
			t.Logf("CLI %s %q exit=%v\n%s\nGET requests: %q", program, args, runErr, out.String(), requests)
			bad := unsafeRequest
			mu.Unlock()
			if bad {
				t.Fatal("unsafe protocol request")
			}
			if !strings.HasPrefix(test.name, "invalid-") && (count == 0 || e.Meta.UpstreamVersion != "1.112.0") {
				t.Fatalf("scenario did not reach the pinned provider: requests=%d result=%+v", count, e)
			}
			if strings.HasPrefix(test.name, "invalid-") && count != 0 {
				t.Fatalf("invalid input sent %d requests", count)
			}
			if test.failure && !strings.HasPrefix(test.name, "invalid-") {
				wantCode := uxv1.CodeUpstream
				switch {
				case strings.HasPrefix(test.name, "wrong-"):
					wantCode = uxv1.CodeSafety
				case test.name == "unauthenticated":
					wantCode = uxv1.CodeAuthentication
				case test.name == "missing-file", test.name == "missing-content":
					wantCode = uxv1.CodeNotFound
				case test.name == "unavailable":
					wantCode = uxv1.CodeUnsupported
				}
				if e.Error == nil || e.Error.Code != wantCode {
					t.Fatalf("wrong failure: got %+v want %s", e.Error, wantCode)
				}
			}
			if test.name == "unauthenticated" && count != 1 {
				t.Fatal("anonymous fallback")
			}
			if test.name == "filtered-pages" && (!e.Meta.Complete || e.Meta.Count != 1 || count != 3) {
				t.Fatalf("pagination: %+v count=%d", e.Meta, count)
			}
			if strings.HasPrefix(test.name, "visibility-") && (len(e.Data.Snippets) != 1 || e.Data.Snippets[0].Visibility != strings.TrimPrefix(test.name, "visibility-")) {
				t.Fatalf("visibility: %+v", e.Data.Snippets)
			}
			if test.name == "content" && (e.Data.Snippet.Content.Text != "a世" || !e.Data.Snippet.Content.Truncated) {
				t.Fatalf("content=%+v", e.Data.Snippet.Content)
			}
			if test.name == "empty-content" && (e.Data.Snippet.Content.Text != "" || !e.Meta.Truncated) {
				t.Fatal("zero content limit not honored")
			}
			if test.name == "long-title" && (!e.Meta.Truncated || len(e.Data.Snippet.Title) > 4096) {
				t.Fatal("UTF-8 title bound not honored")
			}
			if strings.HasPrefix(test.name, "slash-ref-") {
				snippet := e.Data.Snippet
				if strings.HasSuffix(test.name, "-list") {
					if len(e.Data.Snippets) != 1 {
						t.Fatalf("snippets=%+v", e.Data.Snippets)
					}
					snippet = e.Data.Snippets[0]
				}
				if snippet.ID != 42 || len(snippet.Files) != 1+len(snippetFilenameCases) || !snippet.FilesAvailable {
					t.Fatalf("slash-ref metadata=%+v", snippet)
				}
			}
			if want, ok := wantContent[test.name]; ok {
				if e.Data.Snippet.Content == nil || *e.Data.Snippet.Content != want {
					t.Fatalf("content=%+v want=%+v", e.Data.Snippet.Content, want)
				}
				wantRequests := 4
				if e.Data.Snippet.Scope == "project" {
					wantRequests++
				}
				if count != wantRequests {
					t.Fatalf("got %d requests, want %d", count, wantRequests)
				}
			}
		})
	}
	// Exercise cancellation while official glab is waiting on a TLS response.
	mu.Lock()
	mode = "canceled"
	requests = nil
	mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, "snippet", "view", "42", "--scope", "personal", "--hostname", host, "--format", "json")
	cmd.Env, cmd.Dir, cmd.Stdout, cmd.Stderr = env, dir, &stdout, &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-cancelReadStarted:
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		_ = cmd.Wait()
		t.Fatal("snippet read did not reach cancellation fixture")
	}
	waitErr := cmd.Wait()
	var e snippetEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &e); err != nil || waitErr == nil || cmd.ProcessState.ExitCode() != 130 || e.OK || e.Meta.Complete || e.Error == nil || e.Error.Code != uxv1.CodeCanceled {
		t.Fatalf("cancellation exit=%v out=%s stderr=%s err=%v", waitErr, stdout.String(), stderr.String(), err)
	}
}
