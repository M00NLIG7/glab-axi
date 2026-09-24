package product

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

func TestRepoAdminForkPollingPreservesValidatedReceipt(t *testing.T) {
	for _, scenario := range []string{"visibility", "description", "read-error", "invalid-document", "invalid-settings", "project-id", "source-id", "missing-source"} {
		t.Run(scenario, func(t *testing.T) {
			state := adminTestForkState()
			state.after.ImportStatus = "started"
			last := state.after.adminProject
			delegate := state.delegate()
			base := delegate.doFunc
			observations := 0
			delegate.doFunc = func(ctx context.Context, request glab.Request) (glab.Response, error, bool) {
				response, err, handled := base(ctx, request)
				if request.Operation != adminTestOpProject || state.writes == 0 || request.Repo != state.after.Path {
					return response, err, handled
				}
				observations++
				if observations != 2 {
					return response, err, handled
				}
				next := state.after
				next.ImportStatus = "finished"
				next.UpdatedAt = "2026-09-01T12:01:00Z"
				switch scenario {
				case "visibility":
					next.Visibility = "public"
				case "description":
					next.Description = "concurrent edit"
				case "read-error":
					return glab.Response{}, errors.New("poll unavailable"), true
				case "invalid-document":
					response.Body = []byte(`{}`)
					return response, nil, true
				case "invalid-settings":
					next.Visibility = "unknown"
				case "project-id":
					next.ID++
				case "source-id":
					from := *next.ForkedFrom
					from.ID++
					next.ForkedFrom = &from
				case "missing-source":
					next.ForkedFrom = nil
				}
				response.Body = adminTestBody(next)
				return response, nil, true
			}
			stdout, _, deps, closeFixture := adminTestNativeDeps(t, delegate)
			code := adminTestRun(context.Background(), append(adminTestArgs("fork"), "--wait-seconds", "3"), deps, closeFixture)
			var envelope struct {
				Meta  uxv1.Meta `json:"meta"`
				Error struct {
					Code      uxv1.Code   `json:"code"`
					Retryable bool        `json:"retryable"`
					Receipt   adminOutput `json:"receipt"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			receipt := envelope.Error.Receipt.Administration
			if code != 6 || envelope.Error.Code != uxv1.CodeAmbiguousCreate || envelope.Error.Retryable || envelope.Meta.Complete || state.writes != 1 || observations != 2 || !receipt.MutationAttempted || receipt.Outcome != "ambiguous" || receipt.ImportStatus != "started" || receipt.Project == nil || *receipt.Project != last {
				t.Fatalf("exit=%d writes=%d observations=%d output=%s", code, state.writes, observations, stdout)
			}
		})
	}
}

func TestRepoAdminForkPollingRequestDeadlinesAndCancellation(t *testing.T) {
	for _, scenario := range []string{"requested-wait", "caller-deadline", "caller-cancel"} {
		t.Run(scenario, func(t *testing.T) {
			state := adminTestForkState()
			state.after.ImportStatus = "started"
			last := state.after.adminProject
			delegate := state.delegate()
			base := delegate.doFunc
			var ctx context.Context
			var cancel context.CancelFunc
			observations := 0
			pollCanceled := false
			delegate.doFunc = func(requestCtx context.Context, request glab.Request) (glab.Response, error, bool) {
				response, err, handled := base(requestCtx, request)
				if request.Operation != adminTestOpProject || state.writes == 0 || request.Repo != state.after.Path {
					return response, err, handled
				}
				observations++
				if observations != 2 {
					return response, err, handled
				}
				if scenario == "caller-cancel" {
					cancel()
				}
				timer := time.NewTimer(4 * time.Second)
				defer timer.Stop()
				select {
				case <-requestCtx.Done():
					pollCanceled = true
					return glab.Response{}, requestCtx.Err(), true
				case <-timer.C:
					next := state.after
					next.ImportStatus = "finished"
					response.Body = adminTestBody(next)
					return response, nil, true
				}
			}
			stdout, _, deps, closeFixture := adminTestNativeDeps(t, delegate)
			wait := "20"
			if scenario == "requested-wait" {
				wait = "2"
			}
			if scenario == "caller-deadline" {
				ctx, cancel = context.WithTimeout(context.Background(), 1500*time.Millisecond)
			} else {
				ctx, cancel = context.WithCancel(context.Background())
			}
			defer cancel()
			started := time.Now()
			code := adminTestRun(ctx, append(adminTestArgs("fork"), "--wait-seconds", wait), deps, closeFixture)
			elapsed := time.Since(started)
			var envelope struct {
				Meta  uxv1.Meta   `json:"meta"`
				Data  adminOutput `json:"data"`
				Error struct {
					Code    uxv1.Code   `json:"code"`
					Receipt adminOutput `json:"receipt"`
				} `json:"error"`
			}
			if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
				t.Fatal(err)
			}
			receipt := envelope.Data.Administration
			if scenario == "caller-cancel" {
				receipt = envelope.Error.Receipt.Administration
				if code == 0 || envelope.Error.Code != uxv1.CodeCanceled {
					t.Fatalf("exit=%d output=%s", code, stdout)
				}
			} else if code != 0 || envelope.Meta.Reason != "fork_observation_deadline" {
				t.Fatalf("exit=%d output=%s", code, stdout)
			}
			if elapsed > 4*time.Second || !pollCanceled || observations != 2 || state.writes != 1 || envelope.Meta.Complete || !receipt.MutationAttempted || receipt.Outcome != "in_progress" || receipt.ImportStatus != "started" || receipt.Project == nil || *receipt.Project != last {
				t.Fatalf("elapsed=%s canceled=%t observations=%d writes=%d output=%s", elapsed, pollCanceled, observations, state.writes, stdout)
			}
		})
	}
}

func TestRepoAdminDescriptionFileScope(t *testing.T) {
	for _, action := range []string{"create", "edit", "fork"} {
		t.Run(action, func(t *testing.T) {
			before := adminTestProject("team/sub/project", 101)
			state := &adminTestState{after: before}
			args := adminTestArgs(action)
			if action == "edit" {
				state.before = before
				args = adminEditArgs(t, before)
			} else if action == "fork" {
				state = adminTestForkState()
			}
			state.after.Description = "new description"
			args = append(args, "--description-file", adminTestFile(t, []byte(state.after.Description)))
			code, receipt, delegate, out := runAdminTest(t, state, args)
			if action == "fork" {
				var envelope struct {
					Error uxv1.Error `json:"error"`
				}
				if err := json.Unmarshal([]byte(out), &envelope); err != nil {
					t.Fatal(err)
				}
				if code != 2 || envelope.Error.Code != uxv1.CodeUnsupported || len(delegate.requests) != 0 || state.writes != 0 {
					t.Fatalf("exit=%d requests=%d output=%s", code, len(delegate.requests), out)
				}
				return
			}
			if code != 0 || state.writes != 1 || receipt.Outcome != "completed" || receipt.Project == nil || receipt.Project.Description != state.after.Description {
				t.Fatalf("exit=%d writes=%d output=%s", code, state.writes, out)
			}
			var payload map[string]any
			if err := json.Unmarshal(delegate.inputBodies[0], &payload); err != nil {
				t.Fatal(err)
			}
			want := map[string]any{"description": state.after.Description}
			if action == "create" {
				want["namespace_id"], want["name"], want["path"] = float64(21), "project", "project"
				want["visibility"], want["initialize_with_readme"] = "private", false
			}
			if !reflect.DeepEqual(payload, want) {
				t.Fatalf("payload=%v want=%v", payload, want)
			}
		})
	}
}
