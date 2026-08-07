package v1

import (
	"errors"
	"fmt"
)

type Code string

const (
	CodeValidation      Code = "validation_error"
	CodeAuthentication  Code = "authentication_error"
	CodeForbidden       Code = "forbidden"
	CodeNotFound        Code = "not_found"
	CodeConflict        Code = "conflict"
	CodeRateLimited     Code = "rate_limited"
	CodeUpstream        Code = "upstream_error"
	CodeSafety          Code = "safety_violation"
	CodeCanceled        Code = "canceled"
	CodeInternal        Code = "internal_error"
	CodeUnsupported     Code = "unsupported"
	CodeAmbiguousCreate Code = "ambiguous_create"
	CodeAmbiguousUpdate Code = "ambiguous_update"
)

// Error is the stable machine-readable failure type. Cause is never serialized.
type Error struct {
	Code       Code   `json:"code"`
	Message    string `json:"message"`
	Retryable  bool   `json:"retryable"`
	StatusCode int    `json:"-"`
	Ambiguous  bool   `json:"-"`
	Cause      error  `json:"-"`
}

func (e *Error) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Cause }

func NewError(code Code, message string) *Error {
	return &Error{Code: code, Message: message}
}

func Wrap(code Code, message string, cause error) *Error {
	return &Error{Code: code, Message: message, Cause: cause}
}

func AsError(err error) *Error {
	if err == nil {
		return nil
	}
	var typed *Error
	if errors.As(err, &typed) {
		return typed
	}
	return &Error{Code: CodeInternal, Message: "internal error", Cause: err}
}

func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	e := AsError(err)
	switch e.Code {
	case CodeValidation, CodeUnsupported:
		return 2
	case CodeAuthentication:
		return 3
	case CodeForbidden:
		return 4
	case CodeNotFound:
		return 5
	case CodeConflict, CodeAmbiguousCreate, CodeAmbiguousUpdate:
		return 6
	case CodeRateLimited:
		return 7
	case CodeUpstream, CodeInternal:
		return 8
	case CodeSafety:
		return 9
	case CodeCanceled:
		return 130
	default:
		return 8
	}
}

func HTTPError(status int, retryable bool) *Error {
	var code Code
	var message string
	switch status {
	case 401:
		code, message = CodeAuthentication, "GitLab authentication failed"
	case 403:
		code, message = CodeForbidden, "GitLab denied this operation"
	case 404:
		code, message = CodeNotFound, "GitLab resource not found"
	case 409:
		code, message = CodeConflict, "GitLab reported a conflict"
	case 429:
		code, message = CodeRateLimited, "GitLab rate limit reached"
	default:
		code, message = CodeUpstream, fmt.Sprintf("GitLab returned HTTP %d", status)
	}
	return &Error{Code: code, Message: message, Retryable: retryable, StatusCode: status}
}
