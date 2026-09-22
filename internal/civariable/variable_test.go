package civariable

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestDecodeNeverExportsValuesOrDescriptions(t *testing.T) {
	sentinel := strings.Join([]string{"synthetic", "value", "sentinel"}, "-")
	for _, class := range []string{"ordinary", "protected", "masked", "hidden"} {
		raw := map[string]any{"key": "KEY", "environment_scope": "production", "variable_type": "env_var", "masked": class == "masked" || class == "hidden", "hidden": class == "hidden", "protected": class == "protected", "raw": true, "value": sentinel, "description": sentinel, "other": sentinel}
		for _, list := range []bool{true, false} {
			var data any = raw
			if list {
				data = []any{raw}
			}
			body, _ := json.Marshal(data)
			items, err := Decode(body, list, Comparison{Key: "KEY", Scope: "production", Expected: &sentinel, Desired: &sentinel})
			if err != nil || len(items) != 1 || items[0].Class != class || !items[0].MatchesExpected || !items[0].MatchesDesired {
				t.Fatal("invalid safe metadata comparison")
			}
			out, _ := json.Marshal(items)
			if strings.Contains(string(out), sentinel) || strings.Contains(string(out), "Matches") || strings.Contains(string(out), "value") {
				t.Fatal("sensitive value crossed normalization boundary")
			}
			items, err = Decode(body, list, Comparison{Key: "KEY", Scope: "*", Expected: &sentinel})
			if err != nil || items[0].MatchesExpected {
				t.Fatal("private comparison matched wrong scope")
			}
		}
	}
}
func TestDecodeFailsClosedForMissingAmbiguousMalformedMetadata(t *testing.T) {
	base := `{"key":"KEY","environment_scope":"*","variable_type":"env_var","masked":true,"hidden":true,"protected":false,"raw":true,"value":"synthetic-only-value"}`
	tests := []string{"null", "{}", "[]", base + "{}", strings.Replace(base, `"hidden":true,`, "", 1), strings.Replace(base, `"hidden":true`, `"hidden":null`, 1), strings.Replace(base, `"hidden":true`, `"hidden":false,"hidden":true`, 1), strings.Replace(base, `"masked":true`, `"masked":false`, 1), strings.Replace(base, `"variable_type":"env_var"`, `"variable_type":"unknown"`, 1), strings.Replace(base, `"key":"KEY"`, `"key":"../KEY"`, 1)}
	for i, input := range tests {
		if _, err := Decode([]byte(input), false, Comparison{}); err == nil {
			t.Fatalf("accepted malformed security metadata case %d", i)
		}
	}
}
func TestVariableInputBoundsAndMasking(t *testing.T) {
	for _, s := range []string{"", strings.Repeat("a", MaxValueBytes+1), "has\x00nul", string([]byte{0xff})} {
		if ValidValue(s, false) {
			t.Fatal("invalid bounded input accepted")
		}
	}
	for _, s := range []string{"short", "eight chars", "with\nnewline", "white\tspace"} {
		if ValidValue(s, true) {
			t.Fatal("invalid masked input accepted")
		}
	}
	if !ValidValue("multiline\nordinary", false) || !ValidValue("synthetic-masked", true) || !ValidValue(strings.Repeat("a", MaxValueBytes), false) {
		t.Fatal("valid private input rejected")
	}
}
