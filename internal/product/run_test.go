package product

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"

	"glab-axi/internal/contract/uxv1"
	"glab-axi/internal/delegate/glab"
	runtimepkg "glab-axi/internal/runtime"
)

type fakeDelegate struct {
	requests    []glab.Request
	responses   map[glab.Operation][]glab.Response
	errors      map[glab.Operation][]error
	inputBodies [][]byte
	inputModes  []os.FileMode
	inputErr    error
	loginErr    error
	statusErr   error
}

func (f *fakeDelegate) Version(context.Context) (string, error) { return glab.SupportedVersion, nil }
func (f *fakeDelegate) Login(context.Context, string) (string, error) {
	return glab.SupportedVersion, f.loginErr
}
func (f *fakeDelegate) AuthStatus(context.Context, string) (string, error) {
	return glab.SupportedVersion, f.statusErr
}
func (f *fakeDelegate) Do(_ context.Context, request glab.Request) (glab.Response, error) {
	f.requests = append(f.requests, request)
	if request.InputFile != "" {
		body, err := os.ReadFile(request.InputFile)
		if err != nil {
			f.inputErr = err
		} else {
			f.inputBodies = append(f.inputBodies, body)
			if info, statErr := os.Stat(request.InputFile); statErr == nil {
				f.inputModes = append(f.inputModes, info.Mode())
			} else {
				f.inputErr = statErr
			}
		}
	}
	responses := f.responses[request.Operation]
	errs := f.errors[request.Operation]
	var response glab.Response
	var err error
	if len(responses) > 0 {
		response = responses[0]
		f.responses[request.Operation] = responses[1:]
	}
	if len(errs) > 0 {
		err = errs[0]
		f.errors[request.Operation] = errs[1:]
	}
	return response, err
}

func TestProductListNormalizesAndDisclosesDisplayTruncation(t *testing.T) {
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpIssueList: {{Body: []byte(`[
			{"id":1,"iid":1,"title":"one","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/1","author":{"username":"a"}},
			{"id":2,"iid":2,"title":"two","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/2","author":{"username":"b"}},
			{"id":3,"iid":3,"title":"three","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/3","author":{"username":"c"}}
		]`), UpstreamVersion: glab.SupportedVersion}},
	}}
	stdout, stderr, deps := productTestDeps(t, delegate)
	code := Run(context.Background(), []string{"issue", "list", "-R=group/project", "--hostname=gitlab.com", "--limit=2", "--format=json"}, deps)
	if code != 0 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%s stdout=%s", code, stderr.String(), stdout.String())
	}
	var envelope struct {
		Schema string `json:"schema"`
		OK     bool   `json:"ok"`
		Data   struct {
			Issues []Issue `json:"issues"`
		} `json:"data"`
		Meta uxv1.Meta `json:"meta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Schema != uxv1.Schema || !envelope.OK || len(envelope.Data.Issues) != 2 || envelope.Meta.Complete || !envelope.Meta.Truncated || envelope.Meta.Reason != "display_limit" || envelope.Meta.Backend != "official-glab" {
		t.Fatalf("unexpected envelope: %#v", envelope)
	}
	if len(delegate.requests) != 1 || delegate.requests[0].PerPage != 3 || delegate.requests[0].Page != 1 {
		t.Fatalf("pagination requests=%#v", delegate.requests)
	}
}

func TestProductRejectsMalformedAndCrossAuthorityUpstream(t *testing.T) {
	tests := []struct {
		name string
		body string
		code int
	}{
		{"ansi prefix", "\x1b[31m{\"id\":1}", 8},
		{"trailing JSON", "{\"id\":1}{}", 8},
		{"cross authority", `{"id":1,"iid":2,"title":"unsafe","state":"opened","web_url":"https://evil.example/group/project/-/issues/2"}`, 9},
		{"wrong repository URL", `{"id":1,"iid":2,"title":"unsafe","state":"opened","web_url":"https://gitlab.com/other/project/-/issues/2"}`, 9},
		{"URL query", `{"id":1,"iid":2,"title":"unsafe","state":"opened","web_url":"https://gitlab.com/group/project/-/issues/2?private=query-sentinel"}`, 9},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{glab.OpIssueView: {{Body: []byte(test.body), UpstreamVersion: glab.SupportedVersion}}}}
			stdout, _, deps := productTestDeps(t, delegate)
			code := Run(context.Background(), []string{"issue", "view", "2", "-R", "group/project", "--hostname", "gitlab.com", "--format", "json"}, deps)
			if code != test.code {
				t.Fatalf("exit=%d output=%s", code, stdout.String())
			}
			if strings.Contains(stdout.String(), "evil.example") || strings.Contains(stdout.String(), "query-sentinel") || strings.Contains(stdout.String(), "\x1b") {
				t.Fatalf("raw upstream escaped into output: %q", stdout.String())
			}
		})
	}
}

func TestRepositoryNormalizerBoundsProjectIdentity(t *testing.T) {
	item := upstreamRepo{
		ID: 1, Name: "project", PathWithNamespace: strings.Repeat("a", 1024) + "/project",
		WebURL: "https://gitlab.com/group/project", Archived: false,
	}
	if _, _, err := normalizeRepo(item, "gitlab.com"); err == nil {
		t.Fatal("oversized repository identity was accepted")
	}
}

func TestReleaseNormalizerExposesOnlyProjectBoundDownloadMetadata(t *testing.T) {
	body := []byte(`{
		"name":"release","tag_name":"v1.0.0","upcoming_release":false,
		"assets":{"count":2,"sources":[{"format":"zip","url":"https://gitlab.com/group/project/-/archive/v1.0.0/project-v1.0.0.zip"}],
		"links":[{"name":"binary","url":"https://downloads.example.invalid/private","direct_asset_url":"https://gitlab.com/group/project/-/releases/v1.0.0/downloads/binary","link_type":"package"}]},
		"_links":{"self":"https://gitlab.com/group/project/-/releases/v1.0.0"}
	}`)
	release, truncated, err := normalizeReleaseObject(body, "gitlab.com", "group/project")
	if err != nil || truncated || release.AssetsCount != 2 || len(release.Assets) != 2 {
		t.Fatalf("release=%#v truncated=%v err=%v", release, truncated, err)
	}
	for _, asset := range release.Assets {
		if strings.Contains(asset.URL, "downloads.example.invalid") || !strings.Contains(asset.URL, "gitlab.com/group/project/") {
			t.Fatalf("unsafe release asset=%#v", asset)
		}
	}

	unsafe := bytes.Replace(body, []byte("https://gitlab.com/group/project/-/releases/v1.0.0/downloads/binary"), []byte("https://evil.example/binary"), 1)
	if _, _, err := normalizeReleaseObject(unsafe, "gitlab.com", "group/project"); err == nil {
		t.Fatal("cross-authority direct asset URL was accepted")
	}
}

func TestProductMRChecksFailsUnknownJobsClosed(t *testing.T) {
	body := `{
		"id":55,"status":"mystery","source":"merge_request_event","ref":"refs/merge-requests/7/head","sha":"0123456789012345678901234567890123456789","web_url":"https://gitlab.com/group/project/-/pipelines/55",
		"jobs":[{"id":91,"name":"verify","stage":"test","status":"future_state","allow_failure":false,"web_url":"https://gitlab.com/group/project/-/jobs/91"}]
	}`
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{glab.OpMRChecks: {{Body: []byte(body), UpstreamVersion: glab.SupportedVersion}}}}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), []string{"mr", "checks", "7", "-R", "group/project", "--hostname", "gitlab.com", "--format", "json"}, deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	var envelope struct {
		Data struct {
			Pipeline Pipeline `json:"pipeline"`
			Jobs     []Job    `json:"jobs"`
		} `json:"data"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Pipeline.Status != "running" || envelope.Data.Pipeline.RawStatus != "mystery" || len(envelope.Data.Jobs) != 1 || envelope.Data.Jobs[0].Status != "running" || envelope.Data.Jobs[0].RawStatus != "future_state" {
		t.Fatalf("unknown state normalized green: %#v", envelope.Data)
	}
}

func TestProductTraceIsTailBoundedAndRedacted(t *testing.T) {
	secret := strings.Join([]string{"glpat", "runtime", "trace", "sentinel"}, "-")
	trace := strings.Repeat("x", 300<<10) + "\nPRIVATE-TOKEN: " + secret + "\n"
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{glab.OpJobTrace: {{Body: []byte(trace), UpstreamVersion: glab.SupportedVersion}}}}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), []string{"job", "trace", "9", "-R", "group/project", "--hostname", "gitlab.com", "--format", "json"}, deps); code != 0 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), secret) || !strings.Contains(stdout.String(), "[REDACTED]") || !strings.Contains(stdout.String(), "trace_tail_limit") {
		t.Fatalf("trace boundary failure: output length=%d", stdout.Len())
	}
}

func TestDeniedAndHelpPathsNeverConstructDelegateOrResolveCredentials(t *testing.T) {
	for _, args := range [][]string{
		{"mr", "merge", "1", "--format", "json"},
		{"api", "projects", "--format", "json"},
		{"auth", "status", "--show-token"},
		{"auth", "login", "--token", "synthetic"},
		{"issue", "view", "--help"},
	} {
		var stdout bytes.Buffer
		deps := Dependencies{
			Runtime: runtimepkg.Dependencies{
				Stdout: &stdout, Stderr: io.Discard,
				LookupEnv: func(string) (string, bool) {
					t.Fatal("denied/help path resolved environment credentials")
					return "", false
				},
			},
			NewDelegate: func() delegateClient {
				t.Fatal("denied/help path constructed official-glab delegate")
				return nil
			},
		}
		code := Run(context.Background(), args, deps)
		if args[len(args)-1] == "--help" {
			if code != 0 {
				t.Fatalf("help %v exit=%d", args, code)
			}
		} else if code != 2 {
			t.Fatalf("denied %v exit=%d output=%s", args, code, stdout.String())
		}
	}
}

func TestProductAuthenticationDoesNotNormalizeOfficialCredentials(t *testing.T) {
	delegate := &fakeDelegate{statusErr: uxv1.NewError(uxv1.CodeAuthentication, "not authenticated")}
	stdout, _, deps := productTestDeps(t, delegate)
	if code := Run(context.Background(), []string{"auth", "status", "--hostname", "gitlab.com", "--format", "json"}, deps); code != 3 {
		t.Fatalf("exit=%d output=%s", code, stdout.String())
	}
	if strings.Contains(stdout.String(), "glpat-") {
		t.Fatalf("auth output exposed credential material: %s", stdout.String())
	}
}

func TestProductPaginationKeepsOnePageWidth(t *testing.T) {
	page := func(first, count int) glab.Response {
		items := make([]upstreamIssue, 0, count)
		for index := first; index < first+count; index++ {
			items = append(items, upstreamIssue{
				ID: int64(index), IID: int64(index), Title: "issue " + strconv.Itoa(index), State: "opened",
				WebURL: "https://gitlab.com/group/project/-/issues/" + strconv.Itoa(index),
			})
		}
		body, err := json.Marshal(items)
		if err != nil {
			t.Fatal(err)
		}
		return glab.Response{Body: body, UpstreamVersion: glab.SupportedVersion}
	}
	delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpIssueList: {page(1, 100), page(101, 51)},
	}}
	items, state, err := fetchIssues(context.Background(), delegate, Target{Host: "gitlab.com", Repo: "group/project"}, 150)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 150 || state.complete || !state.truncated || state.reason != "display_limit" {
		t.Fatalf("items=%d state=%#v", len(items), state)
	}
	if len(delegate.requests) != 2 || delegate.requests[0].PerPage != 100 || delegate.requests[1].PerPage != 100 {
		t.Fatalf("pagination changed page width: %#v", delegate.requests)
	}
}

func TestHumanTerminalDetectionRejectsNonTTYCharacterDevice(t *testing.T) {
	device, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}
	defer device.Close()
	if streamsAreTerminals(device, device, device) {
		t.Fatal("a character device without a terminal was accepted as human TTY")
	}
}

func productTestDeps(t *testing.T, delegate delegateClient) (*bytes.Buffer, *bytes.Buffer, Dependencies) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	deps := Dependencies{
		Runtime: runtimepkg.Dependencies{
			Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr, Cwd: t.TempDir(),
			LookupEnv: func(string) (string, bool) { return "", false },
		},
		NewDelegate: func() delegateClient { return delegate },
	}
	return &stdout, &stderr, deps
}
