package gitlab

import (
	"encoding/hex"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"glab-axi/internal/contract/v1"
)

type CompatJob struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Stage      string     `json:"stage"`
	Status     string     `json:"status"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
	RawStatus  string     `json:"raw_status,omitempty"`
}

func NormalizeJobs(jobs []Job) ([]CompatJob, error) {
	out := make([]CompatJob, 0, len(jobs))
	for _, job := range jobs {
		if job.ID < 1 || strings.TrimSpace(job.Name) == "" || len(job.Name) > 1024 || len(job.Stage) > 1024 || len(job.Status) > 128 || !utf8.ValidString(job.Name) || !utf8.ValidString(job.Stage) || !utf8.ValidString(job.Status) || hasControl(job.Name) || hasControl(job.Stage) || hasControl(job.Status) {
			return nil, v1.NewError(v1.CodeUpstream, "GitLab returned invalid job metadata")
		}
		status, known := normalizeJobStatus(job.Status, job.AllowFailure)
		item := CompatJob{ID: job.ID, Name: job.Name, Stage: job.Stage, Status: status, FinishedAt: job.FinishedAt}
		if !known {
			item.RawStatus = boundedStatus(job.Status)
		}
		out = append(out, item)
	}
	return out, nil
}

func normalizeJobStatus(raw string, allowFailure bool) (string, bool) {
	switch strings.ToLower(raw) {
	case "success":
		return "success", true
	case "failed":
		if allowFailure {
			return "skipped", true
		}
		return "failed", true
	case "manual":
		if allowFailure {
			return "skipped", true
		}
		return "running", true
	case "skipped":
		return "skipped", true
	case "canceled":
		return "canceled", true
	case "pending", "running", "created", "preparing", "scheduled", "waiting_for_resource", "waiting_for_callback", "canceling":
		return "running", true
	default:
		return "running", false
	}
}

func NormalizeMergeStatus(raw string, conflicts bool) string {
	if conflicts {
		return "cannot_be_merged"
	}
	switch strings.ToLower(raw) {
	case "mergeable", "can_be_merged":
		return "can_be_merged"
	case "cannot_be_merged", "conflict":
		return "cannot_be_merged"
	case "checking", "unchecked", "preparing", "ci_must_pass", "ci_still_running", "approvals_syncing", "requested_changes", "not_open", "draft", "discussions_not_resolved", "need_rebase", "not_approved", "blocked_status":
		return "checking"
	default:
		return "checking"
	}
}

func ValidSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func boundedStatus(raw string) string {
	if len(raw) > 128 {
		return raw[:128]
	}
	return raw
}

func hasControl(value string) bool {
	return strings.ContainsFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
}
