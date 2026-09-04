// Package uxv1 defines the product-facing, backend-explicit output contract.
// It is separate from the frozen glab-axi/v1 automation contract.
package uxv1

const Schema = "glab-axi/ux-v1"

type Envelope struct {
	Schema string   `json:"schema"`
	OK     bool     `json:"ok"`
	Data   any      `json:"data,omitempty"`
	Error  *Error   `json:"error,omitempty"`
	Help   []string `json:"help,omitempty"`
	Meta   Meta     `json:"meta"`
}

type Meta struct {
	Backend         string `json:"backend,omitempty"`
	Host            string `json:"host,omitempty"`
	Repo            string `json:"repo,omitempty"`
	Complete        bool   `json:"complete"`
	Truncated       bool   `json:"truncated"`
	Count           int    `json:"count,omitempty"`
	Limit           int    `json:"limit,omitempty"`
	Reason          string `json:"reason,omitempty"`
	UpstreamVersion string `json:"upstream_version,omitempty"`
}

func Success(data any, meta Meta) Envelope {
	return Envelope{Schema: Schema, OK: true, Data: data, Meta: meta}
}

func Failure(err error, meta Meta) Envelope {
	typed := AsError(err)
	return Envelope{Schema: Schema, OK: false, Error: typed, Help: helpFor(typed.Code), Meta: meta}
}

func helpFor(code Code) []string {
	switch code {
	case CodeValidation, CodeUnsupported:
		return []string{"run gl-axi --help or the command's --help page"}
	case CodeSecurityBoundary:
		return []string{"use official glab directly as a human if this operation is authorized"}
	case CodeInteractiveRequired:
		return []string{"ask a human to run gl-axi auth login in a real terminal"}
	case CodeDependencyMissing:
		return []string{"install official glab 1.112.0 through an official GitLab CLI channel"}
	case CodeDependencyUnsupported:
		return []string{"install the pinned official glab 1.112.0 release"}
	case CodeAuthentication:
		return []string{"ask a human to run gl-axi auth login, or use an approved GitLab token environment variable"}
	case CodeForbidden:
		return []string{"verify the official-glab profile's project role without broadening gl-axi"}
	case CodeConflict:
		return []string{"refresh and inspect the exact selected GitLab resource before retrying"}
	case CodeAmbiguousCreate, CodeAmbiguousUpdate:
		return []string{"inspect exact matching merge requests before retrying"}
	case CodeAmbiguousMerge:
		return []string{"inspect the exact merge request URL and expected head before any retry"}
	case CodeRateLimited:
		return []string{"retry after the provider rate limit resets"}
	case CodeSafety:
		return []string{"verify the reported local or provider authority safety condition"}
	default:
		return nil
	}
}
