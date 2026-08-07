package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	v1 "glab-axi/internal/contract/v1"
	"glab-axi/internal/limits"
)

func TestJSONEnvelopeIsVersionedAndOmitsCauses(t *testing.T) {
	failure := v1.Wrap(v1.CodeUpstream, "controlled message", errors.New("untrusted cause detail"))
	var output bytes.Buffer
	if err := Write(&output, JSON, v1.Failure(failure)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "untrusted") {
		t.Fatal("error cause reached output")
	}
	var envelope map[string]any
	if err := json.Unmarshal(output.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope["schema"] != v1.Schema || envelope["ok"] != false {
		t.Fatalf("unexpected envelope: %v", envelope)
	}
}

func TestOutputLimitBoundary(t *testing.T) {
	base := v1.Success(map[string]any{"payload": ""}, false)
	encoded, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	payloadAtLimit := strings.Repeat("x", limits.MaxOperationBytes-len(encoded))
	var exact bytes.Buffer
	if err := Write(&exact, JSON, v1.Success(map[string]any{"payload": payloadAtLimit}, false)); err != nil {
		t.Fatalf("exact output boundary rejected: %v", err)
	}
	if exact.Len() != limits.MaxOperationBytes+1 { // framing newline is outside the JSON-body bound
		t.Fatalf("exact output len=%d", exact.Len())
	}
	var over bytes.Buffer
	if err := Write(&over, JSON, v1.Success(map[string]any{"payload": payloadAtLimit + "x"}, false)); err == nil {
		t.Fatal("output boundary+1 was accepted")
	}
}

func TestTOONUsesStableKeysAndTables(t *testing.T) {
	value := map[string]any{"jobs": []map[string]any{{"id": 1, "name": "test"}, {"id": 2, "name": "lint"}}}
	var first, second bytes.Buffer
	if err := Write(&first, TOON, v1.Success(value, false)); err != nil {
		t.Fatal(err)
	}
	if err := Write(&second, TOON, v1.Success(value, false)); err != nil {
		t.Fatal(err)
	}
	if first.String() != second.String() || !strings.Contains(first.String(), "jobs[2]{id,name}:") || !strings.Contains(first.String(), `1,"test"`) {
		t.Fatalf("unexpected TOON output:\n%s", first.String())
	}
}
