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
			Name   glab.Operation `json:"name"`
			Fields []string       `json:"response_fields"`
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

func TestIssueCreateExactBounds(t *testing.T) {
	for _, description := range []string{"body", strings.Repeat("界", limits.MaxDescriptionBytes/3) + "xy"} {
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
		d := issueWriteDelegate("comment")
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
		if code := Run(context.Background(), issueWriteArgs(t, "comment"), deps); code != 6 {
			t.Fatalf("exit=%d %s", code, out)
		}
		_, _, r := decodeIssueWriteEnvelope(t, out.Bytes())
		if r.Outcome != "ambiguous" || r.MutationAttempts != 1 || r.ObservedState != "" {
			t.Fatalf("receipt=%+v", r)
		}
	})
	t.Run("expired-caller", func(t *testing.T) {
		d := issueWriteDelegate("comment")
		out, _, deps := issueWriteTestDeps(t, d)
		args := issueWriteArgs(t, "comment")
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
		defer cancel()
		if code := Run(ctx, args, deps); code != 8 || len(d.requests) != 0 || len(d.inputBodies) != 0 {
			t.Fatalf("expired caller: exit=%d requests=%d writes=%d output=%s", code, len(d.requests), len(d.inputBodies), out)
		}
	})
	for _, phase := range []string{"preflight", "mutation"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			d := issueWriteDelegate("comment")
			phaseReached := false
			d.doFunc = func(ctx context.Context, r glab.Request) (glab.Response, error, bool) {
				deadline, ok := ctx.Deadline()
				if !ok || time.Until(deadline) > limits.WriteOperation {
					t.Fatal("missing bounded phase deadline")
				}
				block := phase == "preflight" || phase == "mutation" && r.Operation == issueNoteCreateOperation
				if block {
					phaseReached = true
					cancel()
					<-ctx.Done()
					if ctx.Err() != context.Canceled {
						t.Fatal("request did not observe caller cancellation")
					}
					return glab.Response{Write: r.Operation == issueNoteCreateOperation}, uxv1.Wrap(uxv1.CodeCanceled, "controlled", ctx.Err()), true
				}
				return glab.Response{}, nil, false
			}
			out, _, deps := issueWriteTestDeps(t, d)
			code := Run(ctx, issueWriteArgs(t, "comment"), deps)
			if !phaseReached {
				t.Fatalf("selected cancellation phase was not reached: exit=%d output=%s", code, out)
			}
			want := 6
			if phase == "preflight" {
				want = 130 // explicit cancellation, distinct from an expired caller deadline
			}
			if code != want {
				t.Fatalf("exit=%d %s", code, out)
			}
			if phase == "preflight" {
				if len(d.inputBodies) != 0 {
					t.Fatalf("canceled preflight attempted %d writes", len(d.inputBodies))
				}
			} else {
				_, _, r := decodeIssueWriteEnvelope(t, out.Bytes())
				if r.Outcome != "ambiguous" || r.MutationAttempts != 1 || len(d.inputBodies) != 1 {
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
