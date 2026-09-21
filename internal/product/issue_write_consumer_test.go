package product

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func TestPinnedIssueWritesConsumerContract(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "issue-writes", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Schema     string `json:"schema"`
		Envelope   string `json:"envelope"`
		DataSchema string `json:"data_schema"`
		Reference  struct {
			Commit string `json:"commit"`
		} `json:"reference"`
		Cases []struct {
			Action   string         `json:"action"`
			Argv     []string       `json:"argv"`
			Provider glab.Operation `json:"provider_operation"`
			Outcome  string         `json:"outcome"`
		} `json:"cases"`
		Excluded  []string `json:"excluded_flags"`
		Attempts  int      `json:"max_mutation_attempts"`
		Atomic    bool     `json:"atomic_precondition"`
		RetrySafe bool     `json:"retry_safe"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Schema != "glab-axi/issue-writes-consumer-contract/v1" || contract.Envelope != uxv1.Schema || contract.DataSchema != "schema/ux-v1/issue-write.schema.json" || contract.Reference.Commit != "2bffd9a5b60ded64d6c9851683b27a480173a7ee" || contract.Attempts != 1 || contract.Atomic || contract.RetrySafe || len(contract.Cases) != 5 {
		t.Fatalf("contract=%+v", contract)
	}
	for _, c := range contract.Cases {
		t.Run(c.Action, func(t *testing.T) {
			dir := t.TempDir()
			title, body := filepath.Join(dir, "title"), filepath.Join(dir, "body")
			if err := os.WriteFile(title, []byte("new title"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(body, []byte("new body"), 0600); err != nil {
				t.Fatal(err)
			}
			args := append([]string{}, c.Argv...)
			for i, v := range args {
				if v == "{title}" {
					args[i] = title
				}
				if v == "{body}" {
					args[i] = body
				}
			}
			for _, flag := range contract.Excluded {
				if _, err := Parse(append(append([]string{}, args...), flag, "unapproved")); err == nil {
					t.Fatalf("accepted %s", flag)
				}
			}
			// Identical retries are independent invocations, not replay receipts. A
			// response lost on the first attempt cannot suppress the second mutation.
			for attempt := 0; attempt < 3; attempt++ {
				d := issueWriteDelegate(c.Action)
				wantExit, wantOutcome := 0, c.Outcome
				if c.Action == "create" {
					body := string(d.responses[c.Provider][0].Body)
					body = strings.Replace(body, `"id":1001`, `"id":`+strconv.Itoa(1001+attempt), 1)
					body = strings.Replace(body, `"iid":42`, `"iid":`+strconv.Itoa(42+attempt), 1)
					body = strings.Replace(body, `/issues/42`, `/issues/`+strconv.Itoa(42+attempt), 1)
					d.responses[c.Provider][0].Body = []byte(body)
				}
				if c.Provider == glab.OpIssueNoteCreate {
					d.responses[c.Provider][0].Body = []byte(strings.Replace(string(d.responses[c.Provider][0].Body), `"id":3001`, `"id":`+strconv.Itoa(3001+attempt), 1))
				}
				if attempt == 0 {
					d.errors[c.Provider] = []error{uxv1.Wrap(uxv1.CodeUpstream, "response lost", context.DeadlineExceeded)}
					wantExit, wantOutcome = 6, "ambiguous"
				}
				out, _, deps := productTestDeps(t, d)
				if code := Run(context.Background(), args, deps); code != wantExit {
					t.Fatalf("exit=%d %s", code, out)
				}
				_, _, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
				if receipt.Outcome != wantOutcome || receipt.MutationAttempts != contract.Attempts || receipt.RetrySafe || countOperation(d.requests, c.Provider) != 1 {
					t.Fatalf("receipt=%+v requests=%+v", receipt, d.requests)
				}
				if attempt > 0 && c.Action == "create" && receipt.Identity.IssueID != int64(1001+attempt) {
					t.Fatalf("new invocation reused earlier issue identity: %+v", receipt)
				}
				if attempt > 0 && c.Provider == glab.OpIssueNoteCreate && receipt.NoteID != int64(3001+attempt) {
					t.Fatalf("new invocation reused earlier note identity: %+v", receipt)
				}
			}
		})
	}
	if issueWritePreflight+issueWriteAttempt+issueWriteReadback > limits.WriteOperation {
		t.Fatal("phase limits exceed outer deadline")
	}
}
