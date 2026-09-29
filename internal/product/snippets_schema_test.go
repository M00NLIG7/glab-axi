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

func snippetSchemaValidator(t *testing.T) func(*testing.T, string, []byte) {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.LoadURL = func(url string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("unexpected external schema: %s", url)
	}
	const base = "https://glab-axi.invalid/schema/"
	for _, name := range []string{
		"glab-axi-ux-v1.schema.json", "ux-v1/snippet-list.schema.json", "ux-v1/snippet-view.schema.json",
		"ux-v1/issue-write.schema.json", "ux-v1/issue-edit.schema.json", "ux-v1/board-ordering-receipt.schema.json",
		"ux-v1/ci-variable-mutation.schema.json", "ux-v1/resource-delete.schema.json",
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "schema", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource(base+name, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	compiled := map[string]*jsonschema.Schema{}
	for name, path := range map[string]string{"envelope": "glab-axi-ux-v1.schema.json", "list": "ux-v1/snippet-list.schema.json", "view": "ux-v1/snippet-view.schema.json"} {
		schema, err := compiler.Compile(base + path)
		if err != nil {
			t.Fatal(err)
		}
		compiled[name] = schema
	}
	return func(t *testing.T, leaf string, body []byte) {
		t.Helper()
		var envelope map[string]any
		if err := json.Unmarshal(body, &envelope); err != nil {
			t.Fatal(err)
		}
		if err := compiled["envelope"].Validate(envelope); err != nil {
			t.Fatalf("snippet envelope violates schema: %v", err)
		}
		if envelope["ok"] == true {
			if err := compiled[leaf].Validate(envelope["data"]); err != nil {
				t.Fatalf("snippet data violates %s schema: %v", leaf, err)
			}
		}
	}
}
