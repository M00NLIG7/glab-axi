package collaborationtest

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

func collaborationCLIValidator(t *testing.T) func(*testing.T, []byte, string) {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.LoadURL = func(url string) (io.ReadCloser, error) {
		return nil, fmt.Errorf("unexpected external schema: %s", url)
	}
	const base = "https://glab-axi.invalid/schema/"
	for _, name := range []string{
		"glab-axi-ux-v1.schema.json", "ux-v1/issue-edit.schema.json", "ux-v1/issue-write.schema.json",
		"ux-v1/board-ordering-receipt.schema.json", "ux-v1/ci-variable-mutation.schema.json", "ux-v1/resource-delete.schema.json",
		"ux-v1/mr-discussions.schema.json", "ux-v1/issue-discussions.schema.json", "ux-v1/mr-approvals.schema.json",
	} {
		body, err := os.ReadFile(filepath.Join("..", "..", "schema", name))
		if err != nil {
			t.Fatal(err)
		}
		if err := compiler.AddResource(base+name, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	envelope, err := compiler.Compile(base + "glab-axi-ux-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	schemas := map[string]*jsonschema.Schema{}
	for _, name := range []string{"issue-discussions", "mr-approvals"} {
		schemas[name], err = compiler.Compile(base + "ux-v1/" + name + ".schema.json")
		if err != nil {
			t.Fatal(err)
		}
	}
	return func(t *testing.T, body []byte, name string) {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal(body, &value); err != nil {
			t.Fatal(err)
		}
		if err := envelope.Validate(value); err != nil {
			t.Fatalf("CLI envelope violates schema: %v", err)
		}
		if data, ok := value["data"]; ok {
			if err := schemas[name].Validate(data); err != nil {
				t.Fatalf("CLI collaboration data violates %s schema: %v", name, err)
			}
			// Public top-level contracts must not silently accept new fields.
			data.(map[string]any)["unexpected"] = true
			if err := schemas[name].Validate(data); err == nil {
				t.Fatalf("%s accepts unknown output fields", name)
			}
		}
	}
}
