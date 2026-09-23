package product

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gl-axi/internal/contract/uxv1"
)

func variableArgs(action, class string) []string {
	args := []string{"secret", action, "KEY", "-R", "group/project", "--hostname", "gitlab.com", "--scope", "production", "--expected-project-id", "101", "--expected-project-url", "https://gitlab.com/group/project", "--expected-class", class, "--confirm", "--format", "json"}
	if class != "absent" {
		args = append(args, "--expected-type", "env_var", "--expected-protected", "false", "--expected-raw", "true")
		if class != "hidden" {
			args = append(args, "--expected-value-file", "/private/previous")
		}
	}
	if action == "set" {
		args = append(args, "--value-file", "/private/new", "--type", "env_var", "--protected", "false")
	}
	return args
}
func replaceVariableArg(args []string, key, value string) []string {
	out := append([]string(nil), args...)
	for i := range out {
		if out[i] == key && i+1 < len(out) {
			out[i+1] = value
			return out
		}
	}
	return append(out, key, value)
}
func TestInvalidVariableInputsRefuseBeforeChildWork(t *testing.T) {
	base := append(variableArgs("set", "hidden"), "--auth-source", "native")
	tests := [][]string{
		replaceVariableArg(base, "--hostname", "https://wrong.example"), replaceVariableArg(base, "--repo", "../project"),
		replaceVariableArg(base, "--scope", "\n"), replaceVariableArg(base, "--expected-project-id", "0101"), replaceVariableArg(base, "--expected-project-url", "https://user:private@evil.example/group/project"),
		replaceVariableArg(base, "--expected-class", "masked"), replaceVariableArg(base, "--type", "file"), replaceVariableArg(base, "--protected", "true"), replaceVariableArg(base, "--expected-raw", "unknown"),
		append(append([]string(nil), base...), "--scope", "other"), append(append([]string(nil), base...), "--body", "private-argv-not-supported"), append(append([]string(nil), base...), "--org", "group"),
		replaceVariableArg(base, "--value-file", "relative"),
	}
	for i, args := range tests {
		stdout, stderr, deps := productTestDeps(t, nil)
		deps.Runtime.ConfigPath = filepath.Join(t.TempDir(), "config.json")
		deps.NewDelegate = func() delegateClient { t.Fatal("native variable input reached an official child"); return nil }
		if exit := Run(context.Background(), args, deps); exit == 0 {
			t.Fatalf("case %d succeeded", i)
		}
		if strings.Contains(stdout.String()+stderr.String(), "private-argv-not-supported") {
			t.Fatal("argv value disclosed")
		}
	}
}

func TestPrivateVariableFileAndStdinValidation(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "value")
	value := strings.Join([]string{"synthetic", "private", "value"}, "_")
	if err := os.WriteFile(file, []byte(value), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := readVariableInput(context.Background(), file, nil)
	if err != nil || got != value {
		t.Fatal("private file failed")
	}
	link := filepath.Join(dir, "link")
	if os.Symlink(file, link) == nil {
		if _, err = readVariableInput(context.Background(), link, nil); err == nil {
			t.Fatal("symlink accepted")
		}
	}
	got, err = readVariableInput(context.Background(), "-", strings.NewReader(value))
	if err != nil || got != value {
		t.Fatal("piped input failed")
	}
	reader, writer := io.Pipe()
	defer writer.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := readVariableInput(ctx, "-", reader); uxv1.AsError(err).Code != uxv1.CodeCanceled || time.Since(started) > time.Second {
		t.Fatal("stdin cancellation did not bound read")
	}
}

func TestVariableInventoryPaginationAndOperationBounds(t *testing.T) {
	for _, mode := range []string{"two-pages", "page-limit", "operation-limit", "duplicate", "canceled"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeVariableFixture(t, "hidden", mode, false)
			stdout, _, deps, _, _ := f.deps(t, false)
			args := append(f.args("secret", "list", "hidden", "json"), "--limit", "100")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled" {
				cancel()
			}
			exit := Run(ctx, args, deps)
			if mode == "two-pages" {
				if exit != 0 {
					t.Fatal("valid multi-page list failed")
				}
				var e struct {
					Meta uxv1.Meta `json:"meta"`
				}
				_ = json.Unmarshal(stdout.Bytes(), &e)
				if e.Meta.Complete || !e.Meta.Truncated || e.Meta.Count != 100 || e.Meta.Reason != "display_limit" {
					t.Fatal("display truncation misreported")
				}
			} else if exit == 0 {
				t.Fatal("unsafe inventory succeeded")
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			if writes != 0 {
				t.Fatal("inventory mutated")
			}
		})
	}
}

func TestVariableSchemasAreGenerated(t *testing.T) {
	for name, want := range VariableSchemas() {
		got, err := os.ReadFile(filepath.Join("..", "..", "schema", "ux-v1", name+".schema.json"))
		if err != nil || string(got) != want {
			t.Fatalf("schema %s is stale; run go run ./cmd/gen-product", name)
		}
	}
}
