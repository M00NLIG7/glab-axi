package product

import (
	"encoding/json"
	"testing"

	"gl-axi/internal/civariable"
)

// Synthetic provider objects are shared by the native TLS and compiled-CLI
// regressions. No value-bearing provider fixture is checked into the repository.
func wireVariable(key, scope, class string, protected bool, value string) map[string]any {
	return map[string]any{"key": key, "environment_scope": scope, "variable_type": "env_var", "masked": class == "masked" || class == "hidden", "hidden": class == "hidden", "protected": protected, "raw": true, "value": value, "description": value}
}

func TestWireVariableClasses(t *testing.T) {
	for _, class := range []string{"ordinary", "protected", "masked", "hidden"} {
		body, _ := json.Marshal([]map[string]any{wireVariable("KEY", "*", class, class == "protected", "synthetic-only-value")})
		items, err := civariable.Decode(body, true, civariable.Comparison{})
		if err != nil || len(items) != 1 || items[0].Class != class {
			t.Fatal("invalid wire class fixture")
		}
	}
}
