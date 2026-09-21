package product

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

// This wrapper is confined to product CI reads. In particular, filtered reads
// never feed the unfiltered jobs/bridges proof in guarded merge or native v1.
type ciReadBudget struct {
	delegateClient
	requests int
	bytes    int
}

func (b *ciReadBudget) Do(ctx context.Context, r glab.Request) (glab.Response, error) {
	if err := ctx.Err(); err != nil {
		return glab.Response{}, ciContextError(ctx)
	}
	if b.requests >= ciMaxRequests || b.bytes >= limits.MaxOperationBytes {
		return glab.Response{}, uxv1.NewError(uxv1.CodeUpstream, "CI read cumulative request or byte budget exhausted")
	}
	b.requests++
	r.MaxResponseBytes = limits.MaxOperationBytes - b.bytes
	response, err := b.delegateClient.Do(ctx, r)
	b.bytes += len(response.Body)
	if b.bytes > limits.MaxOperationBytes {
		return glab.Response{}, uxv1.NewError(uxv1.CodeUpstream, "CI read cumulative byte budget exhausted")
	}
	return response, err
}
func ciContextError(ctx context.Context) error {
	if ctx.Err() == context.Canceled {
		return uxv1.NewError(uxv1.CodeCanceled, "CI read canceled by caller")
	}
	return uxv1.NewError(uxv1.CodeUpstream, "CI read timed out")
}
func boundCIOutput(out commandOutput, err error) (commandOutput, error) {
	if err != nil {
		return out, err
	}
	data, marshalErr := json.Marshal(out.data)
	if marshalErr != nil {
		return out, uxv1.NewError(uxv1.CodeInternal, "cannot encode CI read")
	}
	if len(data) > ciMaxOutputBytes {
		return commandOutput{meta: out.meta}, uxv1.NewError(uxv1.CodeUpstream, "CI read output budget exceeded")
	}
	return out, nil
}
func canonicalCIURL(target Target, resource string, id int64) string {
	return (&url.URL{Scheme: "https", Host: target.Host, Path: "/" + target.Repo + "/-/" + resource + "/" + strconv.FormatInt(id, 10)}).String()
}
func bindPipeline(source upstreamPipeline, target Target, id int64, ref, sha string) error {
	if source.ID < 1 || (id != 0 && source.ID != id) || source.WebURL != canonicalCIURL(target, "pipelines", source.ID) {
		return uxv1.NewError(uxv1.CodeSafety, "pipeline identity does not match the selected authority")
	}
	if (ref != "" && source.Ref != ref) || (sha != "" && source.SHA != sha) {
		return uxv1.NewError(uxv1.CodeSafety, "pipeline ref or SHA does not match the selector")
	}
	return nil
}
func readPipeline(ctx context.Context, client delegateClient, target Target, id int64, p Parsed) (Pipeline, upstreamPipeline, string, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpPipelineView, Host: target.Host, Repo: target.Repo, ID: id})
	if err != nil {
		return Pipeline{}, upstreamPipeline{}, response.UpstreamVersion, err
	}
	var source upstreamPipeline
	if err := decodeStrict(response.Body, &source); err != nil {
		return Pipeline{}, source, response.UpstreamVersion, err
	}
	if err := bindPipeline(source, target, id, p.Values["--ref"], p.Values["--sha"]); err != nil {
		return Pipeline{}, source, response.UpstreamVersion, err
	}
	pipeline, err := normalizePipeline(source, target.Host, target.Repo)
	return pipeline, source, response.UpstreamVersion, err
}
func fetchSelectedPipelines(ctx context.Context, client delegateClient, target Target, p Parsed) ([]Pipeline, listState, error) {
	filters := pipelineFilters(p)
	seen := map[int64]bool{}
	return fetchList(ctx, client, glab.Request{Operation: glab.OpPipelineList, Host: target.Host, Repo: target.Repo, PipelineFilters: filters}, p.Limit, func(body []byte) ([]Pipeline, bool, error) {
		var source []upstreamPipeline
		if err := decodeStrict(body, &source); err != nil {
			return nil, false, err
		}
		items := make([]Pipeline, 0, len(source))
		for _, raw := range source {
			if err := bindPipeline(raw, target, 0, filters.Ref, filters.SHA); err != nil {
				return nil, false, err
			}
			if seen[raw.ID] {
				return nil, false, malformed("duplicate pipeline")
			}
			seen[raw.ID] = true
			// PipelineInfo does not contain user in the pinned provider. Username is a
			// server-side selector, not invented per-row identity evidence.
			if (filters.Status != "" && raw.Status != filters.Status) || (filters.Source != "" && raw.Source != filters.Source) {
				return nil, false, malformed("pipeline filter response")
			}
			item, err := normalizePipeline(raw, target.Host, target.Repo)
			if err != nil {
				return nil, false, err
			}
			if filters.Status != "" && raw.Status != item.Status {
				item.RawStatus = raw.Status
			}
			for _, field := range strings.Split(p.Values["--fields"], ",") {
				if field == "iid" {
					if raw.IID < 1 {
						return nil, false, malformed("pipeline IID")
					}
					item.IID = raw.IID
				}
			}
			items = append(items, item)
		}
		return items, false, nil
	})
}
func bindJob(raw upstreamJob, target Target, id, pipelineID int64, status string) error {
	if raw.ID < 1 || (id != 0 && raw.ID != id) || raw.WebURL != canonicalCIURL(target, "jobs", raw.ID) {
		return uxv1.NewError(uxv1.CodeSafety, "job identity does not match the selected authority")
	}
	if pipelineID != 0 && (raw.Pipeline == nil || raw.Pipeline.ID != pipelineID) {
		return uxv1.NewError(uxv1.CodeSafety, "job does not belong to the selected pipeline")
	}
	if raw.Pipeline != nil && raw.Pipeline.WebURL != "" {
		if err := bindPipeline(*raw.Pipeline, target, pipelineID, "", ""); err != nil {
			return err
		}
	}
	if status != "" && raw.Status != status {
		return uxv1.NewError(uxv1.CodeConflict, "job does not match the selected GitLab status")
	}
	return nil
}
func bindJobRevision(raw upstreamJob, expected *upstreamPipeline) error {
	if expected != nil && (raw.Pipeline == nil || raw.Pipeline.Ref != expected.Ref || raw.Pipeline.SHA != expected.SHA) {
		return uxv1.NewError(uxv1.CodeSafety, "job pipeline revision does not match the selected pipeline")
	}
	return nil
}

func readSelectedJob(ctx context.Context, client delegateClient, target Target, id, pipelineID int64, status string, expected *upstreamPipeline) (Job, error) {
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpJobView, Host: target.Host, Repo: target.Repo, ID: id})
	if err != nil {
		return Job{}, err
	}
	var raw upstreamJob
	if err := decodeStrict(response.Body, &raw); err != nil {
		return Job{}, err
	}
	if err := bindJob(raw, target, id, pipelineID, status); err != nil {
		return Job{}, err
	}
	if err := bindJobRevision(raw, expected); err != nil {
		return Job{}, err
	}
	items, err := normalizeJobValues([]upstreamJob{raw}, target.Host, target.Repo)
	if err != nil {
		return Job{}, err
	}
	if status != "" && raw.Status != items[0].Status {
		items[0].RawStatus = raw.Status
	}
	return items[0], nil
}
func fetchSelectedJobs(ctx context.Context, client delegateClient, target Target, pipelineID, jobID int64, status string, limit int, expected *upstreamPipeline) ([]Job, listState, error) {
	if jobID != 0 {
		item, err := readSelectedJob(ctx, client, target, jobID, pipelineID, status, expected)
		return []Job{item}, listState{complete: err == nil, count: 1, upstreamVersion: glab.SupportedVersion}, err
	}
	seen := map[int64]bool{}
	return fetchList(ctx, client, glab.Request{Operation: glab.OpJobList, Host: target.Host, Repo: target.Repo, PipelineID: pipelineID, JobStatus: status}, limit, func(body []byte) ([]Job, bool, error) {
		var raw []upstreamJob
		if err := decodeStrict(body, &raw); err != nil {
			return nil, false, err
		}
		for _, item := range raw {
			if err := bindJob(item, target, 0, pipelineID, status); err != nil {
				return nil, false, err
			}
			if err := bindJobRevision(item, expected); err != nil {
				return nil, false, err
			}
			if seen[item.ID] {
				return nil, false, malformed("duplicate job")
			}
			seen[item.ID] = true
		}
		items, err := normalizeJobValues(raw, target.Host, target.Repo)
		if err == nil && status != "" {
			for i := range items {
				if raw[i].Status != items[i].Status {
					items[i].RawStatus = raw[i].Status
				}
			}
		}
		return items, false, err
	})
}

type selectedTrace struct {
	JobID     int64  `json:"job_id"`
	Trace     string `json:"trace"`
	Truncated bool   `json:"truncated"`
}

func executeSelectedPipelineView(ctx context.Context, client delegateClient, target Target, p Parsed, meta uxv1.Meta) (commandOutput, error) {
	id, _ := ciID(p.Positionals[0])
	pipeline, rawPipeline, version, err := readPipeline(ctx, client, target, id, p)
	meta.UpstreamVersion = version
	data := map[string]any{"pipeline": pipeline}
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	jobID, _ := ciID(p.Values["--job-id"])
	status := p.Values["--job-status"]
	if p.Booleans["--trace-failed"] {
		status = "failed"
	}
	if !p.Booleans["--jobs"] && jobID == 0 && status == "" {
		return commandOutput{data: data, meta: meta}, nil
	}
	jobs, state, err := fetchSelectedJobs(ctx, client, target, id, jobID, status, p.Limit, &rawPipeline)
	meta = mergeMeta(meta, state)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	data["jobs"] = jobs
	if p.Booleans["--trace"] || p.Booleans["--trace-failed"] {
		traces := make([]selectedTrace, 0, min(len(jobs), ciMaxTraces))
		for _, job := range jobs[:min(len(jobs), ciMaxTraces)] {
			// Recheck membership and status immediately before the unstructured trace.
			if _, err := readSelectedJob(ctx, client, target, job.ID, id, status, &rawPipeline); err != nil {
				return commandOutput{meta: meta}, err
			}
			response, err := client.Do(ctx, glab.Request{Operation: glab.OpJobTrace, Host: target.Host, Repo: target.Repo, ID: job.ID})
			if err != nil {
				return commandOutput{meta: meta}, err
			}
			trace, cut := redactedTrace(response.Body)
			traces = append(traces, selectedTrace{JobID: job.ID, Trace: trace, Truncated: cut})
			if cut {
				meta.Complete = false
				meta.Truncated = true
				meta.Reason = "trace_tail_limit"
			}
		}
		if len(jobs) > ciMaxTraces {
			meta.Complete = false
			meta.Truncated = true
			meta.Reason = "trace_job_limit"
		}
		data["traces"] = traces
	}
	return commandOutput{data: data, meta: meta}, nil
}

// Injectable waiting and monotonic time keep watch tests deterministic without
// removing the real context deadline that bounds child execution.
type watchClock struct {
	now  func() time.Time
	wait func(context.Context, time.Duration) error
}

func defaultWatchClock() watchClock {
	return watchClock{now: time.Now, wait: func(ctx context.Context, d time.Duration) error {
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		}
	}}
}
func executePipelineWatch(ctx context.Context, client delegateClient, target Target, p Parsed, meta uxv1.Meta, clock watchClock) (commandOutput, error) {
	timeout, interval, _ := watchDurations(p)
	end := clock.now().Add(timeout)
	id, _ := ciID(p.Positionals[0])
	var anchor upstreamPipeline
	polls := 0
	fail := func(reason, message string, code uxv1.Code) (commandOutput, error) {
		meta.Reason = reason
		meta.Complete = false
		return commandOutput{meta: meta}, uxv1.NewError(code, message)
	}
	for {
		if ctx.Err() != nil {
			if ctx.Err() == context.Canceled {
				return fail("watch_canceled", "pipeline watch canceled by caller", uxv1.CodeCanceled)
			}
			return fail("watch_timeout", "pipeline watch timed out before verified success", uxv1.CodeUpstream)
		}
		if !clock.now().Before(end) {
			return fail("watch_timeout", "pipeline watch timed out before verified success", uxv1.CodeUpstream)
		}
		if polls >= ciMaxRequests {
			return fail("watch_request_limit", "pipeline watch request budget exhausted", uxv1.CodeUpstream)
		}
		pipeline, raw, version, err := readPipeline(ctx, client, target, id, p)
		meta.UpstreamVersion = version
		polls++
		if err != nil {
			if ctx.Err() != nil {
				continue
			}
			return commandOutput{meta: meta}, err
		}
		if ctx.Err() != nil {
			continue
		}
		if !clock.now().Before(end) {
			return fail("watch_timeout", "pipeline watch timed out before verified success", uxv1.CodeUpstream)
		}
		if raw.Ref == "" || validSHAOrEmpty(raw.SHA) == "" || raw.UpdatedAt == nil {
			return fail("watch_unknown", "pipeline watch lacks immutable ref, SHA, or timestamp evidence", uxv1.CodeSafety)
		}
		if polls > 1 && (raw.SHA != anchor.SHA || raw.Ref != anchor.Ref || raw.UpdatedAt.Before(*anchor.UpdatedAt)) {
			return fail("watch_stale", "pipeline identity changed or timestamp regressed during watch", uxv1.CodeSafety)
		}
		anchor = raw
		switch raw.Status {
		case "success":
			data := map[string]any{"pipeline": pipeline, "outcome": "success", "polls": polls}
			encoded, _ := json.Marshal(data)
			if len(encoded) > 64<<10 {
				return fail("watch_output_limit", "pipeline watch output budget exhausted", uxv1.CodeUpstream)
			}
			return commandOutput{data: data, meta: meta}, nil
		case "failed", "canceled", "skipped", "manual":
			return fail("watch_"+raw.Status, "pipeline watch observed non-green status: "+raw.Status, uxv1.CodeConflict)
		case "created", "waiting_for_resource", "preparing", "pending", "running", "scheduled":
		default:
			return fail("watch_unknown", "pipeline watch observed an unknown status", uxv1.CodeSafety)
		}
		wait := min(interval, end.Sub(clock.now()))
		if err := clock.wait(ctx, wait); err != nil {
			if ctx.Err() != nil {
				continue
			}
			return fail("watch_wait_failure", "pipeline watch waiting failed", uxv1.CodeUpstream)
		}
	}
}
