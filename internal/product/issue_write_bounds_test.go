package product

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func TestIssueWriteProviderFieldsAreRequiredEvidence(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "issue-writes", "provider-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Operations []struct {
			Name    glab.Operation    `json:"name"`
			Payload map[string]string `json:"payload"`
			Fields  []string          `json:"response_fields"`
		} `json:"operations"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, operation := range fixture.Operations {
		action := "create"
		if operation.Name == issueNoteCreateOperation {
			action = "comment"
		}
		if operation.Name == issueStateOperation {
			action = operation.Payload["state_event"]
		}
		for _, field := range operation.Fields {
			t.Run(action+"/missing-"+field, func(t *testing.T) {
				d := issueWriteDelegate(action)
				var response map[string]any
				if err := json.Unmarshal(d.responses[operation.Name][0].Body, &response); err != nil {
					t.Fatal(err)
				}
				delete(response, field)
				d.responses[operation.Name][0].Body, err = json.Marshal(response)
				if err != nil {
					t.Fatal(err)
				}
				out, _, deps := issueWriteTestDeps(t, d)
				if code := Run(context.Background(), issueWriteArgs(t, action), deps); code != 6 {
					t.Fatalf("exit=%d %s", code, out)
				}
				_, _, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
				if receipt.Outcome != "ambiguous" || receipt.MutationAttempts != 1 || receipt.NoteID != 0 {
					t.Fatalf("receipt=%+v", receipt)
				}
			})
		}
	}
}

func TestIssueCreateEmptyDescriptionAndExactBounds(t *testing.T) {
	for _, description := range []string{"", strings.Repeat("界", limits.MaxDescriptionBytes/3) + "xy"} {
		t.Run(strconv.Itoa(len(description)), func(t *testing.T) {
			args := issueWriteArgs(t, "create")
			title := strings.Repeat("x", limits.MaxTitleBytes)
			for i, v := range args {
				if v == "--title-file" {
					if err := os.WriteFile(args[i+1], []byte(title+"\n"), 0600); err != nil {
						t.Fatal(err)
					}
				}
				if v == "--description-file" {
					if err := os.WriteFile(args[i+1], []byte(description), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			d := issueWriteDelegate("create")
			var response map[string]any
			if err := json.Unmarshal(d.responses[issueCreateOperation][0].Body, &response); err != nil {
				t.Fatal(err)
			}
			response["title"], response["description"] = title, description
			if description == "" {
				response["description"] = nil
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			d.responses[issueCreateOperation][0].Body = encoded
			out, _, deps := issueWriteTestDeps(t, d)
			if code := Run(context.Background(), args, deps); code != 0 {
				t.Fatalf("exit=%d %s", code, out)
			}
			if out.Len() > 4096 || strings.Contains(out.String(), title) {
				t.Fatalf("unbounded receipt (%d bytes)", out.Len())
			}
		})
	}
}

func TestIssueWriteAggregateBudgetAndPhaseCancellation(t *testing.T) {
	t.Run("aggregate", func(t *testing.T) {
		d := issueWriteDelegate("close")
		// Four individually legal preflight documents nearly exhaust the 8MiB
		// operation cap. The fifth document cannot turn overflow into success.
		pad := func(body []byte) []byte {
			return append(body, []byte(strings.Repeat(" ", limits.MaxJSONPageBytes-len(body)))...)
		}
		for _, op := range []glab.Operation{issueWriteProjectOperation, issueWriteViewOperation} {
			for i := range d.responses[op] {
				d.responses[op][i].Body = pad(d.responses[op][i].Body)
			}
		}
		out, _, deps := issueWriteTestDeps(t, d)
		if code := Run(context.Background(), issueWriteArgs(t, "close"), deps); code != 6 {
			t.Fatalf("exit=%d %s", code, out)
		}
		_, _, r := decodeIssueWriteEnvelope(t, out.Bytes())
		if r.Outcome != "ambiguous" || r.MutationAttempts != 1 || r.ObservedState != "" {
			t.Fatalf("receipt=%+v", r)
		}
	})
	for _, phase := range []string{"preflight", "mutation", "readback"} {
		t.Run(phase, func(t *testing.T) {
			d := issueWriteDelegate("close")
			views := 0
			d.doFunc = func(ctx context.Context, r glab.Request) (glab.Response, error, bool) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > limits.WriteOperation {
					t.Fatal("missing bounded phase deadline")
				}
				if r.Operation == issueWriteViewOperation {
					views++
				}
				block := phase == "preflight" || phase == "mutation" && r.Operation == issueStateOperation || phase == "readback" && views == 3
				if block {
					<-ctx.Done()
					return glab.Response{Write: r.Operation == issueStateOperation}, uxv1.Wrap(uxv1.CodeCanceled, "controlled", ctx.Err()), true
				}
				return glab.Response{}, nil, false
			}
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
			defer cancel()
			out, _, deps := issueWriteTestDeps(t, d)
			start := time.Now()
			code := Run(ctx, issueWriteArgs(t, "close"), deps)
			if time.Since(start) > time.Second {
				t.Fatal("caller deadline ignored")
			}
			want := 6
			if phase == "preflight" {
				want = 8 // caller deadline, distinct from explicit cancellation
			}
			if code != want {
				t.Fatalf("exit=%d %s", code, out)
			}
			if phase != "preflight" {
				_, _, r := decodeIssueWriteEnvelope(t, out.Bytes())
				if r.Outcome != "ambiguous" || r.MutationAttempts != 1 {
					t.Fatalf("receipt=%+v", r)
				}
			}
		})
	}
}

func TestIssueNoteRejectsTransformedOrSystemResponse(t *testing.T) {
	for _, field := range []string{"system", "internal", "noteable_type", "body"} {
		t.Run(field, func(t *testing.T) {
			d := issueWriteDelegate("comment")
			var response map[string]any
			if err := json.Unmarshal(issueNoteBody(), &response); err != nil {
				t.Fatal(err)
			}
			switch field {
			case "system", "internal":
				response[field] = true
			default:
				response[field] = "different"
			}
			encoded, err := json.Marshal(response)
			if err != nil {
				t.Fatal(err)
			}
			d.responses[issueNoteCreateOperation][0].Body = encoded
			out, _, deps := issueWriteTestDeps(t, d)
			if code := Run(context.Background(), issueWriteArgs(t, "comment"), deps); code != 6 {
				t.Fatalf("exit=%d %s", code, out)
			}
			_, _, r := decodeIssueWriteEnvelope(t, out.Bytes())
			if r.NoteID != 0 {
				t.Fatal("unproven note ID disclosed")
			}
		})
	}
}
