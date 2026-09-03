package uxv1

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestAmbiguousMergeUsesConflictExitWithoutSerializingCause(t *testing.T) {
	raw := "ambiguous-merge-provider-sentinel"
	err := Wrap(CodeAmbiguousMerge, "merge outcome is ambiguous", errors.New(raw))
	if ExitCode(err) != 6 {
		t.Fatalf("ambiguous merge exit=%d", ExitCode(err))
	}
	encoded, marshalErr := json.Marshal(Failure(err, Meta{Complete: false}))
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if strings.Contains(string(encoded), raw) || !strings.Contains(string(encoded), `"code":"ambiguous_merge"`) {
		t.Fatalf("ambiguous merge envelope=%s", encoded)
	}
}

func TestSafetyRefusalSerializesOnlyExplicitReceipt(t *testing.T) {
	raw := "provider-controlled-refusal-sentinel"
	err := Wrap(CodeSafety, "mutation refused before provider write", errors.New(raw))
	err.Receipt = struct {
		Action  string `json:"action"`
		Outcome string `json:"outcome"`
	}{Action: "refused", Outcome: "not_applied"}
	encoded, marshalErr := json.Marshal(Failure(err, Meta{Complete: false}))
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	if ExitCode(err) != 9 || strings.Contains(string(encoded), raw) || !strings.Contains(string(encoded), `"receipt":{"action":"refused","outcome":"not_applied"}`) {
		t.Fatalf("safety refusal envelope=%s exit=%d", encoded, ExitCode(err))
	}
}

func TestHTTPRejectionsAreBoundedAndKeepControlMetadataPrivate(t *testing.T) {
	for _, test := range []struct {
		status    int
		wantCode  Code
		retryable bool
	}{
		{status: 400, wantCode: CodeValidation},
		{status: 401, wantCode: CodeAuthentication},
		{status: 403, wantCode: CodeForbidden},
		{status: 404, wantCode: CodeNotFound},
		{status: 409, wantCode: CodeConflict},
		{status: 422, wantCode: CodeValidation},
		{status: 429, wantCode: CodeRateLimited, retryable: true},
	} {
		rejection, ok := NewHTTPRejection(test.status)
		if !ok || rejection.Code != test.wantCode || rejection.StatusCode != test.status || rejection.Retryable != test.retryable {
			t.Fatalf("status %d rejection=%#v ok=%t", test.status, rejection, ok)
		}
		rawProviderText := "provider-controlled-error-sentinel"
		rejection.Cause = errors.New(rawProviderText)
		encoded, err := json.Marshal(rejection)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), rawProviderText) || strings.Contains(string(encoded), "status_code") || strings.Contains(string(encoded), "StatusCode") {
			t.Fatalf("status %d serialized private metadata: %s", test.status, encoded)
		}
	}
	if rejection, ok := NewHTTPRejection(500); ok || rejection != nil {
		t.Fatalf("uncertain status became a definite rejection: %#v", rejection)
	}
}
