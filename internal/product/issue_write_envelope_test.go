package product

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"

	"gl-axi/internal/contract/uxv1"
)

func TestIssueWriteErrorEnvelopeSchema(t *testing.T) {
	compiler := jsonschema.NewCompiler()
	compiler.LoadURL = func(url string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("unexpected external schema: %s", url)
	}
	const schemaBase = "https://glab-axi.invalid/schema/"
	for _, name := range []string{
		"glab-axi-ux-v1.schema.json",
		"ux-v1/issue-write.schema.json",
		"ux-v1/issue-edit.schema.json",
		"ux-v1/board-ordering-receipt.schema.json",
		"ux-v1/ci-variable-mutation.schema.json",
		"ux-v1/resource-delete.schema.json",
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "schema", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource(schemaBase+name, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := compiler.Compile(schemaBase + "glab-axi-ux-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	validateEmitted := func(t *testing.T, body []byte) map[string]any {
		t.Helper()
		var envelope map[string]any
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if err := schema.Validate(envelope); err != nil {
			t.Errorf("emitted envelope violates published schema: %v", err)
		}
		return envelope
	}

	for _, action := range []string{"close", "reopen"} {
		t.Run(action, func(t *testing.T) {
			d := issueWriteDelegate(action)
			out, stderr, deps := issueWriteTestDeps(t, d)
			if code := Run(context.Background(), issueWriteArgs(t, action), deps); code != 2 || stderr.Len() != 0 {
				t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, stderr)
			}
			ok, code, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
			if ok || code != uxv1.CodeUnsupported || receipt.Outcome != "refused" || receipt.MutationAttempts != 0 || len(d.inputBodies) != 0 {
				t.Fatalf("code=%s receipt=%+v writes=%d", code, receipt, len(d.inputBodies))
			}
			envelope := validateEmitted(t, out.Bytes())
			failure := envelope["error"].(map[string]any)
			for _, code := range []string{
				"validation_error", "authentication_error", "forbidden", "not_found", "conflict",
				"rate_limited", "ambiguous_create", "ambiguous_update", "internal_error",
			} {
				failure["code"] = code
				if err := schema.Validate(envelope); err == nil {
					t.Errorf("schema accepted %s with a refused issue write", code)
				}
			}
			failure["code"] = "unsupported"
			failure["receipt"].(map[string]any)["write"].(map[string]any)["outcome"] = "unchanged"
			if err := schema.Validate(envelope); err == nil {
				t.Error("schema accepted a successful no-op receipt in an error envelope")
			}
		})
	}

	for _, action := range []string{"create", "comment"} {
		for _, outcome := range []string{"rejected", "ambiguous"} {
			t.Run(action+"/"+outcome, func(t *testing.T) {
				d := issueWriteDelegate(action)
				op := issueCreateOperation
				if action == "comment" {
					op = issueNoteCreateOperation
				}
				failure := context.DeadlineExceeded
				if outcome == "rejected" {
					failure, _ = uxv1.NewHTTPRejection(422)
				}
				d.errors[op] = []error{failure}
				out, stderr, deps := issueWriteTestDeps(t, d)
				if code := Run(context.Background(), issueWriteArgs(t, action), deps); code == 0 || stderr.Len() != 0 {
					t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, stderr)
				}
				ok, _, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
				if ok || receipt.Outcome != outcome || receipt.MutationAttempts != 1 || len(d.inputBodies) != 1 {
					t.Fatalf("receipt=%+v writes=%d", receipt, len(d.inputBodies))
				}
				envelope := validateEmitted(t, out.Bytes())
				for _, code := range []string{"unsupported", "internal_error", "safety_violation"} {
					envelope["error"].(map[string]any)["code"] = code
					if err := schema.Validate(envelope); err == nil {
						t.Errorf("schema accepted %s with a %s issue write", code, outcome)
					}
				}
			})
		}
	}

	t.Run("existing issue edit refusal", func(t *testing.T) {
		before := issueEditFixture()
		d := issueEditDelegate(before, before, issueEditCatalog())
		out, stderr, deps := productTestDeps(t, d)
		args := issueEditArgs(t, stringPointer("new title"), nil, nil, nil, false, "json")
		if code := Run(context.Background(), args, deps); code != 9 || stderr.Len() != 0 {
			t.Fatalf("exit=%d stdout=%s stderr=%s", code, out, stderr)
		}
		assertIssueEditNoMutation(t, d)
		envelope := validateEmitted(t, out.Bytes())
		envelope["error"].(map[string]any)["code"] = "unsupported"
		if err := schema.Validate(envelope); err == nil {
			t.Error("schema accepted unsupported with an existing issue edit refusal")
		}
	})
}
