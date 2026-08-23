package config

import (
	"os"
	"path/filepath"
	"testing"

	v1 "gl-axi/internal/contract/v1"
)

func TestConfigRoundTripContainsAuthorityOnly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.json")
	cfg := New()
	if err := cfg.Put("gitlab.example.invalid", Host{
		GitHosts: []string{"ssh.example.invalid", "gitlab.example.invalid"},
		APIBase:  "https://api.example.invalid/gitlab/api/v4",
		WebBase:  "https://web.example.invalid/gitlab",
	}); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, cfg); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := loaded.Resolve("ssh.example.invalid")
	if err != nil || resolved.Name != "gitlab.example.invalid" || !resolved.Explicit {
		t.Fatalf("resolved=%+v err=%v", resolved, err)
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%o", info.Mode().Perm())
	}
}

func TestConfigPathSupportsCanonicalAndCompatibilityEnvironment(t *testing.T) {
	for _, test := range []struct {
		name      string
		canonical string
		legacy    string
		want      string
		wantExit  int
	}{
		{name: "canonical", canonical: filepath.Join(t.TempDir(), "canonical.json"), want: "canonical"},
		{name: "compatibility", legacy: filepath.Join(t.TempDir(), "legacy.json"), want: "legacy"},
		{name: "matching aliases", canonical: filepath.Join(t.TempDir(), "shared.json"), want: "shared"},
		{name: "disagreeing aliases", canonical: filepath.Join(t.TempDir(), "one.json"), legacy: filepath.Join(t.TempDir(), "two.json"), wantExit: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("GL_AXI_CONFIG_DIR", "")
			t.Setenv("GLAB_AXI_CONFIG_DIR", "")
			if test.want == "shared" {
				test.legacy = test.canonical
			}
			t.Setenv("GL_AXI_CONFIG", test.canonical)
			t.Setenv("GLAB_AXI_CONFIG", test.legacy)
			path, err := Path()
			if test.wantExit != 0 {
				if v1.ExitCode(err) != test.wantExit {
					t.Fatalf("path=%q error=%v", path, err)
				}
				return
			}
			want := test.canonical
			if want == "" {
				want = test.legacy
			}
			if err != nil || path != filepath.Clean(want) {
				t.Fatalf("path=%q want=%q error=%v", path, want, err)
			}
		})
	}
}

func TestConfigRejectsSymlinkAndBroadMode(t *testing.T) {
	dir := t.TempDir()
	real := filepath.Join(dir, "real.json")
	if err := os.WriteFile(real, []byte(`{"schema":"glab-axi/config-v1","hosts":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(link); v1.ExitCode(err) != 9 {
		t.Fatal("symlink config was not refused")
	}
	if err := os.Chmod(real, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(real); v1.ExitCode(err) != 9 {
		t.Fatal("broad config permissions were not refused")
	}
}

func TestConfigRejectsAmbiguousGitHostMapping(t *testing.T) {
	cfg := New()
	if err := cfg.Put("one.example.invalid", Host{GitHosts: []string{"shared.example.invalid"}, APIBase: "https://one.example.invalid/api/v4", WebBase: "https://one.example.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Put("two.example.invalid", Host{GitHosts: []string{"shared.example.invalid"}, APIBase: "https://two.example.invalid/api/v4", WebBase: "https://two.example.invalid"}); v1.ExitCode(err) != 6 {
		t.Fatalf("ambiguous mapping exit=%d", v1.ExitCode(err))
	}
	if _, exists := cfg.Hosts["two.example.invalid"]; exists {
		t.Fatal("failed Put left an invalid mapping behind")
	}
}

func TestPrivateHostRequiresExplicitMapping(t *testing.T) {
	cfg := New()
	if _, err := cfg.Resolve("gitlab.private.invalid"); v1.ExitCode(err) != 9 {
		t.Fatalf("private host exit=%d", v1.ExitCode(err))
	}
	resolved, err := cfg.Resolve("gitlab.com")
	if err != nil || resolved.Explicit || resolved.Authority.API.String() != "https://gitlab.com/api/v4" {
		t.Fatalf("built-in mapping=%+v err=%v", resolved, err)
	}
}
