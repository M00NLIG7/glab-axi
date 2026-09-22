package product

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

// Algorithm fixtures exercise reconciliation without constructing either
// credential backend. Native CLI/TLS tests separately prove public routing.
func runMRWriteAlgorithm(t *testing.T, ctx context.Context, client mrOperationClient, args []string) (*bytes.Buffer, int) {
	t.Helper()
	parsed, err := Parse(args)
	if err != nil {
		t.Fatal(err)
	}
	p := *parsed.Command
	target := Target{Host: p.Values["--hostname"], Repo: p.Values["--repo"]}
	meta := uxv1.Meta{Backend: "native", Host: target.Host, Repo: target.Repo, Complete: true}
	var result commandOutput
	if p.Definition.Path[1] == "ensure" {
		result, err = executeMREnsure(ctx, client, target, p, meta)
	} else {
		result, err = executeMRWrite(ctx, client, target, p, meta)
	}
	stdout := &bytes.Buffer{}
	envelope, code := uxv1.Success(result.data, result.meta), 0
	if err != nil {
		envelope, code = uxv1.Failure(err, result.meta), uxv1.ExitCode(err)
	}
	if err := json.NewEncoder(stdout).Encode(envelope); err != nil {
		t.Fatal(err)
	}
	return stdout, code
}

func TestMRWriteInvalidInputNoDependency(t *testing.T) {
	base := append(mrWriteArgs("close", "opened"), "--auth-source", "native")
	cases := [][]string{
		removeFlag(base, "--hostname", true), removeFlag(base, "--repo", true),
		replaceArg(base, "42", "042"), replaceArg(base, mergeTestHead, strings.Repeat("0", 40)),
		replaceArg(base, "feature", "feature..other"), replaceArg(base, "main", "feature"),
		replaceArg(base, mrWriteTestURL, mrWriteTestURL+"?state=closed"), replaceArg(base, mrWriteTestURL, mrWriteTestURL+"#note_1"),
		replaceArg(base, "opened", "merged"), appendCopy(base, "--expected-head", mergeTestHead),
		appendCopy(base, "--state-event", "merge"), appendCopy(base, "--body", "arbitrary"),
		appendCopy(base, "--limit", "10"), appendCopy(base, "--approve"),
	}
	for _, args := range cases {
		var stdout bytes.Buffer
		deps := Dependencies{Runtime: productRuntimeNoDiscovery(t, &stdout), NewDelegate: func() delegateClient { t.Fatal("invalid input constructed delegate"); return nil }}
		if code := Run(context.Background(), args, deps); code == 0 {
			t.Fatalf("accepted %v: %s", args, stdout.String())
		}
	}
	for _, body := range []string{"", " ", "/merge", "ordinary\n  /close", "```\n/approve\n```", "ordinary\n\t/label label", ":thumbsup:", "👍", "hello\r/merge", "hi\x00there", "hello\u200b", strings.Repeat("a", limits.MaxDescriptionBytes+1)} {
		t.Run("body "+string([]rune(body)[:min(12, len([]rune(body)))]), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "body")
			if err := os.WriteFile(path, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			args := append(mrWriteArgs("comment", "opened"), "--auth-source", "native", "--body-file", path)
			var stdout bytes.Buffer
			deps := Dependencies{Runtime: productRuntimeNoDiscovery(t, &stdout), NewDelegate: func() delegateClient { t.Fatal("invalid note constructed delegate"); return nil }}
			if code := Run(context.Background(), args, deps); code == 0 {
				t.Fatalf("accepted invalid body: %s", stdout.String())
			}
		})
	}
	permissionModes := []os.FileMode{0644, 0666}
	if runtime.GOOS == "windows" {
		permissionModes = nil
	} // POSIX permission bits are not meaningful there.
	for _, mode := range permissionModes {
		path := filepath.Join(t.TempDir(), "body")
		if err := os.WriteFile(path, []byte("ordinary note"), mode); err != nil {
			t.Fatal(err)
		}
		if _, err := Parse(append(mrWriteArgs("note", "closed"), "--auth-source", "native", "--body-file", path)); err == nil {
			t.Fatal("accepted public body file")
		}
	}
	path := filepath.Join(t.TempDir(), "body")
	if err := os.WriteFile(path, []byte("ordinary note"), 0600); err != nil {
		t.Fatal(err)
	}
	link := path + "-link"
	if err := os.Symlink(path, link); err == nil {
		if _, err := Parse(append(mrWriteArgs("note", "closed"), "--auth-source", "native", "--body-file", link)); err == nil {
			t.Fatal("accepted symlink body")
		}
	}
}

func mrWriteDelegate(t *testing.T) *fakeDelegate {
	t.Helper()
	encode := func(value any) glab.Response {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return glab.Response{Body: data, UpstreamVersion: glab.SupportedVersion}
	}
	return &fakeDelegate{responses: map[glab.Operation][]glab.Response{
		glab.OpMRDiscussionsTargetProject: {encode(map[string]any{"id": 101, "path_with_namespace": "group/project", "web_url": "https://" + mrWriteTestHost + "/group/project"})},
		glab.OpMRView:                     {encode(mrWriteRecord("opened")), encode(mrWriteRecord("opened")), encode(mrWriteRecord("closed"))},
	}}
}

func TestMRWriteBoundsCancellationAndPrivatePayload(t *testing.T) {
	for _, mode := range []string{"page", "canceled preflight", "canceled mutation", "deadline mutation", "normal"} {
		t.Run(mode, func(t *testing.T) {
			delegate := mrWriteDelegate(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			delegate.doFunc = func(callCtx context.Context, request glab.Request) (glab.Response, error, bool) {
				if request.Operation == glab.OpMRView && len(delegate.requests) == 3 && mode == "canceled preflight" {
					cancel()
				}
				if mode == "page" && request.Operation == glab.OpMRView {
					return glab.Response{Body: bytes.Repeat([]byte{' '}, limits.MaxJSONPageBytes+1)}, nil, true
				}
				if request.Operation == mrOpStateUpdate {
					deadline, ok := callCtx.Deadline()
					if !ok || time.Until(deadline) > limits.MergeMutationOperation {
						t.Fatal("unbounded mutation")
					}
					if mode == "canceled mutation" {
						cancel()
						return glab.Response{}, context.Canceled, true
					}
					if mode == "deadline mutation" {
						return glab.Response{}, context.DeadlineExceeded, true
					}
				}
				if callCtx.Err() != nil {
					return glab.Response{}, callCtx.Err(), true
				}
				return glab.Response{}, nil, false
			}
			stdout, code := runMRWriteAlgorithm(t, ctx, delegate, append(mrWriteArgs("close", "opened"), "--auth-source", "native"))
			writes := countOperation(delegate.requests, mrOpStateUpdate)
			switch mode {
			case "page", "canceled preflight":
				if code == 0 || writes != 0 {
					t.Fatalf("exit=%d writes=%d", code, writes)
				}
			case "canceled mutation":
				if code != 6 || writes != 1 || !strings.Contains(stdout.String(), `"outcome":"unknown"`) {
					t.Fatalf("exit=%d writes=%d output=%s", code, writes, stdout.String())
				}
			default:
				if code != 0 || writes != 1 {
					t.Fatalf("exit=%d writes=%d output=%s", code, writes, stdout.String())
				}
				assertPrivateEnsurePayload(t, delegate, map[string]any{"state_event": "close"})
			}
		})
	}
}

func TestMREnsureCreationMetadataNeverReplacesExisting(t *testing.T) {
	selected := func() upstreamMR {
		mr := ensureMR(11, "Draft: title", "body")
		mr.Draft = true
		mr.Assignees = []mrMetadataIdentity{{ID: 7}, {ID: 8}}
		mr.Reviewers = []mrMetadataIdentity{{ID: 9}}
		mr.Milestone = &mrMetadataIdentity{ID: 10}
		return mr
	}
	for _, mode := range []string{"create", "reconciled", "no-op", "assignee drift", "reviewer drift", "milestone drift", "draft drift", "content drift", "post drift", "competing creator"} {
		t.Run(mode, func(t *testing.T) {
			mr := selected()
			switch mode {
			case "assignee drift":
				mr.Assignees = []mrMetadataIdentity{{ID: 7}, {ID: 11}}
			case "reviewer drift", "post drift":
				mr.Reviewers = []mrMetadataIdentity{{ID: 11}}
			case "milestone drift":
				mr.Milestone.ID = 11
			case "draft drift":
				mr.Draft = false
			case "content drift":
				mr.Description = "unseen content"
			}
			delegate := ensureDelegate(t, []upstreamMR{mr})
			if mode == "create" || mode == "reconciled" || mode == "post drift" || mode == "competing creator" {
				canonical, err := json.Marshal(mr)
				if err != nil {
					t.Fatal(err)
				}
				matches, err := json.Marshal([]upstreamMR{mr})
				if err != nil {
					t.Fatal(err)
				}
				delegate.responses[glab.OpEnsureList] = []glab.Response{{Body: []byte("[]")}, {Body: []byte("[]")}, {Body: matches}}
				delegate.responses[glab.OpEnsureCreate] = []glab.Response{{Body: canonical}}
				if mode == "reconciled" {
					delegate.errors = map[glab.Operation][]error{glab.OpEnsureCreate: {uxv1.NewError(uxv1.CodeUpstream, "synthetic lost response")}}
				}
				if mode == "competing creator" {
					delegate.responses[glab.OpEnsureList][1].Body = matches
				}
			}
			args := append(ensureArgs(t, "title", "body"), "--draft", "--assignee-id", "8", "--assignee-id", "7", "--reviewer-id", "9", "--milestone-id", "10")
			args = append(args, "--auth-source", "native")
			stdout, code := runMRWriteAlgorithm(t, context.Background(), delegate, args)
			if countOperation(delegate.requests, glab.OpEnsureUpdate) != 0 {
				t.Fatal("metadata selection replaced an existing MR")
			}
			switch mode {
			case "create", "reconciled":
				if code != 0 || countOperation(delegate.requests, glab.OpEnsureCreate) != 1 {
					t.Fatalf("exit=%d output=%s", code, stdout.String())
				}
				assertPrivateEnsurePayload(t, delegate, map[string]any{"source_branch": "feature", "target_branch": "main", "title": "Draft: title", "description": "body", "assignee_ids": []any{float64(7), float64(8)}, "reviewer_ids": []any{float64(9)}, "milestone_id": float64(10)})
				if !strings.Contains(stdout.String(), `"creation":{"assignee_ids":[7,8],"reviewer_ids":[9],"milestone_id":10,"draft":true}`) {
					t.Fatalf("missing metadata receipt %s", stdout.String())
				}
			case "no-op", "competing creator":
				if code != 0 || countOperation(delegate.requests, glab.OpEnsureCreate) != 0 {
					t.Fatalf("exit=%d output=%s", code, stdout.String())
				}
			default:
				if code != 6 {
					t.Fatalf("exit=%d output=%s", code, stdout.String())
				}
			}
		})
	}
}

func TestMRCreationMetadataStrictInput(t *testing.T) {
	for _, flags := range [][]string{{"--assignee-id", "0"}, {"--reviewer-id", "01"}, {"--milestone-id", "-1"}, {"--assignee-id", "7", "--assignee-id", "7"}, {"--draft=true"}, {"--label", "bug"}, {"--assignee", "name"}, {"--reviewer-id", "999999999999999999999"}} {
		if _, err := Parse(append(append(ensureArgs(t, "title", "body"), "--auth-source", "native"), flags...)); err == nil {
			t.Fatalf("accepted %v", flags)
		}
	}
	args := append(ensureArgs(t, "title", "body"), "--auth-source", "native")
	for i := 1; i <= 21; i++ {
		args = append(args, "--assignee-id", strings.Repeat("1", i))
	}
	if _, err := Parse(args); err == nil {
		t.Fatal("accepted excessive identities")
	}
}
