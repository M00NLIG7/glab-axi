// Package presentation defines the minimal fields allowed to cross CLI output
// boundaries.
package presentation

import (
	"gl-axi/internal/app"
	"gl-axi/internal/gitlab"
)

type Pipeline struct {
	ID     int64  `json:"id"`
	SHA    string `json:"sha,omitempty"`
	Status string `json:"status,omitempty"`
}

type MR struct {
	IID                 int64     `json:"iid"`
	WebURL              string    `json:"web_url"`
	State               string    `json:"state,omitempty"`
	SourceBranch        string    `json:"source_branch,omitempty"`
	TargetBranch        string    `json:"target_branch,omitempty"`
	HeadSHA             string    `json:"head_sha,omitempty"`
	SHA                 string    `json:"sha,omitempty"`
	HasConflicts        bool      `json:"has_conflicts"`
	DetailedMergeStatus string    `json:"detailed_merge_status,omitempty"`
	MergeStatus         string    `json:"merge_status,omitempty"`
	HeadPipeline        *Pipeline `json:"head_pipeline,omitempty"`
}

type MRResult struct {
	MR     MR     `json:"mr"`
	Action string `json:"action,omitempty"`
}

type CIResult struct {
	MR   MR                 `json:"mr"`
	Jobs []gitlab.CompatJob `json:"jobs"`
}

func FromMR(mr gitlab.MergeRequest) MR {
	out := MR{
		IID:                 mr.IID,
		WebURL:              mr.WebURL,
		State:               mr.State,
		SourceBranch:        mr.SourceBranch,
		TargetBranch:        mr.TargetBranch,
		HeadSHA:             mr.SHA,
		SHA:                 mr.SHA,
		HasConflicts:        mr.HasConflicts,
		DetailedMergeStatus: mr.DetailedMergeStatus,
		MergeStatus:         mr.MergeStatus,
	}
	if mr.HeadPipeline != nil {
		out.HeadPipeline = &Pipeline{ID: mr.HeadPipeline.ID, SHA: mr.HeadPipeline.SHA, Status: mr.HeadPipeline.Status}
	}
	return out
}

func FromResult(result app.MRResult) MRResult {
	return MRResult{MR: FromMR(result.MR), Action: result.Action}
}

func FromCI(result app.CIResult) CIResult {
	return CIResult{MR: FromMR(result.MR), Jobs: result.Jobs}
}
