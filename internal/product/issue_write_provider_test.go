package product

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/testgitlab"
)

type issueWriteProviderFixture struct {
	mu              sync.Mutex
	state           string
	description     string
	template        string
	raceDescription string
	labels          []string
	writes          int
}

func (f *issueWriteProviderFixture) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	record := map[string]any{
		"id": 1001, "iid": 42, "project_id": 101, "title": "new title",
		"issue_type": "issue", "updated_at": "2026-08-15T12:00:00Z",
		"web_url": issueNativeWeb + "/group/project/-/issues/42",
	}
	path := strings.TrimPrefix(r.URL.EscapedPath(), issueNativeAPIPath)
	switch r.Method + " " + path {
	case "GET /projects/group%2Fproject":
		_ = json.NewEncoder(w).Encode(map[string]any{"id": 101, "path_with_namespace": "group/project", "web_url": issueNativeWeb + "/group/project"})
		return
	case "GET /projects/101/issues/42":
	case "POST /projects/101/issues", "POST /projects/101/issues/42/notes", "PUT /projects/101/issues/42":
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.writes++
		if r.Method == http.MethodPut {
			if f.raceDescription != "" {
				f.description = f.raceDescription
			}
			f.description = strings.TrimSuffix(f.description, "\n/label ~bug")
			f.description = strings.TrimRight(strings.ReplaceAll(f.description, "\r", ""), " \t\n\v\f")
			f.state = "closed"
			if payload["state_event"] == "reopen" {
				f.state = "opened"
			}
		} else if strings.HasSuffix(path, "/notes") {
			body := strings.TrimRight(strings.ReplaceAll(payload["body"], "\r", ""), " \t\n\v\f")
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": 3001, "project_id": 101, "noteable_id": 1001, "noteable_iid": 42,
				"noteable_type": "Issue", "body": body, "system": false, "internal": false,
			})
			return
		} else {
			f.description = payload["description"]
			if strings.TrimSpace(f.description) == "" {
				f.description = f.template
			}
			if f.description == "/label ~bug" {
				f.labels = []string{"bug"}
				f.description = ""
			}
			f.description = strings.TrimRight(strings.ReplaceAll(f.description, "\r", ""), " \t\n\v\f")
			f.state = "opened"
			w.WriteHeader(http.StatusCreated)
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		return
	}
	record["description"], record["state"], record["labels"] = f.description, f.state, f.labels
	_ = json.NewEncoder(w).Encode(record)
}

func setIssueWriteBodyFile(t *testing.T, args []string, body string) {
	t.Helper()
	for i, arg := range args {
		if arg == "--body-file" || arg == "--description-file" {
			if err := os.WriteFile(args[i+1], []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			return
		}
	}
	t.Fatal("missing private body file")
}

func TestIssueWriteProviderContentNormalization(t *testing.T) {
	for _, action := range []string{"create", "comment", "note"} {
		for _, test := range []struct{ name, input, want string }{
			{"lf", "hello\n", "hello"},
			{"crlf", "hello\r\nworld\r\n", "hello\nworld"},
			{"bare-cr", "hel\rlo", "hello"},
			{"trailing-ascii", "hello \t\n\v\f", "hello"},
			{"internal", "  hello \t\n\nworld", "  hello \t\n\nworld"},
			{"unicode-space", "hello\u00a0\u2003\n", "hello\u00a0\u2003"},
			{"inline-command", "keep /label ~bug\n", "keep /label ~bug"},
		} {
			t.Run(action+"/"+test.name, func(t *testing.T) {
				provider := &issueWriteProviderFixture{state: "opened", description: "existing body"}
				server := testgitlab.New(provider)
				defer server.Close()
				token := strings.Join([]string{"synthetic", "provider", "normalization"}, "-")
				out, stderr, deps := issueNativeDeps(t, server, token, &issueNativeKeyring{})
				args := issueNativeArgs(t, action)
				setIssueWriteBodyFile(t, args, test.input)
				code := Run(context.Background(), args, deps)
				requests := server.Requests()
				assertIssueNativePrivate(t, out.Bytes(), stderr.Bytes(), requests, token)
				if code != 0 {
					t.Fatalf("ordinary content exit=%d output=%s", code, out)
				}
				_, _, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
				writes := 0
				for _, request := range requests {
					if request.Method != http.MethodPost {
						continue
					}
					writes++
					var payload map[string]string
					if err := json.Unmarshal(request.Body, &payload); err != nil {
						t.Fatal(err)
					}
					field := "body"
					if action == "create" {
						field = "description"
					}
					if payload[field] != test.want {
						t.Fatalf("submitted content=%q want=%q", payload[field], test.want)
					}
					sum := sha256.Sum256(request.Body)
					if receipt.RequestedSHA256 != hex.EncodeToString(sum[:]) {
						t.Fatal("receipt does not hash the submitted request")
					}
				}
				if writes != 1 || receipt.MutationAttempts != 1 || receipt.MutationResponse != "accepted" {
					t.Fatalf("writes=%d receipt=%+v", writes, receipt)
				}
			})
		}
	}
}

func TestIssueStateProviderExistingDescription(t *testing.T) {
	for _, action := range []string{"close", "reopen"} {
		for _, test := range []struct {
			name, description string
			unsafe            bool
		}{
			{"ordinary", "keep\nordinary text", false},
			{"empty", "", false},
			{"imported-command", "keep\n/label ~bug", true},
			{"trailing-newline", "keep\n", true},
			{"carriage-return", "ke\rep", true},
		} {
			t.Run(action+"/"+test.name, func(t *testing.T) {
				state := "opened"
				if action == "reopen" {
					state = "closed"
				}
				provider := &issueWriteProviderFixture{state: state, description: test.description}
				server := testgitlab.New(provider)
				defer server.Close()
				token := strings.Join([]string{"synthetic", "provider", "state"}, "-")
				out, stderr, deps := issueNativeDeps(t, server, token, &issueNativeKeyring{})
				code := Run(context.Background(), issueNativeArgs(t, action), deps)
				assertIssueNativePrivate(t, out.Bytes(), stderr.Bytes(), server.Requests(), token)
				provider.mu.Lock()
				defer provider.mu.Unlock()
				if provider.description != test.description {
					t.Fatalf("state-only request rewrote description: %q -> %q; exit=%d output=%s", test.description, provider.description, code, out)
				}
				if test.unsafe {
					_, errCode, _ := decodeIssueWriteEnvelope(t, out.Bytes())
					if errCode != uxv1.CodeSecurityBoundary || provider.writes != 0 {
						t.Fatalf("unsafe description reached mutation: writes=%d output=%s", provider.writes, out)
					}
				} else if code != 0 || provider.writes != 1 {
					t.Fatalf("ordinary state operation failed: exit=%d output=%s", code, out)
				}
			})
		}
	}
}

func TestIssueWriteNormalizationRetainsQuickActionDenials(t *testing.T) {
	for _, action := range []string{"create", "comment", "note"} {
		for _, body := range []string{"hello\n/label ~bug\n", "\r/la\rbel ~bug\r\n", "```\r\n/label ~bug\r\n```\r\n"} {
			t.Run(action+"/"+body, func(t *testing.T) {
				provider := &issueWriteProviderFixture{state: "opened"}
				server := testgitlab.New(provider)
				defer server.Close()
				keyring := &issueNativeKeyring{token: strings.Join([]string{"synthetic", "unresolved", "credential"}, "-")}
				out, _, deps := issueNativeDeps(t, server, "", keyring)
				args := issueNativeArgs(t, action)
				setIssueWriteBodyFile(t, args, body)
				code := Run(context.Background(), args, deps)
				_, errCode, _ := decodeIssueWriteEnvelope(t, out.Bytes())
				if code == 0 || errCode != uxv1.CodeSecurityBoundary || keyring.reads() != 0 || len(server.Requests()) != 0 {
					t.Fatalf("command-like input was not denied before authentication: exit=%d output=%s", code, out)
				}
			})
		}
	}
}

func TestIssueStateContentEvidence(t *testing.T) {
	for _, phase := range []string{"preflight", "mutation", "readback"} {
		for _, field := range []string{"title", "description"} {
			for _, missing := range []bool{false, true} {
				t.Run(phase+"/"+field+"/missing="+strconv.FormatBool(missing), func(t *testing.T) {
					d := issueWriteDelegate("close")
					op, index := issueWriteViewOperation, 1
					wantWrites, wantCode := 0, uxv1.CodeConflict
					if missing {
						wantCode = uxv1.CodeUpstream
					}
					if phase == "mutation" {
						op, index, wantWrites, wantCode = issueStateOperation, 0, 1, uxv1.CodeAmbiguousUpdate
					} else if phase == "readback" {
						index, wantWrites, wantCode = 2, 1, uxv1.CodeConflict
					}
					var record map[string]any
					if err := json.Unmarshal(d.responses[op][index].Body, &record); err != nil {
						t.Fatal(err)
					}
					if missing {
						delete(record, field)
					} else {
						record[field] = "different content"
					}
					body, err := json.Marshal(record)
					if err != nil {
						t.Fatal(err)
					}
					d.responses[op][index].Body = body
					out, _, deps := issueWriteTestDeps(t, d)
					code := Run(context.Background(), issueWriteArgs(t, "close"), deps)
					ok, errCode, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
					if code == 0 || ok || errCode != wantCode || len(d.inputBodies) != wantWrites {
						t.Fatalf("unproven content accepted: writes=%d output=%s", len(d.inputBodies), out)
					}
					if wantWrites == 1 && (receipt.MutationAttempts != 1 || receipt.ObservedState != "closed" || receipt.AtomicPrecondition || receipt.RetrySafe) {
						t.Fatalf("receipt=%+v", receipt)
					}
				})
			}
		}
	}
}

func TestIssueStateUnsafeDescriptionNoop(t *testing.T) {
	d := issueWriteDelegate("close")
	for i := range d.responses[issueWriteViewOperation] {
		var record map[string]any
		if err := json.Unmarshal(issueWriteBody("closed"), &record); err != nil {
			t.Fatal(err)
		}
		record["description"] = "keep\n/label ~bug"
		body, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		d.responses[issueWriteViewOperation][i] = glab.Response{Body: body}
	}
	args := replaceIssueWriteArg(issueWriteArgs(t, "close"), "--expected-state", "closed")
	out, _, deps := issueWriteTestDeps(t, d)
	code := Run(context.Background(), args, deps)
	_, _, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
	if code != 0 || len(d.inputBodies) != 0 || receipt.Outcome != "unchanged" || receipt.MutationAttempts != 0 {
		t.Fatalf("read-only no-op failed: exit=%d output=%s", code, out)
	}
}

func TestIssueWriteProviderUnresolvedCollateralCharacterization(t *testing.T) {
	for _, test := range []struct {
		name, body, template string
		wantCode, wantLabels int
	}{
		{"blank-absent-template", "", "", 0, 0},
		{"blank-ordinary-template", "", "template body", 6, 0},
		{"blank-quick-action-template", "", "/label ~bug", 0, 1},
		{"explicit-quick-action-template", "ordinary body", "/label ~bug", 0, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &issueWriteProviderFixture{template: test.template}
			server := testgitlab.New(provider)
			defer server.Close()
			token := strings.Join([]string{"synthetic", "provider", "template"}, "-")
			out, _, deps := issueNativeDeps(t, server, token, &issueNativeKeyring{})
			args := issueNativeArgs(t, "create")
			setIssueWriteBodyFile(t, args, test.body)
			code := Run(context.Background(), args, deps)
			provider.mu.Lock()
			defer provider.mu.Unlock()
			if code != test.wantCode || provider.writes != 1 || len(provider.labels) != test.wantLabels {
				t.Fatalf("provider characterization changed: exit=%d labels=%v writes=%d output=%s", code, provider.labels, provider.writes, out)
			}
			t.Logf("R2 characterization: exit=%d writes=%d description=%q labels=%v", code, provider.writes, provider.description, provider.labels)
		})
	}
	t.Run("state-description-race", func(t *testing.T) {
		provider := &issueWriteProviderFixture{state: "opened", description: "keep", raceDescription: "keep\n/label ~bug"}
		server := testgitlab.New(provider)
		defer server.Close()
		token := strings.Join([]string{"synthetic", "provider", "race"}, "-")
		out, _, deps := issueNativeDeps(t, server, token, &issueNativeKeyring{})
		code := Run(context.Background(), issueNativeArgs(t, "close"), deps)
		provider.mu.Lock()
		defer provider.mu.Unlock()
		_, _, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
		if code != 0 || provider.writes != 1 || provider.description != "keep" || receipt.Outcome != "state_observed" || receipt.AtomicPrecondition {
			t.Fatalf("provider race characterization changed: exit=%d writes=%d description=%q output=%s", code, provider.writes, provider.description, out)
		}
		t.Log("R1 unresolved: provider strips concurrently inserted command text; matching snapshots cannot prove absence of collateral edits")
	})
}
