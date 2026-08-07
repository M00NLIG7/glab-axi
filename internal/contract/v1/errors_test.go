package v1

import "testing"

func TestDeterministicExitCodes(t *testing.T) {
	cases := []struct {
		err  error
		exit int
	}{
		{NewError(CodeValidation, "x"), 2},
		{NewError(CodeAuthentication, "x"), 3},
		{NewError(CodeForbidden, "x"), 4},
		{NewError(CodeNotFound, "x"), 5},
		{NewError(CodeConflict, "x"), 6},
		{NewError(CodeRateLimited, "x"), 7},
		{NewError(CodeUpstream, "x"), 8},
		{NewError(CodeSafety, "x"), 9},
		{NewError(CodeCanceled, "x"), 130},
	}
	for _, test := range cases {
		if got := ExitCode(test.err); got != test.exit {
			t.Fatalf("code=%s exit=%d, want %d", AsError(test.err).Code, got, test.exit)
		}
	}
}

func TestHTTPStatusMappingDoesNotExposeBody(t *testing.T) {
	cases := map[int]int{401: 3, 403: 4, 404: 5, 409: 6, 429: 7, 500: 8}
	for status, exit := range cases {
		err := HTTPError(status, false)
		if got := ExitCode(err); got != exit || err.Message == "" {
			t.Fatalf("status=%d exit=%d, want %d", status, got, exit)
		}
	}
}
