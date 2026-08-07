package gitlab

import "time"

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
}

type Project struct {
	ID                int64  `json:"id"`
	PathWithNamespace string `json:"path_with_namespace"`
	WebURL            string `json:"web_url"`
}

type Pipeline struct {
	ID     int64  `json:"id"`
	SHA    string `json:"sha"`
	Status string `json:"status"`
}

type MergeRequest struct {
	IID                 int64     `json:"iid"`
	WebURL              string    `json:"web_url"`
	State               string    `json:"state"`
	Title               string    `json:"title"`
	Description         string    `json:"description"`
	SourceBranch        string    `json:"source_branch"`
	TargetBranch        string    `json:"target_branch"`
	SourceProjectID     int64     `json:"source_project_id"`
	TargetProjectID     int64     `json:"target_project_id"`
	SHA                 string    `json:"sha"`
	HasConflicts        bool      `json:"has_conflicts"`
	DetailedMergeStatus string    `json:"detailed_merge_status"`
	MergeStatus         string    `json:"merge_status"`
	HeadPipeline        *Pipeline `json:"head_pipeline"`
}

type Job struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Stage        string     `json:"stage"`
	Status       string     `json:"status"`
	AllowFailure bool       `json:"allow_failure"`
	FinishedAt   *time.Time `json:"finished_at"`
	Pipeline     *Pipeline  `json:"pipeline,omitempty"`
}

type CreateMergeRequest struct {
	SourceBranch string `json:"source_branch"`
	TargetBranch string `json:"target_branch"`
	Title        string `json:"title"`
	Description  string `json:"description"`
}

type UpdateMergeRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}
