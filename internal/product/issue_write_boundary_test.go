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
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"
	"gl-axi/internal/testgitlab"
)

func TestIssueWritesApprovedStateBoundary(t *testing.T) {
	for _, action := range []string{"close", "reopen"} {
		for _, noop := range []bool{false, true} {
			for _, description := range []string{"ordinary body", "", "keep\n/label ~bug", "```sh\n/usr/bin/env\n```", "keep\r\n"} {
				t.Run(action+"/noop="+strconv.FormatBool(noop)+"/"+description, func(t *testing.T) {
					state, desired := "opened", "closed"
					if action == "reopen" {
						state, desired = desired, state
					}
					if noop {
						state = desired
					}
					provider := &issueWriteProviderFixture{state: state, description: description}
					server := testgitlab.New(provider)
					defer server.Close()
					token := strings.Join([]string{"synthetic", "state", "refusal"}, "-")
					keyring := &issueNativeKeyring{token: token}
					out, stderr, deps := issueNativeDeps(t, server, "", keyring)
					args := replaceIssueWriteArg(issueNativeArgs(t, action), "--expected-state", state)
					code := Run(context.Background(), args, deps)
					requests := server.Requests()
					assertIssueNativePrivate(t, out.Bytes(), stderr.Bytes(), requests, token)
					ok, errCode, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
					if noop {
						if code != 0 || !ok || receipt.Outcome != "unchanged" {
							t.Fatalf("read-only no-op failed: exit=%d output=%s", code, out)
						}
					} else if code != 2 || ok || errCode != uxv1.CodeUnsupported || receipt.Outcome != "refused" {
						t.Fatalf("state transition was not refused: exit=%d output=%s", code, out)
					}
					if receipt.MutationAttempts != 0 || receipt.MutationResponse != "not_attempted" || receipt.Postcondition != "preflight" || receipt.ObservedState != state || receipt.RequestedState != desired || receipt.AtomicPrecondition || receipt.RetrySafe || len(receipt.RequestedSHA256) != 64 {
						t.Fatalf("untruthful state receipt: %+v", receipt)
					}
					if len(requests) != 4 || keyring.reads() != 1 {
						t.Fatalf("requests=%d credentials=%d", len(requests), keyring.reads())
					}
					for _, request := range requests {
						if request.Method != http.MethodGet {
							t.Fatalf("state operation dispatched %s", request.Method)
						}
					}
					provider.mu.Lock()
					defer provider.mu.Unlock()
					if provider.writes != 0 || provider.description != description || provider.state != state {
						t.Fatalf("state operation changed provider content: %+v", provider)
					}
				})
			}
		}
	}
	t.Run("concurrent-description", func(t *testing.T) {
		provider := &issueWriteProviderFixture{state: "opened", description: "keep", raceDescription: "keep\n/label ~bug"}
		server := testgitlab.New(provider)
		defer server.Close()
		token := strings.Join([]string{"synthetic", "state", "race"}, "-")
		out, _, deps := issueNativeDeps(t, server, token, &issueNativeKeyring{})
		code := Run(context.Background(), issueNativeArgs(t, "close"), deps)
		provider.mu.Lock()
		defer provider.mu.Unlock()
		_, errCode, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
		if code != 2 || errCode != uxv1.CodeUnsupported || provider.writes != 0 || provider.description != provider.raceDescription || provider.state != "opened" || receipt.MutationAttempts != 0 {
			t.Fatalf("concurrent content was not preserved: exit=%d description=%q output=%s", code, provider.description, out)
		}
	})
}

func TestIssueWritesApprovedBlankCreateBoundary(t *testing.T) {
	for _, template := range []string{"", "template body", "/label ~bug"} {
		for _, body := range []string{"", " \t\r\n\v\f", "\u00a0\u2003", "ordinary body"} {
			t.Run(template+"/"+body, func(t *testing.T) {
				provider := &issueWriteProviderFixture{template: template}
				server := testgitlab.New(provider)
				defer server.Close()
				token := strings.Join([]string{"synthetic", "template", "boundary"}, "-")
				keyring := &issueNativeKeyring{token: token}
				out, stderr, deps := issueNativeDeps(t, server, "", keyring)
				args := issueNativeArgs(t, "create")
				setIssueWriteBodyFile(t, args, body)
				code := Run(context.Background(), args, deps)
				requests := server.Requests()
				assertIssueNativePrivate(t, out.Bytes(), stderr.Bytes(), requests, token)
				provider.mu.Lock()
				defer provider.mu.Unlock()
				if body == "ordinary body" {
					if code != 0 || provider.writes != 1 || provider.description != body || keyring.reads() != 1 {
						t.Fatalf("nonblank create failed: exit=%d output=%s", code, out)
					}
				} else {
					_, errCode, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
					if code != 2 || errCode != uxv1.CodeUnsupported || len(requests) != 0 || keyring.reads() != 0 || provider.writes != 0 || receipt.MutationAttempts != 0 {
						t.Fatalf("blank create reached provider: exit=%d writes=%d output=%s", code, provider.writes, out)
					}
				}
				if len(provider.labels) != 0 {
					t.Fatalf("unrequested template effects: labels=%v", provider.labels)
				}
			})
		}
	}
}

func TestIssueCreateProviderTitleNormalization(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
		invalid           bool
	}{
		{"surrounding-ascii", " new title \t\n", "new title", false},
		{"ascii-controls", "\v\fnew title\t \v\f\r\n", "new title", false},
		{"internal-space", "new \t title\n", "new \t title", false},
		{"unicode-space", "\u00a0new title\u2003\n", "\u00a0new title\u2003", false},
		{"multiline", "new\ntitle", "", true},
		{"blank", " \t\n", "", true},
		{"original-bound", strings.Repeat(" ", limits.MaxTitleBytes) + "x", "", true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := &issueWriteProviderFixture{}
			server := testgitlab.New(provider)
			defer server.Close()
			token := strings.Join([]string{"synthetic", "title", "normalization"}, "-")
			keyring := &issueNativeKeyring{token: token}
			out, stderr, deps := issueNativeDeps(t, server, "", keyring)
			args := issueNativeArgs(t, "create")
			for i, arg := range args {
				if arg == "--title-file" {
					if err := os.WriteFile(args[i+1], []byte(test.input), 0600); err != nil {
						t.Fatal(err)
					}
				}
			}
			code := Run(context.Background(), args, deps)
			requests := server.Requests()
			assertIssueNativePrivate(t, out.Bytes(), stderr.Bytes(), requests, token)
			_, errCode, receipt := decodeIssueWriteEnvelope(t, out.Bytes())
			if test.invalid {
				if code != 2 || errCode != uxv1.CodeValidation || len(requests) != 0 || keyring.reads() != 0 {
					t.Fatalf("invalid title caused effects: exit=%d output=%s", code, out)
				}
				return
			}
			if code != 0 || len(requests) != 3 || receipt.Identity.IssueID != 1001 || receipt.Outcome != "created" || receipt.MutationAttempts != 1 {
				t.Fatalf("ordinary title failed: exit=%d output=%s", code, out)
			}
			var payload map[string]string
			if err := json.Unmarshal(requests[2].Body, &payload); err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(requests[2].Body)
			if payload["title"] != test.want || receipt.RequestedSHA256 != hex.EncodeToString(sum[:]) {
				t.Fatalf("title=%q receipt=%+v", payload["title"], receipt)
			}
		})
	}
}
