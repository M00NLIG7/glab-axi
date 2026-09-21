package glab

import (
	"strconv"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
)

const (
	OpIssueWriteProject Operation = "issue-write-project"
	OpIssueWriteView    Operation = "issue-write-view"
	OpIssueCreate       Operation = "issue-create"
	OpIssueNoteCreate   Operation = "issue-note-create"
	OpIssueState        Operation = "issue-state"
)

func buildIssueWrite(request Request) (invocation, error) {
	if request.ProjectID < 1 {
		return invocation{}, uxv1.NewError(uxv1.CodeValidation, "issue write requires a positive numeric project ID")
	}
	endpoint := "projects/" + strconv.FormatInt(request.ProjectID, 10) + "/issues"
	method, write := "GET", false
	if request.Operation != OpIssueCreate {
		if request.IID < 1 {
			return invocation{}, uxv1.NewError(uxv1.CodeValidation, "issue IID must be a positive integer")
		}
		endpoint += "/" + strconv.FormatInt(request.IID, 10)
	}
	switch request.Operation {
	case OpIssueWriteView:
	case OpIssueCreate:
		method, write = "POST", true
	case OpIssueNoteCreate:
		method, write = "POST", true
		endpoint += "/notes"
	case OpIssueState:
		method, write = "PUT", true
	default:
		return invocation{}, uxv1.NewError(uxv1.CodeUnsupported, "undeclared issue write operation")
	}
	args := []string{"api", "--method", method, "--hostname", request.Host, endpoint}
	if write {
		if err := validatePrivateInputPath(request.InputFile); err != nil {
			return invocation{}, err
		}
		args = append(args, "--input", request.InputFile, "--header", "Content-Type: application/json")
	}
	return invocation{args: args, host: request.Host, maxStdout: limits.MaxJSONPageBytes, write: write, outputKind: outputJSON}, nil
}
