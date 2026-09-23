package product

import (
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

const (
	ciMaxRequests        = 100
	ciMaxOutputBytes     = 2 << 20
	ciMaxTraces          = 5
	watchDefaultTimeout  = 30 * time.Second
	watchDefaultInterval = 3 * time.Second
)

func ciWebBaseFlag() FlagDefinition {
	return FlagDefinition{Name: "--web-base", Value: "URL", Description: "Trusted HTTPS web base on the selected hostname, including an optional installation path (default: host root). Does not change official glab credentials or API routing."}
}

func ciWebBasePath(raw, host string) (string, error) {
	invalid := func() error {
		return uxv1.NewError(uxv1.CodeValidation, "web base must be a canonical HTTPS URL on the selected hostname with a safe installation path")
	}
	base, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || !strings.EqualFold(base.Host, host) {
		return "", invalid()
	}
	if raw != (&url.URL{Scheme: "https", Host: base.Host, Path: base.Path}).String() ||
		!utf8.ValidString(base.Path) || strings.ContainsAny(base.Path, "\\%") || strings.Contains(base.Path, "//") ||
		strings.ContainsFunc(base.Path, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return "", invalid()
	}
	basePath := strings.TrimSuffix(base.Path, "/")
	if basePath != "" && path.Clean(basePath) != basePath {
		return "", invalid()
	}
	return basePath, nil
}

func pipelineListFlags() []FlagDefinition {
	return []FlagDefinition{
		ciWebBaseFlag(),
		{Name: "--ref", Value: "REF", Description: "Exact Git ref."},
		{Name: "--status", Value: "STATUS", Description: "GitLab status: created, waiting_for_resource, preparing, pending, running, success, failed, canceled, skipped, manual, scheduled."},
		{Name: "--source", Value: "SOURCE", Description: "GitLab pipeline source (not a workflow): api, chat, external, external_pull_request_event, merge_request_event, ondemand_dast_scan, ondemand_dast_validation, parent_pipeline, pipeline, push, schedule, security_orchestration_policy, trigger, web, webide."},
		{Name: "--user", Value: "USERNAME", Description: "Triggering GitLab username."},
		{Name: "--sha", Value: "SHA", Description: "Exact lowercase 40- or 64-hex commit SHA."},
		{Name: "--fields", Value: "FIELDS", Description: "Additive field: iid. Existing default fields stay present."},
	}
}

func pipelineIdentityFlags() []FlagDefinition {
	return []FlagDefinition{
		ciWebBaseFlag(),
		{Name: "--ref", Value: "REF", Description: "Require this exact pipeline ref."},
		{Name: "--sha", Value: "SHA", Description: "Require this exact lowercase 40- or 64-hex commit SHA."},
	}
}
func pipelineViewFlags() []FlagDefinition {
	return append(pipelineIdentityFlags(),
		FlagDefinition{Name: "--jobs", Boolean: true, Description: "Include bounded pipeline jobs; these are not workflow steps or merge-check proof."},
		FlagDefinition{Name: "--job-id", Value: "ID", Description: "Select one job belonging to this pipeline; implies --jobs."},
		FlagDefinition{Name: "--job-status", Value: "STATUS", Description: "Select jobs by exact GitLab status; implies --jobs."},
		FlagDefinition{Name: "--trace", Boolean: true, Description: "Include the bounded redacted tail for --job-id."},
		FlagDefinition{Name: "--trace-failed", Boolean: true, Description: "Select failed jobs and at most five bounded redacted trace tails; no temporary full log."},
	)
}
func pipelineWatchFlags() []FlagDefinition {
	return append(pipelineIdentityFlags(),
		FlagDefinition{Name: "--timeout", Value: "SECONDS", Description: "Finite duration 1..300 seconds (default 30)."},
		FlagDefinition{Name: "--interval", Value: "SECONDS", Description: "Polling interval 1..30 seconds, not above timeout (default 3)."},
	)
}
func jobListFlags() []FlagDefinition {
	return []FlagDefinition{
		ciWebBaseFlag(),
		{Name: "--pipeline-id", Value: "ID", Description: "Exact pipeline ID.", Required: true},
		{Name: "--job-id", Value: "ID", Description: "Select one job belonging to this pipeline."},
		{Name: "--status", Value: "STATUS", Description: "Exact GitLab job status, including failed or manual."},
	}
}
func jobIdentityFlags() []FlagDefinition {
	return []FlagDefinition{ciWebBaseFlag(), {Name: "--pipeline-id", Value: "ID", Description: "Require membership in this exact pipeline before reading."}}
}

func pipelineFilters(parsed Parsed) glab.PipelineFilters {
	return glab.PipelineFilters{Ref: parsed.Values["--ref"], Status: parsed.Values["--status"], Source: parsed.Values["--source"], User: parsed.Values["--user"], SHA: parsed.Values["--sha"]}
}

func ciID(raw string) (int64, error) {
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 1 || strconv.FormatInt(n, 10) != raw {
		return 0, uxv1.NewError(uxv1.CodeValidation, "CI resource ID must be a canonical positive integer")
	}
	return n, nil
}

func validateCIReadParsed(p Parsed) error {
	path := strings.Join(p.Definition.Path, " ")
	if !strings.HasPrefix(path, "pipeline ") && !strings.HasPrefix(path, "job ") {
		return nil
	}
	if err := glab.ValidatePipelineFilters(pipelineFilters(p)); err != nil {
		return err
	}
	for _, key := range []string{"--pipeline-id", "--job-id"} {
		if raw := p.Values[key]; raw != "" {
			if _, err := ciID(raw); err != nil {
				return err
			}
		}
	}
	if len(p.Positionals) != 0 {
		if _, err := ciID(p.Positionals[0]); err != nil {
			return err
		}
	}
	for _, key := range []string{"--status", "--job-status"} {
		if value := p.Values[key]; value != "" && !glab.ValidCIStatus(value) {
			return uxv1.NewError(uxv1.CodeValidation, "invalid GitLab CI status")
		}
	}
	if fields := p.Values["--fields"]; fields != "" && fields != "iid" {
		return uxv1.NewError(uxv1.CodeValidation, "unsupported pipeline field; only iid is selectable")
	}
	if p.Booleans["--trace"] && (p.Values["--job-id"] == "" || p.Booleans["--trace-failed"]) {
		return uxv1.NewError(uxv1.CodeValidation, "--trace requires --job-id and cannot combine with --trace-failed")
	}
	if p.Booleans["--trace-failed"] && p.Values["--job-status"] != "" {
		return uxv1.NewError(uxv1.CodeValidation, "--trace-failed cannot combine with --job-status")
	}
	if path == "pipeline watch" {
		_, _, err := watchDurations(p)
		return err
	}
	return nil
}

func watchDurations(p Parsed) (time.Duration, time.Duration, error) {
	timeout, interval := watchDefaultTimeout, watchDefaultInterval
	for _, spec := range []struct {
		key string
		max int
		dst *time.Duration
	}{{"--timeout", 300, &timeout}, {"--interval", 30, &interval}} {
		if raw := p.Values[spec.key]; raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n < 1 || n > spec.max || strconv.Itoa(n) != raw {
				return 0, 0, uxv1.NewError(uxv1.CodeValidation, "watch duration or interval is outside its finite range")
			}
			*spec.dst = time.Duration(n) * time.Second
		}
	}
	if interval > timeout {
		return 0, 0, uxv1.NewError(uxv1.CodeValidation, "watch interval must not exceed timeout")
	}
	return timeout, interval, nil
}
