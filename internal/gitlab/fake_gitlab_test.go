package gitlab_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"glab-axi/internal/app"
	"glab-axi/internal/auth"
	"glab-axi/internal/config"
	v1 "glab-axi/internal/contract/v1"
	"glab-axi/internal/gitlab"
	"glab-axi/internal/limits"
	"glab-axi/internal/safeurl"
	"glab-axi/internal/testgitlab"
)

const (
	testProject   = "group/project"
	testProjectID = int64(71)
	testSHA       = "0123456789abcdef0123456789abcdef01234567"
)

type scenario struct {
	mu      sync.Mutex
	base    string
	handler func(http.ResponseWriter, *http.Request)
}

func (s *scenario) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.handler(w, r)
}

func TestFakeGitLab(t *testing.T) {
	t.Run("paginated current pipeline normalizes fail closed", testPaginatedStatus)
	t.Run("cross origin pagination is refused", testUnsafePagination)
	t.Run("redirects and TLS fail closed", testTransportSafety)
	t.Run("create race is reconciled without duplicate post", testCreateRace)
	t.Run("ambiguous update is reconciled without second put", testUpdateAmbiguity)
	t.Run("duplicate merge requests fail closed", testDuplicateMR)
	t.Run("stale pipeline never becomes green", testStalePipeline)
	t.Run("trace is tailed and redacted", testTraceBound)
	t.Run("job and page caps reject partial snapshots", testPaginationCaps)
	t.Run("get retries are bounded", testRetry)
	t.Run("rate limits are bounded", testRateLimit)
	t.Run("cancellation interrupts request", testCancellation)
	t.Run("returned project authority is exact", testProjectMismatch)
}

func testPaginatedStatus(t *testing.T) {
	state := &scenario{}
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		projectPath := "/api/v4/projects/" + testProject
		switch {
		case r.URL.Path == projectPath:
			jsonResponse(w, 200, projectObject(state.base))
		case r.URL.Path == projectPath+"/merge_requests/1":
			jsonResponse(w, 200, mrObject(state.base, "feature", "main", testSHA, 99, "Title", "Body"))
		case r.URL.Path == projectPath+"/pipelines/99/jobs" && r.URL.Query().Get("page") == "":
			next := state.base + r.URL.Path + "?include_retried=false&page=2&per_page=100"
			w.Header().Set("Link", "<"+next+">; rel=\"next\"")
			jsonResponse(w, 200, []any{
				job(1, "passed", "success", false, testSHA),
				job(2, "blocking-manual", "manual", false, testSHA),
				job(3, "optional-failure", "failed", true, testSHA),
			})
		case r.URL.Path == projectPath+"/pipelines/99/jobs" && r.URL.Query().Get("page") == "2":
			jsonResponse(w, 200, []any{
				job(4, "new-state", "waiting_for_callback", false, testSHA),
				job(5, "unknown", "provider_future_state", false, testSHA),
			})
		default:
			jsonResponse(w, 404, map[string]string{"message": "not found"})
		}
	}
	service, server, token := newService(t, state, testSHA, nil)
	defer server.Close()
	result, err := service.CIStatus(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Jobs) != 5 {
		t.Fatalf("jobs=%d, want 5", len(result.Jobs))
	}
	want := []string{"success", "running", "skipped", "running", "running"}
	for i, status := range want {
		if result.Jobs[i].Status != status {
			t.Fatalf("job %d status=%q, want %q", i, result.Jobs[i].Status, status)
		}
	}
	if result.Jobs[4].RawStatus != "provider_future_state" {
		t.Fatal("unknown status did not remain diagnosable")
	}
	assertCredentialBoundary(t, server.Requests(), token)
}

func testUnsafePagination(t *testing.T) {
	state := &scenario{}
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		projectPath := "/api/v4/projects/" + testProject
		switch r.URL.Path {
		case projectPath:
			jsonResponse(w, 200, projectObject(state.base))
		case projectPath + "/pipelines/99/jobs":
			w.Header().Set("Link", `<https://attacker.invalid/api/v4/projects/x/pipelines/99/jobs?page=2>; rel="next"`)
			jsonResponse(w, 200, []any{job(1, "pass", "success", false, testSHA)})
		default:
			jsonResponse(w, 404, map[string]string{"message": "not found"})
		}
	}
	service, server, _ := newService(t, state, "", nil)
	defer server.Close()
	_, err := service.NormalizedJobs(context.Background(), 99)
	if v1.ExitCode(err) != 9 {
		t.Fatalf("exit=%d, want safety exit 9: %v", v1.ExitCode(err), err)
	}
	if len(server.Requests()) != 2 {
		t.Fatalf("unsafe Link caused unexpected request count: %d", len(server.Requests()))
	}
}

func testTransportSafety(t *testing.T) {
	t.Run("cross-origin redirect", func(t *testing.T) {
		target := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(w, 200, map[string]any{"id": 1, "username": "should-not-be-reached"})
		}))
		defer target.Close()
		origin := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, target.HTTP.URL+r.URL.Path, http.StatusFound)
		}))
		defer origin.Close()
		client := directClient(t, origin, true)
		_, err := client.AuthenticatedUser(context.Background())
		if v1.ExitCode(err) != 9 || len(target.Requests()) != 0 {
			t.Fatalf("redirect exit=%d target_requests=%d err=%v", v1.ExitCode(err), len(target.Requests()), err)
		}
	})

	t.Run("untrusted certificate", func(t *testing.T) {
		server := testgitlab.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			jsonResponse(w, 200, map[string]any{"id": 1, "username": "bot"})
		}))
		defer server.Close()
		client := directClient(t, server, false)
		_, err := client.AuthenticatedUser(context.Background())
		if v1.ExitCode(err) != 8 {
			t.Fatalf("untrusted TLS exit=%d err=%v", v1.ExitCode(err), err)
		}
	})
}

func testCreateRace(t *testing.T) {
	state := &scenario{}
	var existing map[string]any
	posts := 0
	lists := 0
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		projectPath := "/api/v4/projects/" + testProject
		switch {
		case r.URL.Path == projectPath:
			jsonResponse(w, 200, projectObject(state.base))
		case r.URL.Path == projectPath+"/merge_requests" && r.Method == http.MethodGet:
			lists++
			if existing == nil {
				jsonResponse(w, 200, []any{})
			} else {
				jsonResponse(w, 200, []any{existing})
			}
		case r.URL.Path == projectPath+"/merge_requests" && r.Method == http.MethodPost:
			posts++
			existing = mrObject(state.base, "feature", "main", testSHA, 0, "Title", "Body")
			jsonResponse(w, 422, map[string]string{"message": "already exists"})
		default:
			jsonResponse(w, 404, map[string]string{"message": "not found"})
		}
	}
	service, server, _ := newService(t, state, "", nil)
	defer server.Close()
	result, err := service.Create(context.Background(), "feature", "main", "Title", "Body")
	if err != nil || result.Action != "replayed" {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if posts != 1 || lists != 2 {
		t.Fatalf("posts=%d lists=%d; create was replayed unsafely", posts, lists)
	}
}

func testUpdateAmbiguity(t *testing.T) {
	state := &scenario{}
	current := mrObject("", "feature", "main", testSHA, 0, "Old", "Old body")
	puts := 0
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		projectPath := "/api/v4/projects/" + testProject
		current["web_url"] = state.base + "/" + testProject + "/-/merge_requests/1"
		switch {
		case r.URL.Path == projectPath:
			jsonResponse(w, 200, projectObject(state.base))
		case r.URL.Path == projectPath+"/merge_requests/1" && r.Method == http.MethodGet:
			jsonResponse(w, 200, current)
		case r.URL.Path == projectPath+"/merge_requests/1" && r.Method == http.MethodPut:
			puts++
			current["title"] = "New"
			current["description"] = "New body"
			jsonResponse(w, 503, map[string]string{"message": "response lost"})
		default:
			jsonResponse(w, 404, map[string]string{"message": "not found"})
		}
	}
	service, server, _ := newService(t, state, "", nil)
	defer server.Close()
	result, err := service.Update(context.Background(), 1, "New", "New body")
	if err != nil || result.Action != "replayed" || puts != 1 {
		t.Fatalf("result=%+v puts=%d err=%v", result, puts, err)
	}
}

func testDuplicateMR(t *testing.T) {
	state := &scenario{}
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		projectPath := "/api/v4/projects/" + testProject
		switch {
		case r.URL.Path == projectPath:
			jsonResponse(w, 200, projectObject(state.base))
		case r.URL.Path == projectPath+"/merge_requests" && r.URL.Query().Get("page") == "":
			next := state.base + r.URL.Path + "?page=2&per_page=100&source_branch=feature&state=opened&target_branch=main"
			w.Header().Set("Link", "<"+next+">; rel=\"next\"")
			jsonResponse(w, 200, []any{mrObject(state.base, "feature", "main", testSHA, 0, "One", "Body")})
		case r.URL.Path == projectPath+"/merge_requests" && r.URL.Query().Get("page") == "2":
			second := mrObject(state.base, "feature", "main", testSHA, 0, "Two", "Body")
			second["iid"] = 2
			second["web_url"] = state.base + "/" + testProject + "/-/merge_requests/2"
			jsonResponse(w, 200, []any{second})
		default:
			jsonResponse(w, 404, map[string]string{"message": "not found"})
		}
	}
	service, server, _ := newService(t, state, "", nil)
	defer server.Close()
	_, err := service.List(context.Background(), "feature", "main")
	if v1.ExitCode(err) != 6 {
		t.Fatalf("exit=%d, want conflict: %v", v1.ExitCode(err), err)
	}
}

func testStalePipeline(t *testing.T) {
	state := &scenario{}
	jobsCalls := 0
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		projectPath := "/api/v4/projects/" + testProject
		switch r.URL.Path {
		case projectPath:
			jsonResponse(w, 200, projectObject(state.base))
		case projectPath + "/merge_requests/1":
			jsonResponse(w, 200, mrObject(state.base, "feature", "main", testSHA, 99, "Title", "Body"))
		case projectPath + "/pipelines/99/jobs":
			jobsCalls++
			jsonResponse(w, 200, []any{job(1, "pass", "success", false, testSHA)})
		default:
			jsonResponse(w, 404, map[string]string{"message": "not found"})
		}
	}
	service, server, _ := newService(t, state, strings.Repeat("a", 40), nil)
	defer server.Close()
	result, err := service.CIStatus(context.Background(), 1)
	if err != nil || len(result.Jobs) != 1 || result.Jobs[0].Status != "running" || result.Jobs[0].RawStatus != "stale_pipeline" || jobsCalls != 0 {
		t.Fatalf("jobs=%+v calls=%d err=%v", result.Jobs, jobsCalls, err)
	}
}

func testTraceBound(t *testing.T) {
	secret := runtimeToken()
	state := &scenario{}
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		projectPath := "/api/v4/projects/" + testProject
		switch r.URL.Path {
		case projectPath:
			jsonResponse(w, 200, projectObject(state.base))
		case projectPath + "/jobs/10/trace":
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(200)
			_, _ = io.WriteString(w, strings.Repeat("x", limits.MaxTraceBytes+1024)+"\n\x1b[31mAuthorization: Bearer "+secret+"\nend")
		default:
			jsonResponse(w, 404, map[string]string{"message": "not found"})
		}
	}
	service, server, _ := newServiceWithToken(t, state, "", nil, secret)
	defer server.Close()
	trace, truncated, err := service.Trace(context.Background(), 10)
	if err != nil || !truncated || len(trace) > limits.MaxTraceBytes || !strings.Contains(string(trace), "[REDACTED]") || strings.Contains(string(trace), secret) || strings.ContainsRune(string(trace), '\x1b') || !strings.Contains(string(trace), "trace truncated") {
		t.Fatalf("trace bounds/redaction failed: len=%d truncated=%v err=%v", len(trace), truncated, err)
	}
}

func testPaginationCaps(t *testing.T) {
	t.Run("job cap", func(t *testing.T) {
		state := &scenario{}
		jobCount := limits.MaxJobs
		state.handler = func(w http.ResponseWriter, r *http.Request) {
			projectPath := "/api/v4/projects/" + testProject
			switch r.URL.Path {
			case projectPath:
				jsonResponse(w, 200, projectObject(state.base))
			case projectPath + "/pipelines/99/jobs":
				jobs := make([]any, jobCount)
				for i := range jobs {
					jobs[i] = job(int64(i+1), "bounded-job", "success", false, testSHA)
				}
				jsonResponse(w, 200, jobs)
			default:
				jsonResponse(w, 404, map[string]string{"message": "not found"})
			}
		}
		service, server, _ := newService(t, state, "", nil)
		defer server.Close()
		if jobs, err := service.NormalizedJobs(context.Background(), 99); err != nil || len(jobs) != limits.MaxJobs {
			t.Fatalf("exact job cap len=%d err=%v", len(jobs), err)
		}
		state.mu.Lock()
		jobCount = limits.MaxJobs + 1
		state.mu.Unlock()
		if _, err := service.NormalizedJobs(context.Background(), 99); v1.ExitCode(err) != 8 {
			t.Fatalf("job cap+1 exit=%d err=%v", v1.ExitCode(err), err)
		}
	})

	t.Run("page cap", func(t *testing.T) {
		state := &scenario{}
		overflow := false
		state.handler = func(w http.ResponseWriter, r *http.Request) {
			projectPath := "/api/v4/projects/" + testProject
			switch r.URL.Path {
			case projectPath:
				jsonResponse(w, 200, projectObject(state.base))
			case projectPath + "/pipelines/99/jobs":
				pageText := r.URL.Query().Get("page")
				if pageText == "" {
					pageText = "1"
				}
				pageNumber := 1
				_, _ = fmt.Sscan(pageText, &pageNumber)
				if pageNumber < limits.MaxPages || overflow {
					next := fmt.Sprintf("%s%s?include_retried=false&page=%d&per_page=100", state.base, r.URL.Path, pageNumber+1)
					w.Header().Set("Link", "<"+next+">; rel=\"next\"")
				}
				jsonResponse(w, 200, []any{})
			default:
				jsonResponse(w, 404, map[string]string{"message": "not found"})
			}
		}
		service, server, _ := newService(t, state, "", nil)
		defer server.Close()
		if jobs, err := service.NormalizedJobs(context.Background(), 99); err != nil || len(jobs) != 0 {
			t.Fatalf("exact page cap len=%d err=%v", len(jobs), err)
		}
		server.Reset()
		state.mu.Lock()
		overflow = true
		state.mu.Unlock()
		if _, err := service.NormalizedJobs(context.Background(), 99); v1.ExitCode(err) != 8 {
			t.Fatalf("page cap+1 exit=%d err=%v", v1.ExitCode(err), err)
		}
		if got := len(server.Requests()); got != limits.MaxPages+1 { // one project request plus ten pages
			t.Fatalf("request count=%d, want %d", got, limits.MaxPages+1)
		}
	})
}

func testRetry(t *testing.T) {
	state := &scenario{}
	calls := 0
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v4/user" {
			jsonResponse(w, 404, map[string]string{"message": "not found"})
			return
		}
		calls++
		if calls == 1 {
			jsonResponse(w, 503, map[string]string{"message": "temporary"})
			return
		}
		jsonResponse(w, 200, map[string]any{"id": 1, "username": "bot"})
	}
	sleeps := 0
	sleep := func(ctx context.Context, duration time.Duration) error {
		sleeps++
		if duration != 250*time.Millisecond {
			t.Fatalf("unexpected retry delay: %s", duration)
		}
		return nil
	}
	service, server, _ := newService(t, state, "", sleep)
	defer server.Close()
	if err := service.AuthStatus(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if calls != 2 || sleeps != 1 {
		t.Fatalf("calls=%d sleeps=%d", calls, sleeps)
	}
}

func testRateLimit(t *testing.T) {
	state := &scenario{}
	calls := 0
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Retry-After", "31")
		jsonResponse(w, http.StatusTooManyRequests, map[string]string{"message": "slow down"})
	}
	sleeps := 0
	service, server, _ := newService(t, state, "", func(context.Context, time.Duration) error {
		sleeps++
		return nil
	})
	defer server.Close()
	err := service.AuthStatus(context.Background(), false)
	if v1.ExitCode(err) != 7 || calls != 1 || sleeps != 0 {
		t.Fatalf("exit=%d calls=%d sleeps=%d err=%v", v1.ExitCode(err), calls, sleeps, err)
	}
}

func testCancellation(t *testing.T) {
	state := &scenario{}
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}
	service, server, _ := newService(t, state, "", nil)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	err := service.AuthStatus(ctx, false)
	if v1.ExitCode(err) != 130 || time.Since(start) > time.Second {
		t.Fatalf("cancellation exit=%d duration=%s err=%v", v1.ExitCode(err), time.Since(start), err)
	}
}

func testProjectMismatch(t *testing.T) {
	state := &scenario{}
	state.handler = func(w http.ResponseWriter, r *http.Request) {
		jsonResponse(w, 200, map[string]any{"id": testProjectID, "path_with_namespace": "other/project", "web_url": state.base + "/other/project"})
	}
	service, server, _ := newService(t, state, "", nil)
	defer server.Close()
	_, err := service.NormalizedJobs(context.Background(), 99)
	if v1.ExitCode(err) != 9 {
		t.Fatalf("mismatched project exit=%d, want safety: %v", v1.ExitCode(err), err)
	}
}

func newService(t *testing.T, state *scenario, localSHA string, sleep gitlab.SleepFunc) (*app.Service, *testgitlab.Server, string) {
	return newServiceWithToken(t, state, localSHA, sleep, runtimeToken())
}

func newServiceWithToken(t *testing.T, state *scenario, localSHA string, sleep gitlab.SleepFunc, token string) (*app.Service, *testgitlab.Server, string) {
	t.Helper()
	server := testgitlab.New(state)
	state.base = server.HTTP.URL
	ca, err := server.CAFile(t.TempDir())
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	hostName := strings.TrimPrefix(server.HTTP.URL, "https://")
	authority, err := safeurl.NewAuthority(hostName, server.HTTP.URL+"/api/v4", server.HTTP.URL)
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	host := config.ResolvedHost{Name: hostName, Authority: authority, CABundle: ca, ProxyDisabled: true, Explicit: true}
	client, err := gitlab.NewClient(host, auth.Credential{Value: token, Kind: auth.PrivateToken}, gitlab.Options{Sleep: sleep})
	if err != nil {
		server.Close()
		t.Fatal(err)
	}
	return &app.Service{Client: client, Host: host, Project: testProject, LocalSHA: localSHA}, server, token
}

func directClient(t *testing.T, server *testgitlab.Server, trustCA bool) *gitlab.Client {
	t.Helper()
	hostName := strings.TrimPrefix(server.HTTP.URL, "https://")
	authority, err := safeurl.NewAuthority(hostName, server.HTTP.URL+"/api/v4", server.HTTP.URL)
	if err != nil {
		t.Fatal(err)
	}
	host := config.ResolvedHost{Name: hostName, Authority: authority, ProxyDisabled: true, Explicit: true}
	if trustCA {
		host.CABundle, err = server.CAFile(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
	}
	client, err := gitlab.NewClient(host, auth.Credential{Value: runtimeToken(), Kind: auth.PrivateToken}, gitlab.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func projectObject(base string) map[string]any {
	return map[string]any{"id": testProjectID, "path_with_namespace": testProject, "web_url": base + "/" + testProject}
}

func mrObject(base, source, target, sha string, pipelineID int64, title, description string) map[string]any {
	value := map[string]any{
		"iid": 1, "web_url": base + "/" + testProject + "/-/merge_requests/1", "state": "opened",
		"title": title, "description": description, "source_branch": source, "target_branch": target,
		"source_project_id": testProjectID, "target_project_id": testProjectID, "sha": sha,
		"has_conflicts": false, "detailed_merge_status": "mergeable", "merge_status": "can_be_merged",
	}
	if pipelineID > 0 {
		value["head_pipeline"] = map[string]any{"id": pipelineID, "sha": sha, "status": "success"}
	}
	return value
}

func job(id int64, name, status string, allowed bool, sha string) map[string]any {
	return map[string]any{
		"id": id, "name": name, "stage": "test", "status": status, "allow_failure": allowed,
		"finished_at": "2026-08-07T12:00:00Z", "pipeline": map[string]any{"id": 99, "sha": sha, "status": "running"},
	}
}

func jsonResponse(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func runtimeToken() string {
	return strings.Join([]string{"glpat", "runtime", "fake", "sentinel"}, "-")
}

func assertCredentialBoundary(t *testing.T, requests []testgitlab.Request, token string) {
	t.Helper()
	if len(requests) == 0 {
		t.Fatal("expected fake-server requests")
	}
	for _, request := range requests {
		if request.Header.Get("PRIVATE-TOKEN") != token {
			t.Fatal("credential was not sent in the private token header")
		}
		if strings.Contains(request.URL, token) || strings.Contains(string(request.Body), token) {
			t.Fatal("credential reached request URL or body")
		}
	}
}
