package v1

const Schema = "glab-axi/v1"

type Envelope struct {
	Schema string   `json:"schema"`
	OK     bool     `json:"ok"`
	Data   any      `json:"data,omitempty"`
	Error  *Error   `json:"error,omitempty"`
	Help   []string `json:"help,omitempty"`
	Meta   Meta     `json:"meta"`
}

type Meta struct {
	Truncated bool `json:"truncated"`
}

func Success(data any, truncated bool) Envelope {
	return Envelope{Schema: Schema, OK: true, Data: data, Meta: Meta{Truncated: truncated}}
}

func Failure(err error) Envelope {
	typed := AsError(err)
	return Envelope{Schema: Schema, OK: false, Error: typed, Help: helpFor(typed.Code), Meta: Meta{}}
}

func helpFor(code Code) []string {
	switch code {
	case CodeValidation, CodeUnsupported:
		return []string{"run glab-axi --help and use only the declared command grammar"}
	case CodeAuthentication:
		return []string{"configure one noninteractive token source for the selected host"}
	case CodeForbidden:
		return []string{"verify project role and token scope without broadening the client command"}
	case CodeConflict, CodeAmbiguousCreate, CodeAmbiguousUpdate:
		return []string{"inspect exact matching merge requests before retrying"}
	case CodeRateLimited:
		return []string{"retry after the provider rate limit resets"}
	case CodeSafety:
		return []string{"verify host, API base, web base, and repository identity"}
	default:
		return nil
	}
}
