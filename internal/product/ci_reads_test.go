package product

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

const ciTestSHA = "1123456789012345678901234567890123456789"

func ciArgs(args ...string) []string {
	return append(args, "-R", "group/project", "--hostname", "gitlab.com", "--format", "json")
}
func ciPipeline(id int64, status string) upstreamPipeline {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return upstreamPipeline{ID: id, IID: id + 100, Status: status, Source: "push", Ref: "main", SHA: ciTestSHA, WebURL: fmt.Sprintf("https://gitlab.com/group/project/-/pipelines/%d", id), UpdatedAt: &stamp}
}
func ciJob(id int64, status string) upstreamJob {
	pipeline := ciPipeline(55, "failed")
	return upstreamJob{ID: id, Name: "test", Stage: "test", Status: status, WebURL: fmt.Sprintf("https://gitlab.com/group/project/-/jobs/%d", id), Pipeline: &pipeline}
}
func ciResponse(value any) glab.Response {
	body, _ := json.Marshal(value)
	return glab.Response{Body: body, UpstreamVersion: glab.SupportedVersion}
}
func ciEnvelope(t *testing.T, body string) struct {
	OK    bool
	Meta  uxv1.Meta
	Data  map[string]json.RawMessage
	Error *uxv1.Error
} {
	t.Helper()
	var result struct {
		OK    bool
		Meta  uxv1.Meta
		Data  map[string]json.RawMessage
		Error *uxv1.Error
	}
	if err := json.Unmarshal([]byte(body), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestCIReadSelectorsFailBeforeDependencyWork(t *testing.T) {
	invalid := [][]string{
		{"pipeline", "list", "--ref", "../main"}, {"pipeline", "list", "--status", "failure"},
		{"pipeline", "list", "--source", "workflow"}, {"pipeline", "list", "--user", "@me"},
		{"pipeline", "list", "--sha", "abc"}, {"pipeline", "list", "--fields", "iid,iid"},
		{"pipeline", "list", "--fields", "sha"}, {"pipeline", "list", "--fields", "web_url"},
		{"pipeline", "list", "--fields", "updated_at"}, {"pipeline", "list", "--fields", "iid,sha"},
		{"pipeline", "list", "--fields", "steps"}, {"pipeline", "list", "--fields", "iid,"},
		{"pipeline", "list", "--status", "failed", "--status", "success"},
		{"pipeline", "view", "+55"}, {"pipeline", "watch", "055"},
		{"pipeline", "view", "55", "--trace"}, {"pipeline", "view", "55", "--trace", "--job-id", "9", "--trace-failed"},
		{"pipeline", "view", "55", "--trace-failed", "--job-status", "failed"},
		{"pipeline", "view", "55", "--job-status", "failure"},
		{"job", "list"}, {"job", "list", "--pipeline-id", "0"}, {"job", "list", "--pipeline-id", "55", "--job-id", "-9"},
		{"job", "list", "--pipeline-id", "55", "--status", "all"},
		{"job", "view", "9", "--pipeline-id", "055"}, {"job", "trace", "9", "--pipeline-id", "invalid"},
		{"pipeline", "watch", "55", "--timeout", "301"}, {"pipeline", "watch", "55", "--interval", "0"},
		{"pipeline", "watch", "55", "--timeout", "1", "--interval", "2"}, {"pipeline", "watch", "55", "--timeout", "1s"},
		{"pipeline", "watch", "55", "--limit", "2"}, {"pipeline", "list", "--workflow", "ci.yml"},
	}
	for _, args := range invalid {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			stdout, _, deps := productTestDeps(t, &fakeDelegate{})
			deps.NewDelegate = func() delegateClient { t.Fatal("invalid request reached dependency work"); return nil }
			if code := Run(context.Background(), ciArgs(args...), deps); code != 2 {
				t.Fatalf("code=%d output=%s", code, stdout)
			}
		})
	}
}

func TestCIListFiltersAndAdditiveFieldsAcrossPages(t *testing.T) {
	fake := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
		want := glab.PipelineFilters{Ref: "main", Status: "failed", Source: "push", User: "alice", SHA: ciTestSHA}
		if r.Operation != glab.OpPipelineList || r.PipelineFilters != want || r.PerPage != 100 {
			t.Fatalf("request=%#v", r)
		}
		items := []upstreamPipeline{}
		if r.Page == 1 {
			for i := int64(1); i <= 100; i++ {
				items = append(items, ciPipeline(i, "failed"))
			}
		}
		return ciResponse(items), nil, true
	}}
	stdout, _, deps := productTestDeps(t, fake)
	code := Run(context.Background(), ciArgs("pipeline", "list", "--ref", "main", "--status", "failed", "--source", "push", "--user", "alice", "--sha", ciTestSHA, "--fields", "iid", "--limit", "100"), deps)
	env := ciEnvelope(t, stdout.String())
	if code != 0 || !env.Meta.Complete || env.Meta.Truncated || len(fake.requests) != 2 || !strings.Contains(string(env.Data["pipelines"]), `"iid":101`) {
		t.Fatalf("code=%d requests=%#v output=%s", code, fake.requests, stdout)
	}
	if fake.requests[1].MaxResponseBytes >= fake.requests[0].MaxResponseBytes {
		t.Fatal("cumulative remaining capture budget did not shrink")
	}
}
func TestCIListAllPinnedEnumsAndDefaultFields(t *testing.T) {
	for _, status := range []string{"created", "waiting_for_resource", "preparing", "pending", "running", "success", "failed", "canceled", "skipped", "manual", "scheduled"} {
		t.Run(status, func(t *testing.T) {
			fake := &fakeDelegate{responses: map[glab.Operation][]glab.Response{glab.OpPipelineList: {ciResponse([]upstreamPipeline{ciPipeline(55, status)})}, glab.OpJobList: {ciResponse([]upstreamJob{ciJob(9, status)})}}}
			stdout, _, deps := productTestDeps(t, fake)
			if Run(context.Background(), ciArgs("pipeline", "list", "--status", status), deps) != 0 {
				t.Fatal(stdout)
			}
			var pipelines []Pipeline
			if err := json.Unmarshal(ciEnvelope(t, stdout.String()).Data["pipelines"], &pipelines); err != nil {
				t.Fatal(err)
			}
			if len(pipelines) != 1 || pipelines[0].IID != 0 || pipelines[0].SHA != ciTestSHA ||
				pipelines[0].WebURL != "https://gitlab.com/group/project/-/pipelines/55" || pipelines[0].UpdatedAt == nil {
				t.Fatal("default fields changed: ", stdout)
			}
			stdout.Reset()
			if Run(context.Background(), ciArgs("job", "list", "--pipeline-id", "55", "--status", status), deps) != 0 {
				t.Fatal(stdout)
			}
			if fake.requests[1].JobStatus != status {
				t.Fatal("status not passed to provider")
			}
		})
	}
	for _, source := range []string{"api", "chat", "external", "external_pull_request_event", "merge_request_event", "ondemand_dast_scan", "ondemand_dast_validation", "parent_pipeline", "pipeline", "push", "schedule", "security_orchestration_policy", "trigger", "web", "webide"} {
		if _, err := Parse(ciArgs("pipeline", "list", "--source", source)); err != nil {
			t.Fatalf("source=%s: %v", source, err)
		}
	}
}
func TestCIReadExactBindingAndWrongSelectors(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(*upstreamPipeline)
	}{
		{"ID", func(p *upstreamPipeline) { p.ID = 56 }},
		{"host", func(p *upstreamPipeline) { p.WebURL = strings.Replace(p.WebURL, "gitlab.com", "evil.example", 1) }},
		{"project suffix", func(p *upstreamPipeline) { p.WebURL = strings.Replace(p.WebURL, "/group/", "/other/group/", 1) }},
		{"URL ID", func(p *upstreamPipeline) { p.WebURL = strings.Replace(p.WebURL, "55", "56", 1) }},
		{"ref", func(p *upstreamPipeline) { p.Ref = "other" }},
		{"SHA", func(p *upstreamPipeline) { p.SHA = strings.Repeat("2", 40) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := ciPipeline(55, "success")
			test.mutate(&p)
			fake := &fakeDelegate{responses: map[glab.Operation][]glab.Response{glab.OpPipelineView: {ciResponse(p)}}}
			stdout, _, deps := productTestDeps(t, fake)
			if code := Run(context.Background(), ciArgs("pipeline", "view", "55", "--ref", "main", "--sha", ciTestSHA), deps); code != 9 {
				t.Fatalf("code=%d: %s", code, stdout)
			}
		})
	}
	for _, test := range []struct {
		name   string
		mutate func(*upstreamJob)
	}{
		{"ID", func(j *upstreamJob) { j.ID = 10 }},
		{"pipeline", func(j *upstreamJob) { j.Pipeline.ID = 56 }},
		{"missing pipeline", func(j *upstreamJob) { j.Pipeline = nil }},
		{"URL", func(j *upstreamJob) { j.WebURL = strings.Replace(j.WebURL, "gitlab.com", "evil.example", 1) }},
	} {
		t.Run("job "+test.name, func(t *testing.T) {
			j := ciJob(9, "failed")
			test.mutate(&j)
			fake := &fakeDelegate{responses: map[glab.Operation][]glab.Response{glab.OpJobView: {ciResponse(j)}}}
			stdout, _, deps := productTestDeps(t, fake)
			if code := Run(context.Background(), ciArgs("job", "trace", "9", "--pipeline-id", "55"), deps); code != 9 || len(fake.requests) != 1 {
				t.Fatalf("code=%d: %s", code, stdout)
			}
		})
	}
}

func TestCIReadListBoundaries(t *testing.T) {
	for _, test := range []struct {
		name         string
		limit, total int
		reason       string
		complete     bool
	}{{"exact", 1, 1, "", true}, {"display", 1, 2, "display_limit", false}, {"page", 1000, 1000, "hard_page_limit", false}} {
		t.Run(test.name, func(t *testing.T) {
			fake := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
				items := []upstreamJob{}
				start := (r.Page - 1) * r.PerPage
				for i := start; i < min(test.total, start+r.PerPage); i++ {
					items = append(items, ciJob(int64(i+1), "failed"))
				}
				return ciResponse(items), nil, true
			}}
			stdout, _, deps := productTestDeps(t, fake)
			if code := Run(context.Background(), ciArgs("job", "list", "--pipeline-id", "55", "--status", "failed", "--limit", fmt.Sprint(test.limit)), deps); code != 0 {
				t.Fatal(stdout)
			}
			env := ciEnvelope(t, stdout.String())
			if env.Meta.Complete != test.complete || env.Meta.Reason != test.reason || len(fake.requests) > 10 {
				t.Fatal(stdout)
			}
			for _, r := range fake.requests {
				if r.JobStatus != "failed" {
					t.Fatal("filter lost on pagination")
				}
			}
		})
	}
}

func TestCIFailedTracesBoundedAndRedacted(t *testing.T) {
	jobs := []upstreamJob{}
	for i := int64(1); i <= 6; i++ {
		j := ciJob(i, "failed")
		j.AllowFailure = true
		jobs = append(jobs, j)
	}
	secret := strings.Join([]string{"glpat", "synthetic", "trace", "secret"}, "-")
	trace := strings.Repeat("é", limits.MaxTraceBytes) + "\nPRIVATE-TOKEN: " + secret + "\nlast failure\n"
	fake := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
		switch r.Operation {
		case glab.OpPipelineView:
			return ciResponse(ciPipeline(55, "failed")), nil, true
		case glab.OpJobList:
			if r.JobStatus != "failed" {
				t.Fatal("not selecting failed jobs")
			}
			return ciResponse(jobs), nil, true
		case glab.OpJobView:
			return ciResponse(ciJob(r.ID, "failed")), nil, true
		case glab.OpJobTrace:
			return glab.Response{Body: []byte(trace)}, nil, true
		}
		t.Fatalf("unexpected operation %s", r.Operation)
		return glab.Response{}, nil, true
	}}
	stdout, _, deps := productTestDeps(t, fake)
	if code := Run(context.Background(), ciArgs("pipeline", "view", "55", "--trace-failed"), deps); code != 0 {
		t.Fatal(stdout)
	}
	env := ciEnvelope(t, stdout.String())
	var traces []selectedTrace
	if err := json.Unmarshal(env.Data["traces"], &traces); err != nil {
		t.Fatal(err)
	}
	if len(traces) != 5 || env.Meta.Complete || !env.Meta.Truncated || env.Meta.Reason != "trace_job_limit" || strings.Contains(stdout.String(), secret) || strings.Contains(stdout.String(), "full_log") {
		t.Fatalf("meta=%#v traces=%d", env.Meta, len(traces))
	}
	for _, trace := range traces {
		if !trace.Truncated || !strings.HasSuffix(trace.Trace, "last failure\n") || !utf8Valid([]byte(trace.Trace)) {
			t.Fatalf("invalid trace %d", trace.JobID)
		}
	}
}

func TestCITraceRedactionBeforeTailSelection(t *testing.T) {
	secret := strings.Join([]string{"opaque", "runtime", "trace", "sentinel"}, "-")
	const lastLine = "last failure\n"
	const marker = "[trace tail truncated]\n"
	const redactedHeader = "Authorization: [REDACTED]\n"
	boundaryTrace := func(offset int) string {
		padding := limits.MaxTraceBytes - len(secret) + offset - len(lastLine) - 1
		return strings.Repeat("earlier line\n", 32) + "Authorization: Bearer " + secret + "\n" + strings.Repeat("x", padding) + lastLine
	}
	for _, test := range []struct {
		name, body, want string
		truncated        bool
	}{
		{name: "cut inside header", body: boundaryTrace(-len("Bearer ")), truncated: true},
		{name: "cut before credential", body: boundaryTrace(0), truncated: true},
		{name: "cut inside credential", body: boundaryTrace(len(secret) / 2), truncated: true},
		{name: "multibyte tail", body: strings.Repeat("é", limits.MaxTraceBytes/2) + "x\nAuthorization: Bearer " + secret + "\n" + lastLine, truncated: true},
		{name: "short trace", body: "Authorization: Bearer " + secret + "\n" + lastLine, want: redactedHeader + lastLine},
		{name: "redaction shrinks below limit", body: "start\nAuthorization: Bearer " + strings.Repeat(secret, limits.MaxTraceBytes/len(secret)+1) + "\n" + lastLine, want: "start\n" + redactedHeader + lastLine},
		{name: "redaction expands above limit", body: strings.Repeat("x", limits.MaxTraceBytes-len("\nAuthorization: a\n")-len(lastLine)) + "\nAuthorization: a\n" + lastLine, truncated: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, args := range [][]string{
				{"pipeline", "view", "55", "--job-id", "9", "--trace"},
				{"pipeline", "view", "55", "--trace-failed"},
				{"job", "trace", "9"},
				{"job", "trace", "9", "--pipeline-id", "55"},
			} {
				t.Run(strings.Join(args, " "), func(t *testing.T) {
					fake := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
						switch r.Operation {
						case glab.OpPipelineView:
							return ciResponse(ciPipeline(55, "failed")), nil, true
						case glab.OpJobList:
							return ciResponse([]upstreamJob{ciJob(9, "failed")}), nil, true
						case glab.OpJobView:
							return ciResponse(ciJob(9, "failed")), nil, true
						case glab.OpJobTrace:
							if r.ID != 9 || len(test.body) > r.MaxResponseBytes {
								t.Fatal("unexpected trace identity or insufficient response budget")
							}
							return glab.Response{Body: []byte(test.body), UpstreamVersion: glab.SupportedVersion}, nil, true
						default:
							t.Fatalf("unexpected operation %s", r.Operation)
							return glab.Response{}, nil, true
						}
					}}
					stdout, stderr, deps := productTestDeps(t, fake)
					if code := Run(context.Background(), ciArgs(args...), deps); code != 0 || stderr.Len() != 0 {
						t.Fatalf("trace command failed: code=%d", code)
					}
					if strings.Contains(stdout.String()+stderr.String(), secret[len(secret)/2:]) {
						t.Fatal("opaque credential escaped into output")
					}
					env := ciEnvelope(t, stdout.String())
					wantReason := ""
					if test.truncated {
						wantReason = "trace_tail_limit"
					}
					if !env.OK || env.Meta.Complete != !test.truncated || env.Meta.Truncated != test.truncated || env.Meta.Reason != wantReason {
						t.Fatalf("unexpected trace metadata: %#v", env.Meta)
					}
					var trace string
					if args[0] == "pipeline" {
						var traces []selectedTrace
						if err := json.Unmarshal(env.Data["traces"], &traces); err != nil {
							t.Fatal(err)
						}
						if len(traces) != 1 || traces[0].JobID != 9 || traces[0].Truncated != test.truncated {
							t.Fatal("unexpected selected trace identity or truncation")
						}
						trace = traces[0].Trace
					} else {
						if err := json.Unmarshal(env.Data["trace"], &trace); err != nil {
							t.Fatal(err)
						}
					}
					if !strings.Contains(trace, "[REDACTED]") || !strings.HasSuffix(trace, lastLine) || !utf8.ValidString(trace) || strings.ContainsRune(trace, utf8.RuneError) {
						t.Fatal("redaction or UTF-8 tail content lost")
					}
					if strings.HasPrefix(trace, marker) != test.truncated || len(strings.TrimPrefix(trace, marker)) > limits.MaxTraceBytes {
						t.Fatal("trace tail exceeded its bound or misreported truncation")
					}
					if test.want != "" && trace != test.want {
						t.Fatal("untruncated redacted content changed")
					}
				})
			}
		})
	}
}

func TestCIJobSelectionAndStatusMismatch(t *testing.T) {
	for _, status := range []string{"failed", "manual", "future_state"} {
		fake := &fakeDelegate{responses: map[glab.Operation][]glab.Response{glab.OpPipelineView: {ciResponse(ciPipeline(55, "failed"))}, glab.OpJobView: {ciResponse(ciJob(9, status))}}}
		stdout, _, deps := productTestDeps(t, fake)
		code := Run(context.Background(), ciArgs("pipeline", "view", "55", "--job-id", "9", "--job-status", "failed"), deps)
		want := 0
		if status != "failed" {
			want = 6
		}
		if code != want || len(fake.requests) != 2 {
			t.Fatalf("code=%d: %s", code, stdout)
		}
	}
}
func TestPipelineWatchTruthfulCompletionAndBounds(t *testing.T) {
	tests := []struct {
		name              string
		statuses          []string
		mutate            func(int, *upstreamPipeline)
		timeout, interval string
		code              int
		reason            string
		polls             int
	}{
		{name: "complete", statuses: []string{"pending", "running", "success"}, code: 0, polls: 3},
		{name: "failed", statuses: []string{"failed"}, code: 6, reason: "watch_failed", polls: 1},
		{name: "manual", statuses: []string{"manual"}, code: 6, reason: "watch_manual", polls: 1},
		{name: "canceled", statuses: []string{"canceled"}, code: 6, reason: "watch_canceled", polls: 1},
		{name: "skipped", statuses: []string{"skipped"}, code: 6, reason: "watch_skipped", polls: 1},
		{name: "unknown", statuses: []string{"future_success"}, code: 9, reason: "watch_unknown", polls: 1},
		{name: "timeout", statuses: []string{"running"}, timeout: "3", interval: "1", code: 8, reason: "watch_timeout", polls: 3},
		{name: "request budget", statuses: []string{"running"}, timeout: "300", interval: "1", code: 8, reason: "watch_request_limit", polls: 100},
		{name: "stale", statuses: []string{"running", "success"}, mutate: func(i int, p *upstreamPipeline) {
			if i == 1 {
				stamp := p.UpdatedAt.Add(-time.Second)
				p.UpdatedAt = &stamp
			}
		}, code: 9, reason: "watch_stale", polls: 2},
		{name: "changed SHA", statuses: []string{"running", "success"}, mutate: func(i int, p *upstreamPipeline) {
			if i == 1 {
				p.SHA = strings.Repeat("2", 40)
			}
		}, code: 9, reason: "watch_stale", polls: 2},
		{name: "missing timestamp", statuses: []string{"success"}, mutate: func(_ int, p *upstreamPipeline) { p.UpdatedAt = nil }, code: 9, reason: "watch_unknown", polls: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			fake := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
				if r.Operation != glab.OpPipelineView || r.ID != 55 {
					t.Fatal(r)
				}
				p := ciPipeline(55, test.statuses[min(calls, len(test.statuses)-1)])
				if test.mutate != nil {
					test.mutate(calls, &p)
				}
				calls++
				return ciResponse(p), nil, true
			}}
			stdout, _, deps := productTestDeps(t, fake)
			now := time.Unix(0, 0)
			deps.watchClock = &watchClock{now: func() time.Time { return now }, wait: func(_ context.Context, d time.Duration) error {
				if stdout.Len() != 0 {
					t.Fatal("progress emitted as success")
				}
				now = now.Add(d)
				return nil
			}}
			args := []string{"pipeline", "watch", "55"}
			if test.timeout != "" {
				args = append(args, "--timeout", test.timeout, "--interval", test.interval)
			}
			code := Run(context.Background(), ciArgs(args...), deps)
			env := ciEnvelope(t, stdout.String())
			if code != test.code || env.Meta.Reason != test.reason || calls != test.polls || env.OK != (code == 0) || env.Meta.Complete != (code == 0) {
				t.Fatalf("calls=%d code=%d output=%s", calls, code, stdout)
			}
		})
	}
}
func TestCIWatchCancellationAndControlledErrors(t *testing.T) {
	for _, mode := range []string{"cancel wait", "cancel child", "forbidden", "rate limited", "bytes"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			fake := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
				calls++
				switch mode {
				case "cancel child":
					cancel()
					return ciResponse(ciPipeline(55, "success")), nil, true
				case "forbidden":
					return glab.Response{}, uxv1.NewError(uxv1.CodeForbidden, "denied"), true
				case "rate limited":
					return glab.Response{}, uxv1.NewError(uxv1.CodeRateLimited, "limited"), true
				case "bytes":
					return glab.Response{Body: make([]byte, limits.MaxOperationBytes+1)}, nil, true
				}
				return ciResponse(ciPipeline(55, "running")), nil, true
			}}
			stdout, _, deps := productTestDeps(t, fake)
			deps.watchClock = &watchClock{now: time.Now, wait: func(context.Context, time.Duration) error { cancel(); return context.Canceled }}
			code := Run(ctx, ciArgs("pipeline", "watch", "55"), deps)
			want := map[string]int{"cancel wait": 130, "cancel child": 130, "forbidden": 4, "rate limited": 7, "bytes": 8}[mode]
			if code != want || calls != 1 || ciEnvelope(t, stdout.String()).OK {
				t.Fatalf("code=%d calls=%d: %s", code, calls, stdout)
			}
		})
	}
}

func TestCIReadRejectsUnfaithfulProviderPagesAndRevision(t *testing.T) {
	for _, mode := range []string{"duplicate", "wrong status", "wrong source", "overflow page", "page failure", "wrong job revision"} {
		t.Run(mode, func(t *testing.T) {
			fake := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
				if r.Operation == glab.OpPipelineView {
					return ciResponse(ciPipeline(55, "failed")), nil, true
				}
				if r.Operation == glab.OpJobList {
					job := ciJob(9, "failed")
					job.Pipeline.SHA = strings.Repeat("2", 40)
					return ciResponse([]upstreamJob{job}), nil, true
				}
				if mode == "page failure" && r.Page == 2 {
					return glab.Response{}, uxv1.NewError(uxv1.CodeForbidden, "denied"), true
				}
				p := ciPipeline(55, "failed")
				if mode == "wrong status" {
					p.Status = "success"
				}
				if mode == "wrong source" {
					p.Source = "web"
				}
				items := []upstreamPipeline{p}
				if mode == "duplicate" {
					items = append(items, p)
				}
				if mode == "overflow page" || mode == "page failure" {
					items = nil
					count := r.PerPage
					if mode == "overflow page" {
						count++
					}
					for i := 0; i < count; i++ {
						items = append(items, ciPipeline(int64(i+1), "failed"))
					}
				}
				return ciResponse(items), nil, true
			}}
			stdout, _, deps := productTestDeps(t, fake)
			args := []string{"pipeline", "list", "--status", "failed", "--source", "push", "--limit", "100"}
			want := 8
			if mode == "page failure" {
				want = 4
			}
			if mode == "wrong job revision" {
				args = []string{"pipeline", "view", "55", "--jobs"}
				want = 9
			}
			if code := Run(context.Background(), ciArgs(args...), deps); code != want || ciEnvelope(t, stdout.String()).OK {
				t.Fatalf("code=%d: %s", code, stdout)
			}
		})
	}
}

func TestCIReadBudgetsRejectBeforeFurtherWork(t *testing.T) {
	fake := &fakeDelegate{doFunc: func(_ context.Context, r glab.Request) (glab.Response, error, bool) {
		if r.MaxResponseBytes != limits.MaxOperationBytes {
			t.Fatal("capture cap not narrowed")
		}
		return glab.Response{Body: make([]byte, limits.MaxOperationBytes)}, nil, true
	}}
	budget := &ciReadBudget{delegateClient: fake}
	if _, err := budget.Do(context.Background(), glab.Request{}); err != nil {
		t.Fatal(err)
	}
	if _, err := budget.Do(context.Background(), glab.Request{}); err == nil || len(fake.requests) != 1 {
		t.Fatal("cumulative byte exhaustion made another request")
	}
	budget = &ciReadBudget{delegateClient: fake, requests: ciMaxRequests}
	if _, err := budget.Do(context.Background(), glab.Request{}); err == nil || len(fake.requests) != 1 {
		t.Fatal("request exhaustion made another request")
	}
	// Escaped output, not only its unescaped source length, consumes the cap.
	out := commandOutput{data: map[string]any{"trace": strings.Repeat("\x01", ciMaxOutputBytes/5)}}
	if _, err := boundCIOutput(out, nil); err == nil {
		t.Fatal("escaped output exceeded final data budget")
	}
}

func TestCIReadConsumerFixture(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "contracts", "read-parity", "ci-reads.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		ReferenceCommit string     `json:"reference_commit"`
		Accepted        [][]string `json:"accepted"`
		Rejected        [][]string `json:"rejected"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.ReferenceCommit != "2bffd9a5b60ded64d6c9851683b27a480173a7ee" {
		t.Fatal("reference drift")
	}
	for _, argv := range fixture.Accepted {
		if _, err := Parse(ciArgs(argv...)); err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
	}
	for _, argv := range fixture.Rejected {
		if _, err := Parse(ciArgs(argv...)); err == nil {
			t.Fatalf("unexpected acceptance %v", argv)
		}
	}
}
