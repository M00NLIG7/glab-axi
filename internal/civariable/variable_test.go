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
		if class == "hidden" {
			raw["value"] = nil
		}
		body, _ := json.Marshal([]any{raw})
		items, err := Decode(body, Comparison{Key: "KEY", Scope: "production", Expected: &sentinel, Desired: &sentinel})
		wantMatch := class != "hidden"
		if err != nil || len(items) != 1 || items[0].Class != class || items[0].MatchesExpected != wantMatch || items[0].MatchesDesired != wantMatch {
			t.Fatal("invalid safe metadata comparison")
		}
		out, _ := json.Marshal(items)
		if strings.Contains(string(out), sentinel) || strings.Contains(string(out), "Matches") || strings.Contains(string(out), "value") {
			t.Fatal("sensitive value crossed normalization boundary")
		}
		for _, match := range []Comparison{{Key: "KEY", Scope: "*", Expected: &sentinel, Desired: &sentinel}, {Key: "OTHER", Scope: "production", Expected: &sentinel, Desired: &sentinel}} {
			items, err = Decode(body, match)
			if err != nil || items[0].MatchesExpected || items[0].MatchesDesired {
				t.Fatal("private comparison matched wrong identity")
			}
		}
	}
}

func TestDecodeUnavailableValuesNeverMatch(t *testing.T) {
	for _, hidden := range []bool{false, true} {
		for _, value := range []any{nil, 7, "", "synthetic-private-value"} {
			raw := map[string]any{"key": "KEY", "environment_scope": "*", "variable_type": "env_var", "masked": hidden, "hidden": hidden, "protected": false, "raw": true, "value": value}
			for _, missing := range []bool{false, true} {
				if missing {
					delete(raw, "value")
				}
				body, _ := json.Marshal([]any{raw})
				for _, expected := range []string{"", "synthetic-private-value", "different-private-value"} {
					items, err := Decode(body, Comparison{Key: "KEY", Scope: "*", Expected: &expected, Desired: &expected})
					readable, isString := value.(string)
					want := !hidden && !missing && isString && readable == expected
					if err != nil || len(items) != 1 || items[0].MatchesExpected != want || items[0].MatchesDesired != want {
						t.Fatal("unavailable or unequal value became a match")
					}
				}
			}
		}
	}
}

func TestDecodeFailsClosedForMissingAmbiguousMalformedMetadata(t *testing.T) {
	base := `[{"key":"KEY","environment_scope":"*","variable_type":"env_var","masked":true,"hidden":true,"protected":false,"raw":true,"value":null}]`
	tests := []string{"null", "{}", base[1 : len(base)-1], "[null]", "[[]]", "[{}]", base + "{}", strings.Replace(base, `"hidden":true,`, "", 1), strings.Replace(base, `"hidden":true`, `"hidden":null`, 1), strings.Replace(base, `"hidden":true`, `"hidden":false,"hidden":true`, 1), strings.Replace(base, `"masked":true`, `"masked":false`, 1), strings.Replace(base, `"variable_type":"env_var"`, `"variable_type":"unknown"`, 1), strings.Replace(base, `"key":"KEY"`, `"key":"../KEY"`, 1), string([]byte{0xff})}
	for i, input := range tests {
		if _, err := Decode([]byte(input), Comparison{}); err == nil {
			t.Fatalf("accepted malformed security metadata case %d", i)
		}
	}
	for _, count := range []int{0, 1, 100, 101} {
		objects := make([]string, count)
		for i := range objects {
			objects[i] = base[1 : len(base)-1]
		}
		items, err := Decode([]byte("["+strings.Join(objects, ",")+"]"), Comparison{})
		if count > 100 {
			if err == nil {
				t.Fatal("unbounded array accepted")
			}
		} else if err != nil || items == nil || len(items) != count {
			t.Fatal("bounded array rejected")
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
