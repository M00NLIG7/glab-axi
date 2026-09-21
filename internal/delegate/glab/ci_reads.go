package glab

import (
	"encoding/hex"
	"regexp"
	"strings"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/safeurl"
)

// PipelineFilters is a closed subset of the pinned ci list provider contract.
// It never grants arbitrary query or argv authority.
type PipelineFilters struct {
	Ref    string
	Status string
	Source string
	User   string
	SHA    string
}

var ciUsername = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]{0,254}$`)

func ValidCIStatus(value string) bool {
	switch value {
	case "created", "waiting_for_resource", "preparing", "pending", "running", "success", "failed", "canceled", "skipped", "manual", "scheduled":
		return true
	}
	return false
}

func ValidatePipelineFilters(f PipelineFilters) error {
	invalid := func() error { return uxv1.NewError(uxv1.CodeValidation, "invalid pipeline selector") }
	if f.Ref != "" {
		if err := safeurl.ValidateBranch(f.Ref); err != nil {
			return invalid()
		}
	}
	if f.Status != "" && !ValidCIStatus(f.Status) {
		return invalid()
	}
	if f.Source != "" {
		switch f.Source {
		case "api", "chat", "external", "external_pull_request_event", "merge_request_event", "ondemand_dast_scan", "ondemand_dast_validation", "parent_pipeline", "pipeline", "push", "schedule", "security_orchestration_policy", "trigger", "web", "webide":
		default:
			return invalid()
		}
	}
	if f.User != "" && !ciUsername.MatchString(f.User) {
		return invalid()
	}
	if f.SHA != "" {
		if (len(f.SHA) != 40 && len(f.SHA) != 64) || strings.ToLower(f.SHA) != f.SHA {
			return invalid()
		}
		if _, err := hex.DecodeString(f.SHA); err != nil {
			return invalid()
		}
	}
	return nil
}

func (f PipelineFilters) args() []string {
	var args []string
	for _, pair := range [][2]string{{"--ref", f.Ref}, {"--status", f.Status}, {"--source", f.Source}, {"--username", f.User}, {"--sha", f.SHA}} {
		if pair[1] != "" {
			args = append(args, pair[0], pair[1])
		}
	}
	return args
}
