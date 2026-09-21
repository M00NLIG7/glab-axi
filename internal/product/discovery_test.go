package product

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/delegate/glab"
)

func discoveryResponse(value any) glab.Response {
	body, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return glab.Response{Body: body, UpstreamVersion: glab.SupportedVersion}
}

func discoveryRepo(path, kind string) map[string]any {
	i := strings.LastIndex(path, "/")
	return map[string]any{"id": 42, "name": "project", "path_with_namespace": path, "web_url": "https://gitlab.com/" + path, "namespace": map[string]any{"id": 17, "kind": kind, "full_path": path[:i]}, "visibility": "private", "archived": false, "http_url_to_repo": "https://gitlab.com/" + path + ".git", "ssh_url_to_repo": "git@gitlab.com:" + path + ".git"}
}

func TestDiscoveryCancellationAndDeadline(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	delegate := &fakeDelegate{}
	delegate.doFunc = func(ctx context.Context, _ glab.Request) (glab.Response, error, bool) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 30*time.Second {
			t.Fatal("read lacks hard deadline")
		}
		cancel()
		<-ctx.Done()
		return glab.Response{}, ctx.Err(), true
	}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(ctx, []string{"search", "issues", "bug", "--scope", "host", "--format", "json"}, deps); code == 0 {
		t.Fatal(stdout.String())
	}
	if len(delegate.requests) != 1 {
		t.Fatalf("requests=%v", delegate.requests)
	}
}

func TestDiscoveryRepoViewRejectsWrongProject(t *testing.T) {
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{glab.OpRepoView: {discoveryResponse(discoveryRepo("other/project", "group"))}}}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), []string{"repo", "view", "group/project", "--hostname", "gitlab.com", "--format", "json"}, deps); code == 0 {
		t.Fatalf("accepted wrong project: %s", stdout.String())
	}
}
