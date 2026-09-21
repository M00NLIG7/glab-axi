package glab

import (
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
	"strings"
	"sync"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
)

func TestRepoAdminBuildersUseFixedRoutes(t *testing.T) {
	file := filepath.Join(t.TempDir(), "input")
	for _, test := range []struct {
		op            Operation
		id            int64
		method, route string
		write         bool
	}{
		{OpAdminUser, 0, "GET", "user", false}, {OpAdminNamespace, 21, "GET", "namespaces/21", false}, {OpAdminProject, 0, "GET", "projects/team%2Fsub%2Fproject", false},
		{OpAdminCreate, 0, "POST", "projects", true}, {OpAdminEdit, 101, "PUT", "projects/101", true}, {OpAdminFork, 101, "POST", "projects/101/fork", true},
	} {
		r := Request{Operation: test.op, Host: "gitlab.example.invalid", Repo: "team/sub/project", ID: test.id}
		if test.write {
			r.InputFile = file
		}
		inv, err := build(r)
		if err != nil {
			t.Fatal(err)
		}
		want := []string{"api", "--method", test.method, "--hostname", r.Host, test.route}
		if test.write {
			want = append(want, "--input", file, "--header", "Content-Type: application/json")
		}
		if !reflect.DeepEqual(inv.args, want) || inv.write != test.write {
			t.Fatalf("invocation=%#v want=%v", inv, want)
		}
		if test.id > 0 {
			r.ID = 0
			if _, err := build(r); err == nil {
				t.Fatal("zero ID accepted")
			}
			r.ID = test.id
		}
		if test.write {
			r.InputFile = "-"
			if _, err := build(r); err == nil {
				t.Fatal("stdin payload accepted")
			}
		}
	}
}
func TestRepoAdminAbsenceRequiresFramedHTTPStatus(t *testing.T) {
	for _, test := range []struct {
		text   string
		status int
	}{
		{"glab: 404\n", 404}, {"not found", 0}, {"provider description says 404", 0}, {"glab: 403\n", 403},
	} {
		err := classifyChildFailure([]byte(test.text), fmt.Errorf("exit 1"), false, OpAdminProject)
		if uxv1.AsError(err).StatusCode != test.status {
			t.Fatalf("%q: %#v", test.text, err)
		}
	}
}

// This optional CI test executes the checksum-pinned official binary itself,
// with a synthetic per-host CA and token. No real profile or GitLab is used.
func TestPinnedOfficialGlabRepoAdminTLS(t *testing.T) {
	binary := officialGlabTestBinary()
	if binary == "" {
		t.Skip("official-glab package fixture not supplied")
	}
	binary, err := filepath.Abs(binary)
	if err != nil {
		t.Fatal(err)
	}
	host := "gitlab.self-managed.example"
	cert, ca := selfManagedTestCertificate(t, host)
	secret := strings.Join([]string{"synthetic", "repo", "admin", "token"}, "-")
	var mu sync.Mutex
	var records []capturedOfficialGlabMutation
	status := 200
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, readErr := io.ReadAll(http.MaxBytesReader(w, r.Body, 16<<10))
		mu.Lock()
		defer mu.Unlock()
		records = append(records, capturedOfficialGlabMutation{method: r.Method, host: r.Host, requestURI: r.RequestURI, body: body, readErr: readErr})
		if r.Header.Get("PRIVATE-TOKEN") != secret {
			t.Error("missing synthetic token")
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, `{"id":101}`)
	}))
	server.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{cert}}
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
	if err := os.WriteFile(filepath.Join(config, "config.yml"), []byte(fmt.Sprintf("hosts:\n  %s:\n    ca_cert: %s\n", host, caPath)), 0600); err != nil {
		t.Fatal(err)
	}
	client := NewClient(ClientConfig{Path: binary, Dir: home, Env: []string{"HOME=" + home, "GLAB_CONFIG_DIR=" + config, "GITLAB_TOKEN=" + secret, "HTTPS_PROXY=" + proxy.URL, "NO_PROXY=", "SSL_CERT_FILE=" + caPath, "PATH=/usr/bin:/bin"}})
	for _, test := range []struct {
		op            Operation
		id            int64
		method, route string
		payload       map[string]any
		status        int
	}{
		{OpAdminUser, 0, "GET", "user", nil, 200},
		{OpAdminNamespace, 21, "GET", "namespaces/21", nil, 200},
		{OpAdminProject, 0, "GET", "projects/team%2Fsub%2Fproject", nil, 404},
		{OpAdminCreate, 0, "POST", "projects", map[string]any{"namespace_id": 21, "path": "project", "name": "project", "visibility": "private", "initialize_with_readme": false}, 201},
		{OpAdminEdit, 101, "PUT", "projects/101", map[string]any{"visibility": "private", "issues_access_level": "private", "wiki_access_level": "disabled"}, 200},
		{OpAdminFork, 101, "POST", "projects/101/fork", map[string]any{"namespace_id": 21, "path": "fork", "name": "fork", "visibility": "private"}, 201},
		{OpAdminCreate, 0, "POST", "projects", map[string]any{"namespace_id": 21, "path": "project", "visibility": "private"}, 500},
		{OpAdminEdit, 101, "PUT", "projects/101", map[string]any{"description": "new"}, 422},
		{OpAdminFork, 101, "POST", "projects/101/fork", map[string]any{"namespace_id": 21, "path": "fork", "visibility": "private"}, 409},
	} {
		t.Run(string(test.op)+fmt.Sprint(test.status), func(t *testing.T) {
			r := Request{Operation: test.op, Host: host, Repo: "team/sub/project", ID: test.id}
			var encoded []byte
			if test.payload != nil {
				encoded, _ = json.Marshal(test.payload)
				r.InputFile = filepath.Join(home, "payload.json")
				if err := os.WriteFile(r.InputFile, encoded, 0600); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			status = test.status
			records = nil
			mu.Unlock()
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			_, err := client.Do(ctx, r)
			if test.status < 400 && err != nil || test.status >= 400 && err == nil {
				t.Fatalf("status=%d error=%v", test.status, err)
			}
			if test.status == 404 && uxv1.AsError(err).StatusCode != 404 {
				t.Fatalf("absence not proven: %#v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(records) != 1 {
				t.Fatalf("requests=%d; mutation must never be retried", len(records))
			}
			got := records[0]
			if got.method != test.method || got.requestURI != "/api/v4/"+test.route || got.host != host || got.readErr != nil || string(got.body) != string(encoded) {
				t.Fatalf("unexpected request: %+v", got)
			}
		})
	}
}
