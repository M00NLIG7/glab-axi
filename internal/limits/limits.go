// Package limits centralizes all externally observable size and time bounds.
package limits

import "time"

const (
	MaxHostBytes                 = 253
	MaxProjectBytes              = 1024
	MaxProjectSegments           = 32
	MaxBranchBytes               = 1024
	MaxTitleBytes                = 1024
	MaxDescriptionBytes          = 128 << 10
	MaxTokenBytes                = 16 << 10
	MaxJSONPageBytes             = 2 << 20
	MaxOperationBytes            = 8 << 20
	MaxErrorReadBytes            = 64 << 10
	MaxErrorOutputBytes          = 2 << 10
	MaxStderrBytes               = 4 << 10
	MaxTraceBytes                = 256 << 10
	MaxPages                     = 10
	MaxJobs                      = 1000
	MaxDiscussionNotes           = 1000
	MaxDiscussionBodiesBytes     = 2 << 20
	MaxDiscussionIdentifierBytes = 256
	MaxDiscussionUserFieldBytes  = 256
	MaxDiscussionPathBytes       = 1024
	MaxDiscussionLineCodeBytes   = 256
)

const (
	ShortOperation           = 30 * time.Second
	WriteOperation           = 45 * time.Second
	EnsureReconcileOperation = 10 * time.Second
	MergePreflightOperation  = 20 * time.Second
	MergeMutationOperation   = 15 * time.Second
	MergeReconcileOperation  = 10 * time.Second
	ConnectTimeout           = 5 * time.Second
	TLSHandshake             = 5 * time.Second
	HeaderTimeout            = 10 * time.Second
	MaxRetryAfter            = 30 * time.Second
)
