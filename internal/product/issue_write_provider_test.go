package product

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/testgitlab"
)

type issueWriteProviderFixture struct {
	mu              sync.Mutex
	state           string
	description     string
	template        string
	raceDescription string
	issueReads      int
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
		f.issueReads++
	case "POST /projects/101/issues", "POST /projects/101/issues/42/notes", "PUT /projects/101/issues/42":
		var payload map[string]string
		if json.NewDecoder(r.Body).Decode(&payload) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.writes++
		if r.Method == http.MethodPut {
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
			record["title"] = strings.Trim(payload["title"], " \t\n\v\f\r\x00")
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
	if r.Method == http.MethodGet && f.issueReads == 2 && f.raceDescription != "" {
		f.description = f.raceDescription
	}
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
