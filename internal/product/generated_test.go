package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestEveryCommandDeclaresAUniqueDataSchema(t *testing.T) {
	seenIDs := map[string]string{}
	seenNames := map[string]bool{}
	for _, definition := range Definitions() {
		if seenNames[definition.Schema] {
			continue
		}
		seenNames[definition.Schema] = true
		path := filepath.Join("..", "..", "schema", "ux-v1", definition.Schema+".schema.json")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v", definition.Schema, err)
		}
		var schema struct {
			ID                   string         `json:"$id"`
			Type                 string         `json:"type"`
			AdditionalProperties bool           `json:"additionalProperties"`
			Required             []string       `json:"required"`
			Properties           map[string]any `json:"properties"`
		}
		if err := json.Unmarshal(data, &schema); err != nil {
			t.Fatalf("%s: %v", definition.Schema, err)
		}
		if schema.ID == "" || schema.Type != "object" || schema.AdditionalProperties || len(schema.Required) == 0 || len(schema.Properties) == 0 {
			t.Fatalf("schema %s is not a closed data contract: %#v", definition.Schema, schema)
		}
		if previous := seenIDs[schema.ID]; previous != "" {
			t.Fatalf("schemas %s and %s share $id %s", previous, definition.Schema, schema.ID)
		}
		seenIDs[schema.ID] = definition.Schema
	}
}

func TestGeneratedPublicAssetsMatchCommandRegistry(t *testing.T) {
	contracts := []struct {
		path string
		want string
	}{
		{filepath.Join("..", "..", "skills", "glab-axi", "SKILL.md"), SkillMarkdown()},
		{filepath.Join("..", "..", "docs", "command-reference.md"), CommandReferenceMarkdown()},
	}
	for _, contract := range contracts {
		got, err := os.ReadFile(contract.path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != contract.want {
			t.Fatalf("generated contract %s is stale; run go run ./cmd/gen-product", contract.path)
		}
	}
}
