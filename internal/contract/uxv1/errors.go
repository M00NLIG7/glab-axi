package uxv1

import (
	"errors"

	v1 "glab-axi/internal/contract/v1"
)

type Code string

const (
	CodeValidation            Code = "validation_error"
	CodeUnsupported           Code = "unsupported"
	CodeSecurityBoundary      Code = "security_boundary"
	CodeInteractiveRequired   Code = "interactive_required"
	CodeDependencyMissing     Code = "dependency_missing"
	CodeDependencyUnsupported Code = "dependency_unsupported"
	CodeAuthentication        Code = "authentication_error"
	CodeForbidden             Code = "forbidden"
	CodeNotFound              Code = "not_found"
	CodeConflict              Code = "conflict"
	CodeRateLimited           Code = "rate_limited"
	CodeUpstream              Code = "upstream_error"
	CodeSafety                Code = "safety_violation"
	CodeCanceled              Code = "canceled"
	CodeInternal              Code = "internal_error"
	CodeAmbiguousCreate       Code = "ambiguous_create"
	CodeAmbiguousUpdate       Code = "ambiguous_update"
)

// Error is the stable product failure. Cause is retained only for control flow
// and is never serialized.
type Error struct {
	Code      Code   `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
	Cause     error  `json:"-"`
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
	var native *v1.Error
	if errors.As(err, &native) {
		return &Error{Code: fromNative(native.Code), Message: native.Message, Retryable: native.Retryable, Cause: err}
	}
	return &Error{Code: CodeInternal, Message: "internal error", Cause: err}
}

func fromNative(code v1.Code) Code {
	switch code {
	case v1.CodeValidation:
		return CodeValidation
	case v1.CodeUnsupported:
		return CodeUnsupported
	case v1.CodeAuthentication:
		return CodeAuthentication
	case v1.CodeForbidden:
		return CodeForbidden
	case v1.CodeNotFound:
		return CodeNotFound
	case v1.CodeConflict:
		return CodeConflict
	case v1.CodeRateLimited:
		return CodeRateLimited
	case v1.CodeSafety:
		return CodeSafety
	case v1.CodeCanceled:
		return CodeCanceled
	case v1.CodeAmbiguousCreate:
		return CodeAmbiguousCreate
	case v1.CodeAmbiguousUpdate:
		return CodeAmbiguousUpdate
	case v1.CodeUpstream:
		return CodeUpstream
	default:
		return CodeInternal
	}
}

func ExitCode(err error) int {
	switch AsError(err).Code {
	case CodeValidation, CodeUnsupported, CodeSecurityBoundary:
		return 2
	case CodeInteractiveRequired, CodeAuthentication:
		return 3
	case CodeForbidden:
		return 4
	case CodeNotFound:
		return 5
	case CodeConflict, CodeAmbiguousCreate, CodeAmbiguousUpdate:
		return 6
	case CodeRateLimited:
		return 7
	case CodeDependencyMissing, CodeDependencyUnsupported, CodeUpstream, CodeInternal:
		return 8
	case CodeSafety:
		return 9
	case CodeCanceled:
		return 130
	default:
		return 8
	}
}
