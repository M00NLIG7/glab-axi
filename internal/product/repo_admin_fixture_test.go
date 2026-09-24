package product

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

// Local labels retain the feature fault-injection matrix without exposing any
// corresponding delegated operation. Production always uses productnative.
const (
	adminTestOpUser      glab.Operation = "repo-admin-user"
	adminTestOpNamespace glab.Operation = "repo-admin-namespace"
	adminTestOpProject   glab.Operation = "repo-admin-project"
	adminTestOpCreate    glab.Operation = "repo-admin-create"
	adminTestOpEdit      glab.Operation = "repo-admin-edit"
	adminTestOpFork      glab.Operation = "repo-admin-fork"
)

// Run the existing field/drift matrix through real local TLS and the native
// client. The fake's labels are test observations, not delegated child argv.
func adminTestNativeDeps(t *testing.T, fake *fakeDelegate) (*bytes.Buffer, *bytes.Buffer, Dependencies, func()) {
	t.Helper()
	token := strings.Join([]string{"synthetic", "native", "matrix", "credential"}, "-")
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request := glab.Request{Host: "gitlab.example.invalid"}
		path := strings.TrimPrefix(r.URL.EscapedPath(), "/api/v4/")
		switch {
		case r.Method == "GET" && path == "user":
			request.Operation = adminTestOpUser
		case r.Method == "GET" && strings.HasPrefix(path, "namespaces/"):
			request.Operation = adminTestOpNamespace
			request.ID, _ = strconv.ParseInt(strings.TrimPrefix(path, "namespaces/"), 10, 64)
		case r.Method == "GET" && strings.HasPrefix(path, "projects/"):
			request.Operation = adminTestOpProject
			request.Repo, _ = url.PathUnescape(strings.TrimPrefix(path, "projects/"))
		case r.Method == "POST" && path == "projects":
			request.Operation = adminTestOpCreate
		case r.Method == "PUT" && strings.HasPrefix(path, "projects/"):
			request.Operation = adminTestOpEdit
			request.ID, _ = strconv.ParseInt(strings.TrimPrefix(path, "projects/"), 10, 64)
		case r.Method == "POST" && strings.HasPrefix(path, "projects/") && strings.HasSuffix(path, "/fork"):
			request.Operation = adminTestOpFork
			request.ID, _ = strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(path, "projects/"), "/fork"), 10, 64)
		default:
			t.Errorf("unexpected native administration route %s %s", r.Method, path)
			w.WriteHeader(400)
			return
		}
		if r.Header.Get("PRIVATE-TOKEN") != token {
			t.Error("fixture credential mismatch")
			w.WriteHeader(401)
			return
		}
		if r.Method != "GET" {
			body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
			if err != nil {
				t.Error(err)
			}
			fake.inputBodies = append(fake.inputBodies, body)
		}
		response, err := fake.Do(r.Context(), request)
		if err != nil {
			status := uxv1.AsError(err).StatusCode
			if status == 0 {
				status = http.StatusInternalServerError
			}
			w.WriteHeader(status)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(response.Body)
	}))
	t.Cleanup(server.Close)
	stdout, stderr, deps := productTestDeps(t, nil)
	deps.NewDelegate = func() delegateClient { t.Fatal("native matrix constructed an official-glab delegate"); return nil }
	dir := t.TempDir()
	if err := os.Chmod(dir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := config.New()
	cfg.Hosts["gitlab.example.invalid"] = config.Host{GitHosts: []string{"gitlab.example.invalid"}, APIBase: server.URL + "/api/v4", WebBase: "https://gitlab.example.invalid", ProxyDisabled: true}
	deps.Runtime.ConfigPath = filepath.Join(dir, "config.json")
	if err := config.Save(deps.Runtime.ConfigPath, cfg); err != nil {
		t.Fatal(err)
	}
	deps.Runtime.HTTPClient = server.Client()
	deps.Runtime.LookupEnv = func(name string) (string, bool) {
		if name == "GL_AXI_TOKEN" {
			return token, true
		}
		return "", false
	}
	deps.Runtime.Keyring = &adminNativeKeyring{err: auth.ErrKeyringUnavailable}
	// Close joins the fixture handlers before tests inspect their captured state.
	return stdout, stderr, deps, server.Close
}

func adminTestRun(ctx context.Context, args []string, deps Dependencies, closeFixture func()) int {
	code := Run(ctx, args, deps)
	closeFixture()
	return code
}
