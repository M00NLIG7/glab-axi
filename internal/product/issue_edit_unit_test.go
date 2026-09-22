package product

import (
	"context"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/output"
)

// Runs only the feature state machine with a synthetic typed provider. Real
// selector/auth/HTTP behavior is covered separately through Run and compiled
// native TLS E2Es. This never enables an official-glab write route.
func runIssueEditStateMachine(ctx context.Context, args []string, deps Dependencies) int {
	if deps.NewDelegate == nil {
		panic("issue-edit state-machine tests require a synthetic provider")
	}
	args = appendCopy(args, "--auth-source", "native")
	parsedResult, err := Parse(args)
	meta := uxv1.Meta{Backend: "native", Host: "gitlab.com", Repo: "group/project", Complete: true}
	if err != nil {
		return writeFailure(deps.Runtime.Stdout, deps.Runtime.Stderr, "gl-axi", productFormatHint(args), err, meta)
	}
	parsed := *parsedResult.Command
	requested, err := loadIssueEditRequested(parsed)
	result := commandOutput{meta: meta}
	if err == nil {
		target := issueEditTarget{Target: Target{Host: "gitlab.com", Repo: "group/project"}, projectURL: "https://gitlab.com/group/project", issueURL: issueEditTestURL}
		result, err = executeIssueEditWithClient(ctx, deps.NewDelegate(), target, parsed, requested, meta)
	}
	if err != nil {
		result.meta.Complete = false
		return writeFailure(deps.Runtime.Stdout, deps.Runtime.Stderr, "gl-axi", parsed.Format, err, result.meta)
	}
	if err := output.WriteValue(deps.Runtime.Stdout, parsed.Format, uxv1.Success(result.data, result.meta)); err != nil {
		panic(err)
	}
	return 0
}
