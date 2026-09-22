package glab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestIssueEditProviderContractPinsNonAtomicSingleAttempt(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "..", "contracts", "official-glab", "v1.112.0", "issue-edit-provider.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema   string `json:"schema"`
		Official struct {
			Version string `json:"version"`
			Commit  string `json:"commit"`
			Client  string `json:"client_go"`
		} `json:"official_glab"`
		Sources []struct {
			URL      string `json:"url"`
			Evidence string `json:"evidence"`
		} `json:"provider_sources"`
		Request struct {
			Method        string   `json:"method"`
			Path          string   `json:"path"`
			Fields        []string `json:"body_fields"`
			Encoding      string   `json:"label_encoding"`
			NoReplacement bool     `json:"no_replacement_labels"`
			NoTimestamp   bool     `json:"no_updated_at_setter"`
			Attempts      int      `json:"attempts_maximum"`
		} `json:"request"`
		Concurrency struct {
			Atomic   bool   `json:"atomic_revision_check"`
			Labels   bool   `json:"numeric_label_precondition"`
			Contract string `json:"contract"`
			Race     string `json:"race"`
		} `json:"concurrency"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "glab-axi/issue-edit-provider-evidence/v1" || fixture.Official.Version != SupportedVersion || fixture.Official.Commit != "816e3a52411aba73d90237859fdc6ecbc86bd169" || fixture.Official.Client != "v2.53.0" || len(fixture.Sources) != 5 {
		t.Fatalf("provider evidence=%#v", fixture)
	}
	if fixture.Request.Method != "PUT" || fixture.Request.Path != "projects/{validated_numeric_project_id}/issues/{iid}" || !reflect.DeepEqual(fixture.Request.Fields, []string{"title", "description", "add_labels", "remove_labels"}) || fixture.Request.Encoding != "comma-separated exact names" || !fixture.Request.NoReplacement || !fixture.Request.NoTimestamp || fixture.Request.Attempts != 1 || fixture.Concurrency.Atomic || fixture.Concurrency.Labels || fixture.Concurrency.Contract != "best_effort" || fixture.Concurrency.Race == "" {
		t.Fatalf("provider semantics=%#v", fixture)
	}
	if _, err := build(Request{Operation: OpIssueEditUpdate, Host: "gitlab.com", Repo: "group/project", ID: 101, IID: 42, InputFile: filepath.Join(t.TempDir(), "request.json")}); err == nil {
		t.Fatal("official-glab builder exposed the native-only issue mutation")
	}
}
