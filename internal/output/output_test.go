package output

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"

	v1 "gl-axi/internal/contract/v1"
	"gl-axi/internal/limits"
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
	for _, format := range []Format{JSON, TOON} {
		t.Run(string(format), func(t *testing.T) {
			var base bytes.Buffer
			if err := Write(&base, format, v1.Success(map[string]any{"payload": ""}, false)); err != nil {
				t.Fatal(err)
			}
			payloadAtLimit := strings.Repeat("x", limits.MaxOperationBytes-base.Len()+1)
			var exact bytes.Buffer
			if err := Write(&exact, format, v1.Success(map[string]any{"payload": payloadAtLimit}, false)); err != nil {
				t.Fatalf("exact output boundary rejected: %v", err)
			}
			if exact.Len() != limits.MaxOperationBytes+1 {
				t.Fatalf("exact output len=%d", exact.Len())
			}
			writer := &recordingWriter{}
			if err := Write(writer, format, v1.Success(map[string]any{"payload": payloadAtLimit + "x"}, false)); err == nil || writer.calls != 0 {
				t.Fatalf("oversized output: error=%v writes=%d", err, writer.calls)
			}
		})
	}
}

type recordingWriter struct {
	calls int
	err   error
	n     int
}

func (w *recordingWriter) Write([]byte) (int, error) {
	w.calls++
	return w.n, w.err
}

func TestWriteValueWriterFailures(t *testing.T) {
	writeErr := errors.New("writer failed")
	for _, format := range []Format{JSON, TOON} {
		for _, test := range []struct {
			name string
			n    int
			err  error
			want error
		}{
			{"unwritten", 0, writeErr, writeErr},
			{"partial", 5, writeErr, writeErr},
			{"short", 5, nil, io.ErrShortWrite},
		} {
			t.Run(string(format)+"/"+test.name, func(t *testing.T) {
				writer := &recordingWriter{n: test.n, err: test.err}
				err := WriteValue(writer, format, v1.Success(map[string]any{"value": "result"}, false))
				if !errors.Is(err, test.want) || writer.calls != 1 {
					t.Fatalf("error=%v want=%v writes=%d", err, test.want, writer.calls)
				}
			})
		}
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
