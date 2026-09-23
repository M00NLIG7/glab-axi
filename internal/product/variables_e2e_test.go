package product

import (
	"encoding/json"
	"testing"

	"gl-axi/internal/civariable"
)

// Synthetic provider objects are shared by the native TLS and compiled-CLI
// regressions. No value-bearing provider fixture is checked into the repository.
func variableRecord(key, scope, class string, protected bool, value string) map[string]any {
	return map[string]any{"key": key, "environment_scope": scope, "variable_type": "env_var", "masked": class == "masked" || class == "hidden", "hidden": class == "hidden", "protected": protected, "raw": true, "value": value, "description": value}
}

func variableWire(record map[string]any) map[string]any {
	wire := make(map[string]any, len(record))
	for key, value := range record {
		wire[key] = value
	}
	if wire["hidden"] == true {
		wire["value"] = nil
	}
	return wire
}

func TestWireVariableClasses(t *testing.T) {
	for _, class := range []string{"ordinary", "protected", "masked", "hidden"} {
		body, _ := json.Marshal([]map[string]any{variableWire(variableRecord("KEY", "*", class, class == "protected", "synthetic-only-value"))})
		var wire []struct {
			Value *string `json:"value"`
		}
		if json.Unmarshal(body, &wire) != nil || len(wire) != 1 || (wire[0].Value == nil) != (class == "hidden") {
			t.Fatal("fixture value visibility contradicts the provider contract")
		}
		items, err := civariable.Decode(body, civariable.Comparison{})
		if err != nil || len(items) != 1 || items[0].Class != class {
			t.Fatal("invalid wire class fixture")
		}
	}
}
