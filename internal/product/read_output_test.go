package product

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

type partialProductWriter struct {
	bytes.Buffer
	calls int
	n     int
	err   error
}

func (w *partialProductWriter) Write(data []byte) (int, error) {
	w.calls++
	n, _ := w.Buffer.Write(data[:min(w.n, len(data))])
	return n, w.err
}

func TestProductOutputWriterFailures(t *testing.T) {
	for _, format := range []string{"json", "toon"} {
		for _, test := range []struct {
			name string
			n    int
			err  error
		}{
			{"unwritten", 0, uxv1.NewError(uxv1.CodeUpstream, "writer refused")},
			{"partial", 5, uxv1.NewError(uxv1.CodeUpstream, "writer refused")},
			{"short", 5, nil},
		} {
			t.Run(format+"/"+test.name, func(t *testing.T) {
				delegate := &fakeDelegate{responses: map[glab.Operation][]glab.Response{
					glab.OpIssueList: {{Body: []byte(`[]`), UpstreamVersion: glab.SupportedVersion}},
				}}
				_, stderr, deps := productTestDeps(t, delegate)
				writer := &partialProductWriter{n: test.n, err: test.err}
				deps.Runtime.Stdout = writer
				code := Run(context.Background(), readParityArgs([]string{"issue", "list", "--format", format}), deps)
				if code != 8 || writer.calls != 1 || writer.Len() != test.n || stderr.String() != "gl-axi: output failure\n" || len(delegate.requests) != 1 {
					t.Fatalf("exit=%d writes=%d bytes=%d stderr=%s requests=%#v", code, writer.calls, writer.Len(), stderr, delegate.requests)
				}
			})
		}
	}
}

func TestProductSerializationFailureEnvelope(t *testing.T) {
	for _, format := range []string{"json", "toon"} {
		t.Run(format, func(t *testing.T) {
			stdout, stderr, deps := productTestDeps(t, nil)
			calls := 0
			deps.NewDelegate = func() delegateClient {
				t.Fatal("serialization failure started provider work")
				return nil
			}
			deps.SetupHooks = func(context.Context) (any, error) {
				calls++
				return map[string]any{"invalid": make(chan int)}, nil
			}
			if code := Run(context.Background(), []string{"setup", "hooks", "--format", format}, deps); code != 8 || stderr.Len() != 0 || calls != 1 {
				t.Fatalf("exit=%d output=%s stderr=%s calls=%d", code, stdout, stderr, calls)
			}
			if stdout.Len() > limits.MaxErrorOutputBytes {
				t.Fatalf("unbounded encoding failure: %d bytes", stdout.Len())
			}
			if format == "json" {
				var envelope uxv1.Envelope
				if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
					t.Fatal(err)
				}
				if envelope.OK || envelope.Data != nil || envelope.Error == nil || envelope.Error.Code != uxv1.CodeInternal || envelope.Error.Message != "cannot encode output" || envelope.Meta.Complete {
					t.Fatalf("unexpected encoding failure: %s", stdout)
				}
			} else if !strings.HasPrefix(stdout.String(), "schema: \"glab-axi/ux-v1\"\nok: false\nerror:\n  code: \"internal_error\"\n  message: \"cannot encode output\"\n") || !strings.Contains(stdout.String(), "\n  complete: false\n") || strings.Contains(stdout.String(), "\ndata:") {
				t.Fatalf("unexpected TOON encoding failure: %s", stdout)
			}
		})
	}
}
