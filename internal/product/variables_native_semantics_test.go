package product

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"
)

func TestNativeVariableTypesAndExactPrestate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation ACL boundary remains unavailable")
	}
	for _, tc := range []struct {
		name, action, class, mode string
		file, rawFalse            bool
		want                      int
	}{
		{name: "create-file", action: "set", class: "absent", file: true},
		{name: "rotate-file", action: "set", class: "hidden", file: true},
		{name: "delete-file", action: "delete", class: "hidden", file: true},
		{name: "delete-masked", action: "delete", class: "masked"},
		{name: "delete-protected", action: "delete", class: "protected"},
		{name: "disable-expansion", action: "set", class: "hidden", rawFalse: true},
		{name: "wrong-project", action: "delete", class: "hidden", mode: "wrong-project", want: 9},
		{name: "wrong-host", action: "delete", class: "hidden", mode: "wrong-host", want: 9},
		{name: "old-version", action: "delete", class: "hidden", mode: "old-version", want: 2},
		{name: "value-drift", action: "delete", class: "hidden", mode: "drift", want: 6},
		{name: "wrong-poststate", action: "set", class: "hidden", mode: "wrong-poststate", want: 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newNativeVariableFixture(t, tc.class, tc.mode, false)
			args := f.args("secret", tc.action, tc.class, "json")
			if tc.class != "absent" {
				if tc.file {
					f.state[0]["variable_type"] = "file"
					args = replaceVariableArg(args, "--expected-type", "file")
				}
				if tc.rawFalse {
					f.state[0]["raw"] = false
					args = replaceVariableArg(args, "--expected-raw", "false")
				}
				if tc.class == "protected" {
					f.state[0]["protected"] = true
					args = replaceVariableArg(args, "--expected-protected", "true")
				}
			}
			if tc.action == "set" && tc.file {
				args = replaceVariableArg(args, "--type", "file")
			}
			stdout, stderr, deps, _, _ := f.deps(t, false)
			exit := Run(context.Background(), args, deps)
			f.assertConfidential(t, stdout.String(), stderr.String())
			if exit != tc.want {
				t.Fatalf("native prestate outcome=%d want=%d", exit, tc.want)
			}
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			wantWrites := 0
			if tc.want == 0 || tc.mode == "wrong-poststate" {
				wantWrites = 1
			}
			if writes != wantWrites {
				t.Fatal("incorrect mutation count")
			}
		})
	}
}

func TestNativeVariableScopesAreLiteral(t *testing.T) {
	for _, scope := range []string{"*", "review/*", "production"} {
		t.Run(scope, func(t *testing.T) {
			f := newNativeVariableFixture(t, "hidden", "", false)
			args := replaceVariableArg(f.args("secret", "list", "hidden", "json"), "--scope", scope)
			stdout, stderr, deps, _, _ := f.deps(t, false)
			if Run(context.Background(), args, deps) != 0 {
				t.Fatal("exact scope list failed")
			}
			f.assertConfidential(t, stdout.String(), stderr.String())
			var result struct {
				Data struct {
					Variables []struct {
						Scope string `json:"environment_scope"`
					} `json:"variables"`
				} `json:"data"`
			}
			if json.Unmarshal(stdout.Bytes(), &result) != nil || len(result.Data.Variables) != 1 || result.Data.Variables[0].Scope != scope {
				t.Fatal("literal scope became environment-pattern matching")
			}
		})
	}
}
