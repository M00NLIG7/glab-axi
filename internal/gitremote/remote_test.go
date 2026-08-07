package gitremote

import "testing"

func TestParseGitLabRemotes(t *testing.T) {
	cases := map[string]Identity{
		"git@gitlab.example.invalid:group/project.git":                {Host: "gitlab.example.invalid", Project: "group/project"},
		"ssh://git@gitlab.example.invalid:2222/group/sub/project.git": {Host: "gitlab.example.invalid:2222", Project: "group/sub/project"},
		"https://gitlab.example.invalid:8443/group/project.git":       {Host: "gitlab.example.invalid:8443", Project: "group/project"},
	}
	for raw, want := range cases {
		got, err := Parse(raw)
		if err != nil || got != want {
			t.Fatalf("Parse(%q)=%+v, %v; want %+v", raw, got, err, want)
		}
	}
}

func TestParseRejectsCredentialedAndLocalRemotes(t *testing.T) {
	invalid := []string{
		"https://user@gitlab.example.invalid/group/project.git",
		"https://user:password@gitlab.example.invalid/group/project.git",
		"file:///tmp/group/project.git",
		"../group/project.git",
		"git@gitlab.example.invalid:../project.git",
		"git@gitlab.example.invalid:group/project.git?token=x",
	}
	for _, raw := range invalid {
		if _, err := Parse(raw); err == nil {
			t.Fatalf("unsafe remote accepted: %s", raw)
		}
	}
}
