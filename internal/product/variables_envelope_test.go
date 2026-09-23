package product

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

func TestNativeVariableRejectionEnvelope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation ACL boundary remains unavailable")
	}
	compiler := jsonschema.NewCompiler()
	compiler.LoadURL = func(url string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("unexpected external schema: %s", url)
	}
	const schemaBase = "https://glab-axi.invalid/schema/"
	for _, name := range []string{
		"glab-axi-ux-v1.schema.json",
		"ux-v1/issue-edit.schema.json",
		"ux-v1/board-ordering-receipt.schema.json",
		"ux-v1/ci-variable-mutation.schema.json",
		"ux-v1/resource-delete.schema.json",
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "schema", filepath.FromSlash(name)))
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
	for _, operation := range []struct{ group, action, class string }{
		{"secret", "set", "absent"}, {"secret", "set", "hidden"}, {"secret", "delete", "hidden"},
		{"variable", "set", "absent"}, {"variable", "set", "ordinary"}, {"variable", "delete", "ordinary"},
	} {
		t.Run(operation.group+"/"+operation.action+"/"+operation.class, func(t *testing.T) {
			f := newNativeVariableFixture(t, operation.class, "rejected", false)
			stdout, stderr, deps, _, _ := f.deps(t, false)
			exit := Run(context.Background(), f.args(operation.group, operation.action, operation.class, "json"), deps)
			f.assertConfidential(t, stdout.String(), stderr.String())
			if exit != 4 {
				t.Fatalf("provider rejection returned exit %d", exit)
			}
			var envelope map[string]any
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal("invalid rejection JSON")
			}
			if err := schema.Validate(envelope); err != nil {
				t.Fatalf("emitted rejection violates the complete public envelope: %v", err)
			}
			rejection := envelope["error"].(map[string]any)
			receipt := rejection["receipt"].(map[string]any)["variable"].(map[string]any)
			if rejection["code"] != "forbidden" || rejection["retryable"] != false || receipt["outcome"] != "rejected" || receipt["mutation_attempted"] != true || receipt["provider_acknowledged"] != false {
				t.Fatal("provider rejection lost its non-retryable mutation evidence")
			}
			f.mu.Lock()
			writes, reads := f.writes, f.inventoryReads
			f.mu.Unlock()
			if writes != 1 || reads != 3 {
				t.Fatal("rejected mutation or bounded reconciliation was skipped or replayed")
			}
			for _, outcome := range []string{"not_applied", "precondition_observed", "postcondition_observed"} {
				receipt["outcome"] = outcome
				if schema.Validate(envelope) == nil {
					t.Fatalf("error envelope accepted outcome %s", outcome)
				}
			}
			receipt["outcome"] = "rejected"
			rejection["code"] = "upstream_error"
			if schema.Validate(envelope) == nil {
				t.Fatal("complete envelope ignored variable-specific error codes")
			}
			rejection["code"] = "forbidden"
			envelope["ok"] = true
			if schema.Validate(envelope) == nil {
				t.Fatal("complete envelope accepted an error as success")
			}
			envelope["ok"] = false
			receipt["value"] = "unexpected"
			if schema.Validate(envelope) == nil {
				t.Fatal("referenced receipt schema accepted a value disclosure")
			}
		})
	}
}
