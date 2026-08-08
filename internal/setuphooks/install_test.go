package setuphooks

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInstallPreservesConfigAndIsIdempotent(t *testing.T) {
	home := t.TempDir()
	claude := filepath.Join(home, ".claude")
	codex := filepath.Join(home, ".codex")
	if err := os.MkdirAll(claude, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(codex, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(`{"theme":"dark","hooks":{"SessionStart":[{"matcher":"existing","hooks":[{"type":"command","command":"other-tool","timeout":5}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(codex, "config.toml"), []byte("model = \"safe\"\n\n[features]\nhooks = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	const skill = "---\nname: glab-axi\n---\nUse bounded reads.\n"
	result, err := Install(home, "glab-axi", skill)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != "installed" || len(result.Integrations) != 2 {
		t.Fatalf("result=%#v", result)
	}
	first, err := snapshot(home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Install(home, "glab-axi", skill); err != nil {
		t.Fatal(err)
	}
	second, err := snapshot(home)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("second setup changed managed files")
	}

	var settings map[string]any
	data, err := os.ReadFile(filepath.Join(claude, "settings.json"))
	if err != nil || json.Unmarshal(data, &settings) != nil {
		t.Fatalf("settings: %v", err)
	}
	if settings["theme"] != "dark" || !strings.Contains(string(data), "other-tool") || !strings.Contains(string(data), "glab-axi") {
		t.Fatalf("unrelated hook/config was lost: %s", data)
	}
	config, err := os.ReadFile(filepath.Join(codex, "config.toml"))
	if err != nil || !strings.Contains(string(config), `model = "safe"`) || !strings.Contains(string(config), "hooks = true") || strings.Contains(string(config), "hooks = false") {
		t.Fatalf("Codex config=%q err=%v", config, err)
	}
	for _, path := range []string{filepath.Join(home, ".agents", "skills", "glab-axi", "SKILL.md"), filepath.Join(home, ".claude", "skills", "glab-axi", "SKILL.md")} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != skill {
			t.Fatalf("skill %s=%q err=%v", path, data, err)
		}
	}
}

func TestInstallRefusesSymlinkTargetBeforeAnyWrite(t *testing.T) {
	home := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte(`{"unchanged":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(home, ".claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(home, ".claude", "settings.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Install(home, "glab-axi", "skill"); err == nil {
		t.Fatal("symlink target was accepted")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != `{"unchanged":true}` {
		t.Fatalf("outside changed: %q %v", data, err)
	}
	if _, err := os.Stat(filepath.Join(home, ".codex", "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("partial setup occurred: %v", err)
	}
}

func snapshot(home string) (string, error) {
	var out strings.Builder
	for _, relative := range []string{
		filepath.Join(".claude", "settings.json"),
		filepath.Join(".codex", "hooks.json"),
		filepath.Join(".codex", "config.toml"),
		filepath.Join(".agents", "skills", "glab-axi", "SKILL.md"),
		filepath.Join(".claude", "skills", "glab-axi", "SKILL.md"),
	} {
		data, err := os.ReadFile(filepath.Join(home, relative))
		if err != nil {
			return "", err
		}
		out.WriteString(relative)
		out.WriteByte('\x00')
		out.Write(data)
		out.WriteByte('\x00')
	}
	return out.String(), nil
}
