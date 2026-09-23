package product

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

func ciAuthorityCommands() [][]string {
	return [][]string{
		{"pipeline", "list"},
		{"pipeline", "view", "55", "--jobs"},
		{"pipeline", "view", "55", "--job-id", "9", "--trace"},
		{"pipeline", "watch", "55", "--timeout", "3", "--interval", "1"},
		{"job", "list", "--pipeline-id", "55"},
		{"job", "view", "9", "--pipeline-id", "55"},
		{"job", "trace", "9", "--pipeline-id", "55"},
	}
}

func ciAuthorityDelegate(t *testing.T, pipeline upstreamPipeline, job upstreamJob, host string) *fakeDelegate {
	t.Helper()
	return &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
		if r.Host != host || r.Repo != "group/project" {
			t.Fatalf("delegated target changed: %#v", r)
		}
		switch r.Operation {
		case glab.OpPipelineList:
			return ciResponse([]upstreamPipeline{pipeline}), nil, true
		case glab.OpPipelineView:
			return ciResponse(pipeline), nil, true
		case glab.OpJobList:
			return ciResponse([]upstreamJob{job}), nil, true
		case glab.OpJobView:
			return ciResponse(job), nil, true
		case glab.OpJobTrace:
			return glab.Response{Body: []byte("trace tail"), UpstreamVersion: glab.SupportedVersion}, nil, true
		default:
			t.Fatalf("unexpected provider operation: %s", r.Operation)
			return glab.Response{}, nil, true
		}
	}}
}

func TestCIReadTrustedWebBaseAndHostnameCase(t *testing.T) {
	for _, test := range []struct {
		name, host, base, returnedBase string
	}{
		{"default root", "gitlab.com", "", "https://gitlab.com"},
		{"uppercase target", "GITLAB.COM", "", "https://gitlab.com"},
		{"uppercase response", "gitlab.com", "", "https://GITLAB.COM"},
		{"explicit root", "GITLAB.COM", "https://gitlab.com/", "https://GiTlAb.CoM"},
		{"relative", "gitlab.example.invalid", "https://gitlab.example.invalid/gitlab", "https://gitlab.example.invalid/gitlab"},
		{"relative mixed case", "GITLAB.EXAMPLE.INVALID:8443", "https://gitlab.example.invalid:8443/GitLab/", "https://GitLab.Example.Invalid:8443/GitLab"},
		{"nested installation", "gitlab.example.invalid", "https://gitlab.example.invalid/tools/gitlab", "https://gitlab.example.invalid/tools/gitlab"},
	} {
		for _, command := range ciAuthorityCommands() {
			t.Run(test.name+"/"+strings.Join(command, " "), func(t *testing.T) {
				pipeline := ciPipeline(55, "success")
				pipeline.WebURL = test.returnedBase + "/group/project/-/pipelines/55"
				job := ciJob(9, "success")
				job.WebURL = test.returnedBase + "/group/project/-/jobs/9"
				job.Pipeline = &pipeline
				fake := ciAuthorityDelegate(t, pipeline, job, test.host)
				stdout, _, deps := productTestDeps(t, fake)
				args := append(append([]string{}, command...), "--hostname", test.host, "-R", "group/project", "--format", "json")
				if test.base != "" {
					args = append(args, "--web-base", test.base)
				}
				if code := Run(context.Background(), args, deps); code != 0 {
					t.Fatalf("code=%d: %s", code, stdout)
				}
				env := ciEnvelope(t, stdout.String())
				if !env.OK || !env.Meta.Complete || env.Meta.Host != test.host || len(fake.requests) == 0 {
					t.Fatal(stdout)
				}
				for _, key := range []string{"pipeline", "pipelines", "job", "jobs"} {
					body, ok := env.Data[key]
					if !ok {
						continue
					}
					if key == "pipeline" || key == "job" {
						body = append(append([]byte{'['}, body...), ']')
					}
					var resources []struct {
						WebURL string `json:"web_url"`
					}
					if err := json.Unmarshal(body, &resources); err != nil {
						t.Fatal(err)
					}
					want := pipeline.WebURL
					if key == "job" || key == "jobs" {
						want = job.WebURL
					}
					if len(resources) != 1 || resources[0].WebURL != want {
						t.Fatalf("%s: %s", key, body)
					}
				}
			})
		}
	}
}

func TestCIReadRejectsHostileURLPrefixes(t *testing.T) {
	for _, basePath := range []string{"", "/gitlab"} {
		for _, test := range []struct {
			name   string
			mutate func(string) string
		}{
			{"untrusted installation", func(raw string) string { return strings.Replace(raw, "gitlab.com/", "gitlab.com/evil/", 1) }},
			{"project prefix", func(raw string) string { return strings.Replace(raw, "/group/", "/other/group/", 1) }},
			{"project case", func(raw string) string { return strings.Replace(raw, "/group/", "/Group/", 1) }},
			{"installation mismatch", func(raw string) string { return strings.Replace(raw, "gitlab.com"+basePath, "gitlab.com/GitLab", 1) }},
			{"encoded project slash", func(raw string) string { return strings.Replace(raw, "/group/", "/group%2F", 1) }},
			{"encoded installation slash", func(raw string) string { return strings.Replace(raw, "gitlab.com/", "gitlab.com/%2F", 1) }},
			{"dot segments", func(raw string) string { return strings.Replace(raw, "/group/", "/other/../group/", 1) }},
			{"encoded dot segments", func(raw string) string { return strings.Replace(raw, "/group/", "/other/%2e%2e/group/", 1) }},
			{"double slash", func(raw string) string { return strings.Replace(raw, "/group/", "//group/", 1) }},
			{"host suffix", func(raw string) string { return strings.Replace(raw, "gitlab.com", "gitlab.com.evil.invalid", 1) }},
			{"port", func(raw string) string { return strings.Replace(raw, "gitlab.com", "gitlab.com:443", 1) }},
			{"userinfo", func(raw string) string { return strings.Replace(raw, "https://", "https://user@", 1) }},
			{"scheme", func(raw string) string { return strings.Replace(raw, "https:", "http:", 1) }},
			{"resource", func(raw string) string { return strings.Replace(raw, "/-/", "/-/other/", 1) }},
			{"id", func(raw string) string { return raw + "0" }},
			{"trailing slash", func(raw string) string { return raw + "/" }},
			{"query", func(raw string) string { return raw + "?a=b" }},
			{"empty query", func(raw string) string { return raw + "?" }},
			{"fragment", func(raw string) string { return raw + "#other" }},
			{"empty fragment", func(raw string) string { return raw + "#" }},
		} {
			for _, command := range ciAuthorityCommands() {
				t.Run(basePath+"/"+test.name+"/"+strings.Join(command, " "), func(t *testing.T) {
					pipeline := ciPipeline(55, "success")
					pipeline.WebURL = test.mutate("https://gitlab.com" + basePath + "/group/project/-/pipelines/55")
					job := ciJob(9, "success")
					job.WebURL = test.mutate("https://gitlab.com" + basePath + "/group/project/-/jobs/9")
					fake := ciAuthorityDelegate(t, pipeline, job, "gitlab.com")
					stdout, _, deps := productTestDeps(t, fake)
					args := append(append([]string{}, command...), "--web-base", "https://gitlab.com"+basePath)
					if code := Run(context.Background(), ciArgs(args...), deps); code != 9 {
						t.Fatalf("code=%d: %s", code, stdout)
					}
					env := ciEnvelope(t, stdout.String())
					if env.OK || env.Meta.Complete || env.Error == nil || env.Error.Code != uxv1.CodeSafety || len(fake.requests) != 1 {
						t.Fatalf("requests=%#v output=%s", fake.requests, stdout)
					}
				})
			}
		}
	}
}

func TestCIReadNestedPipelineUsesTrustedWebBase(t *testing.T) {
	for _, pipelineURL := range []string{
		"https://gitlab.com/group/project/-/pipelines/55",
		"https://gitlab.com/evil/gitlab/group/project/-/pipelines/55",
		"https://gitlab.com/gitlab/other/group/project/-/pipelines/55",
		"https://gitlab.com/gitlab/group/project/-/pipelines/56",
	} {
		t.Run(pipelineURL, func(t *testing.T) {
			job := ciJob(9, "success")
			job.WebURL = "https://gitlab.com/gitlab/group/project/-/jobs/9"
			job.Pipeline.WebURL = pipelineURL
			fake := ciAuthorityDelegate(t, *job.Pipeline, job, "gitlab.com")
			stdout, _, deps := productTestDeps(t, fake)
			if code := Run(context.Background(), ciArgs("job", "trace", "9", "--pipeline-id", "55", "--web-base", "https://gitlab.com/gitlab"), deps); code != 9 || len(fake.requests) != 1 {
				t.Fatalf("code=%d requests=%#v: %s", code, fake.requests, stdout)
			}
		})
	}
}

func TestCIReadInvalidWebBaseStopsBeforeDelegation(t *testing.T) {
	for _, base := range []string{
		"http://gitlab.com/gitlab", "https://evil.invalid/gitlab", "https://gitlab.com:443/gitlab",
		"https://user@gitlab.com/gitlab", "https://gitlab.com/gitlab?", "https://gitlab.com/gitlab?a=b",
		"https://gitlab.com/gitlab#", "https://gitlab.com/gitlab#fragment", "//gitlab.com/gitlab",
		"https://gitlab.com/gitlab/../other", "https://gitlab.com/gitlab/./other", "https://gitlab.com//gitlab",
		"https://gitlab.com/gitlab//", "https://gitlab.com//", "https://gitlab.com/%2e%2e/gitlab", "https://gitlab.com/gitlab%2Fother",
		"https://gitlab.com/gitlab%5Cother", "https://gitlab.com/%252e%252e", "https://gitlab.com/%00",
		"https://gitlab.com/%ff", "https://gitlab.com/%", "https://gitlab.com/" + strings.Repeat("a", 2048),
	} {
		for _, command := range ciAuthorityCommands() {
			t.Run(base+"/"+strings.Join(command, " "), func(t *testing.T) {
				stdout, _, deps := productTestDeps(t, &fakeDelegate{})
				deps.NewDelegate = func() delegateClient { t.Fatal("invalid web base reached delegate"); return nil }
				args := append(append([]string{}, command...), "--web-base", base)
				if code := Run(context.Background(), ciArgs(args...), deps); code != 2 {
					t.Fatalf("code=%d: %s", code, stdout)
				}
			})
		}
	}
	for _, command := range [][]string{{"auth", "status"}, {"issue", "list"}, {"mr", "list"}} {
		stdout, _, deps := productTestDeps(t, &fakeDelegate{})
		deps.NewDelegate = func() delegateClient { t.Fatal("unrelated command reached delegate"); return nil }
		args := append(command, "--web-base", "https://gitlab.com/gitlab", "--format", "json")
		if code := Run(context.Background(), args, deps); code != 2 {
			t.Fatalf("code=%d: %s", code, stdout)
		}
	}
}
