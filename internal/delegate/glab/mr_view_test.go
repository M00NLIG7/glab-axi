package glab

import (
	"encoding/json"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
)

const (
	mrViewTestHead = "0123456789012345678901234567890123456789"
	mrViewTestBase = "1123456789012345678901234567890123456789"
)

func TestNormalizeMRViewResponseAcceptsOfficialAndRESTHeadShapes(t *testing.T) {
	for _, test := range []struct {
		name string
		body string
	}{
		{
			name: "official client diff refs",
			body: `{"id":1011,"iid":11,"project_id":101,"source_project_id":101,"target_project_id":101,"diff_refs":{"base_sha":"` + mrViewTestBase + `","head_sha":"` + mrViewTestHead + `","start_sha":"2123456789012345678901234567890123456789"}}`,
		},
		{
			name: "prior REST response",
			body: `{"id":1011,"iid":11,"source_project_id":101,"target_project_id":101,"sha":"` + mrViewTestHead + `","base_sha":"` + mrViewTestBase + `"}`,
		},
		{
			name: "matching dual proof",
			body: `{"id":1011,"iid":11,"project_id":101,"source_project_id":101,"target_project_id":101,"sha":"` + mrViewTestHead + `","base_sha":"` + mrViewTestBase + `","diff_refs":{"base_sha":"` + mrViewTestBase + `","head_sha":"` + mrViewTestHead + `"}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := normalizeMRViewResponse([]byte(test.body))
			if err != nil {
				t.Fatal(err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(normalized, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded["sha"] != mrViewTestHead || decoded["base_sha"] != mrViewTestBase || decoded["id"] != float64(1011) || decoded["iid"] != float64(11) {
				t.Fatalf("normalized=%s", normalized)
			}
		})
	}
}

func TestNormalizeMRViewResponseRejectsUnprovableIdentity(t *testing.T) {
	otherHead := "4123456789012345678901234567890123456789"
	for _, test := range []struct {
		name string
		body string
		code uxv1.Code
	}{
		{name: "head mismatch", body: `{"sha":"` + mrViewTestHead + `","diff_refs":{"head_sha":"` + otherHead + `"}}`, code: uxv1.CodeSafety},
		{name: "base mismatch", body: `{"base_sha":"` + mrViewTestBase + `","diff_refs":{"base_sha":"` + otherHead + `"}}`, code: uxv1.CodeSafety},
		{name: "invalid REST base", body: `{"base_sha":"not-a-sha"}`, code: uxv1.CodeUpstream},
		{name: "invalid diff base", body: `{"diff_refs":{"base_sha":"short"}}`, code: uxv1.CodeUpstream},
		{name: "invalid diff start", body: `{"diff_refs":{"start_sha":"short"}}`, code: uxv1.CodeUpstream},
		{name: "invalid REST head", body: `{"sha":"not-a-sha","diff_refs":{"head_sha":"` + mrViewTestHead + `"}}`, code: uxv1.CodeUpstream},
		{name: "zero REST head", body: `{"sha":"0000000000000000000000000000000000000000"}`, code: uxv1.CodeUpstream},
		{name: "uppercase official head", body: `{"diff_refs":{"head_sha":"ABCDEF0123456789ABCDEF0123456789ABCDEF01"}}`, code: uxv1.CodeUpstream},
		{name: "invalid official head", body: `{"sha":"` + mrViewTestHead + `","diff_refs":{"head_sha":"short"}}`, code: uxv1.CodeUpstream},
		{name: "malformed diff refs", body: `{"sha":"` + mrViewTestHead + `","diff_refs":[]}`, code: uxv1.CodeUpstream},
		{name: "non-string diff head", body: `{"diff_refs":{"head_sha":42}}`, code: uxv1.CodeUpstream},
		{name: "duplicate top head", body: `{"sha":"` + mrViewTestHead + `","sha":"` + mrViewTestHead + `"}`, code: uxv1.CodeUpstream},
		{name: "duplicate diff head", body: `{"diff_refs":{"head_sha":"` + mrViewTestHead + `","head_sha":"` + mrViewTestHead + `"}}`, code: uxv1.CodeUpstream},
		{name: "project mismatch", body: `{"project_id":101,"target_project_id":102,"sha":"` + mrViewTestHead + `"}`, code: uxv1.CodeSafety},
		{name: "malformed project", body: `{"project_id":"101","target_project_id":101,"sha":"` + mrViewTestHead + `"}`, code: uxv1.CodeUpstream},
		{name: "fractional project", body: `{"project_id":101.5,"target_project_id":101,"sha":"` + mrViewTestHead + `"}`, code: uxv1.CodeUpstream},
		{name: "malformed source project", body: `{"source_project_id":"101","sha":"` + mrViewTestHead + `"}`, code: uxv1.CodeUpstream},
		{name: "trailing payload", body: `{"sha":"` + mrViewTestHead + `"}{}`, code: uxv1.CodeUpstream},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := normalizeMRViewResponse([]byte(test.body))
			if err == nil || uxv1.AsError(err).Code != test.code {
				t.Fatalf("error=%v", err)
			}
			if strings.Contains(err.Error(), mrViewTestHead) || strings.Contains(err.Error(), mrViewTestBase) || strings.Contains(err.Error(), otherHead) {
				t.Fatalf("identity leaked through error: %v", err)
			}
		})
	}
}

func TestNormalizeMRViewResponseNeverInventsMissingHead(t *testing.T) {
	normalized, err := normalizeMRViewResponse([]byte(`{"id":1011,"iid":11,"project_id":101,"source_project_id":101,"target_project_id":101,"diff_refs":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(normalized, &decoded); err != nil {
		t.Fatal(err)
	}
	if _, exists := decoded["sha"]; exists {
		t.Fatalf("missing head was invented: %s", normalized)
	}
}
