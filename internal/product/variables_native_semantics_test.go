package product

import (
	"context"
	"encoding/json"
	"runtime"
	"testing"

	"gl-axi/internal/contract/uxv1"
)

func TestNativeVariableVersionGate(t *testing.T) {
	for _, version := range []struct {
		value     string
		supported bool
	}{
		{"17.6.0", true}, {"17.6.0-ee", true}, {"17.6.1-pre", true},
		{"18.10.0-pre", true}, {"18.10.0", true},
		{"17.5.9", false}, {"17.5.9-ee", false}, {"17.5.9-pre", false},
		{"17.6.0-pre", false}, {"16.11.9", false},
		{"", false}, {"17.6", false}, {"18.10.0-unknown", false},
		{"999999999999999999999.0.0", false},
		{"18.999999999999999999999.0", false},
		{"18.10.999999999999999999999", false},
	} {
		for _, group := range []string{"secret", "variable"} {
			for _, action := range []string{"list", "set", "delete"} {
				t.Run(version.value+"/"+group+"/"+action, func(t *testing.T) {
					if runtime.GOOS == "windows" && action != "list" {
						t.Skip("private mutation ACL boundary remains unavailable")
					}
					class := "hidden"
					if group == "variable" {
						class = "ordinary"
					}
					f := newNativeVariableFixture(t, class, "", false)
					f.version = version.value
					stdout, stderr, deps, _, lookups := f.deps(t, false)
					exit := Run(context.Background(), f.args(group, action, class, "json"), deps)
					f.assertConfidential(t, stdout.String(), stderr.String())
					var envelope struct {
						OK    bool `json:"ok"`
						Error *struct {
							Code uxv1.Code `json:"code"`
						} `json:"error"`
					}
					if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
						t.Fatal("invalid version-gate response")
					}
					if version.supported {
						if exit != 0 || !envelope.OK || envelope.Error != nil {
							t.Fatalf("supported version failed with exit %d", exit)
						}
					} else if exit != 2 || envelope.OK || envelope.Error == nil || envelope.Error.Code != uxv1.CodeUnsupported {
						t.Fatalf("unsupported version returned exit %d", exit)
					}
					wantWrites, wantProjectReads, wantInventoryReads := 0, 0, 0
					if version.supported {
						wantProjectReads, wantInventoryReads = 2, 1
						if action != "list" {
							wantWrites, wantProjectReads, wantInventoryReads = 1, 3, 3
						}
					}
					f.mu.Lock()
					writes, projectReads, inventoryReads := f.writes, f.projectReads, f.inventoryReads
					f.mu.Unlock()
					if writes != wantWrites || projectReads != wantProjectReads || inventoryReads != wantInventoryReads || *lookups != 1 {
						t.Fatal("version gate bypassed preflight or changed the operation budget or identity")
					}
					requests := f.server.Requests()
					if len(requests) != 1+wantWrites+wantProjectReads+wantInventoryReads || requests[0].URL != "/api/v4/version" {
						t.Fatal("version was not checked exactly once before project access")
					}
				})
			}
		}
	}
}

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
		{name: "hidden-value-drift-is-unobservable", action: "delete", class: "hidden", mode: "drift"},
		{name: "readable-value-drift", action: "delete", class: "masked", mode: "drift", want: 6},
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
