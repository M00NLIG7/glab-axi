package product

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v5"
)

// Validate actual CLI output against both the public envelope and edit receipt
// contracts, including successful data that the shared envelope leaves generic.
func issueEditCLIValidator(t *testing.T) func(*testing.T, []byte) {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.LoadURL = func(url string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("unexpected external schema: %s", url)
	}
	const base = "https://glab-axi.invalid/schema/"
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
		if err := compiler.AddResource(base+name, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	envelopeSchema, err := compiler.Compile(base + "glab-axi-ux-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	editSchema, err := compiler.Compile(base + "ux-v1/issue-edit.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return func(t *testing.T, body []byte) {
		t.Helper()
		var envelope map[string]any
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if err := envelopeSchema.Validate(envelope); err != nil {
			t.Fatalf("CLI envelope violates published schema: %v", err)
		}
		if data, ok := envelope["data"]; ok {
			if err := editSchema.Validate(data); err != nil {
				t.Fatalf("CLI edit receipt violates published schema: %v", err)
			}
		}
	}
}
