package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
)

func TestPinnedMRWriteConsumerGrammar(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "mr-writes", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema                   string                                      `json:"schema"`
		Reference                struct{ Repository, Commit, Source string } `json:"reference"`
		Envelope                 string                                      `json:"envelope"`
		Backend                  string                                      `json:"backend"`
		AuthSource               string                                      `json:"auth_source"`
		Commands                 []string                                    `json:"ordinary_commands"`
		RequiredFlags            []string                                    `json:"required_flags"`
		StateTransitions         string                                      `json:"state_transitions"`
		StateNoop                string                                      `json:"state_noop"`
		NoteInput                string                                      `json:"note_input"`
		CreationFlags            []string                                    `json:"creation_flags"`
		ProviderRevisionEnforced bool                                        `json:"provider_revision_enforced"`
		MaximumMutationAttempts  int                                         `json:"maximum_mutation_attempts"`
		NoteReconciliation       string                                      `json:"note_reconciliation"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "glab-axi/mr-writes-consumer-contract/v1" || fixture.Reference.Repository != "https://github.com/kunchenguid/gh-axi" || fixture.Reference.Commit != "2bffd9a5b60ded64d6c9851683b27a480173a7ee" || fixture.Reference.Source != "src/commands/pr.ts" || fixture.Envelope != uxv1.Schema || fixture.Backend != "native" || fixture.AuthSource != "native" || fixture.ProviderRevisionEnforced || fixture.MaximumMutationAttempts != 1 || fixture.NoteReconciliation != "attributable-post-id-then-exact-readback-never-latest-note" {
		t.Fatalf("invalid consumer contract identity: %#v", fixture)
	}
	if fixture.StateTransitions != "temporarily-refused-before-mutation" || fixture.StateNoop != "read-only-unchanged" {
		t.Fatal("unexpected lifecycle authority")
	}
	if !reflect.DeepEqual(fixture.Commands, []string{"comment", "note", "close", "reopen"}) || !reflect.DeepEqual(fixture.CreationFlags, []string{"--assignee-id", "--reviewer-id", "--milestone-id", "--draft"}) {
		t.Fatal("unexpected command/field graph")
	}
	body := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(body, []byte("Ordinary synthetic note."), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range fixture.Commands {
		args := append(mrWriteArgs(action, "opened"), "--auth-source", "native")
		if action == "comment" || action == "note" {
			args = append(args, fixture.NoteInput, body)
		}
		parsed, err := Parse(args)
		if err != nil || parsed.Command == nil {
			t.Fatalf("parse %s: %v", action, err)
		}
		if !parsed.Command.Definition.Write || parsed.Command.Definition.Schema != "mr-write" {
			t.Fatal("consumer write lost registry/schema contract")
		}
		for _, required := range fixture.RequiredFlags {
			if _, err := Parse(removeFlag(args, required, true)); err == nil {
				t.Fatalf("%s accepted missing %s", action, required)
			}
		}
	}
}

func TestMRNoteContentBoundAndUnicodeProse(t *testing.T) {
	path := filepath.Join(t.TempDir(), "body")
	for _, body := range []string{strings.Repeat("a", limits.MaxDescriptionBytes), "好", "Plain note :thumbsup:", "Δοκιμή", "تعليق"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		got, err := readMRNoteBody(path)
		if err != nil || got != body {
			t.Fatalf("bounded prose rejected: length=%d error=%v", len(body), err)
		}
	}
	for _, body := range []string{":thumbsup: :thumbsdown:", ":thumbsup:\n:thumbsdown:", "ℹ️", "ℹ️ ℹ️"} {
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readMRNoteBody(path); err == nil {
			t.Fatalf("emoji-only body accepted: %q", body)
		}
	}
}
