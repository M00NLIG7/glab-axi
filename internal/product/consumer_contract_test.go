package product

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
)

type firstmateConsumerContract struct {
	Schema string `json:"schema"`
	Source struct {
		Project string `json:"project"`
		Commit  string `json:"commit"`
	} `json:"source"`
	Provider string `json:"provider"`
	GlabAXI  struct {
		MinimumVersion          string `json:"minimum_version"`
		RequiredEnvelope        string `json:"required_envelope"`
		RequiredBackend         string `json:"required_backend"`
		RequiredUpstreamVersion string `json:"required_upstream_version"`
	} `json:"glab_axi"`
	PlannedInvocation struct {
		Argv                   []string `json:"argv"`
		RequiredExplicitInputs []string `json:"required_explicit_inputs"`
		ForbiddenFlags         []string `json:"forbidden_flags"`
	} `json:"planned_invocation"`
	SuccessContract struct {
		EnvelopeSchema       string   `json:"envelope_schema"`
		DataSchema           string   `json:"data_schema"`
		Actions              []string `json:"actions"`
		RequiredJSONPointers []string `json:"required_json_pointers"`
	} `json:"success_contract"`
	ExitContract map[string]int `json:"exit_contract"`
	Custody      struct {
		IntegrationStatus string `json:"firstmate_integration_status"`
	} `json:"custody"`
}

func TestPinnedFirstmateConsumerContractDrivesExactMergeGrammar(t *testing.T) {
	path := filepath.Join("..", "..", "contracts", "firstmate", "fb368dcc6380bfba5b4ba35722106692f3e789b3.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var contract firstmateConsumerContract
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Schema != "glab-axi/firstmate-consumer-contract/v1" || contract.Source.Project != "Firstmate" || contract.Source.Commit != "fb368dcc6380bfba5b4ba35722106692f3e789b3" || contract.Provider != "gitlab" || contract.GlabAXI.MinimumVersion != "0.2.0" || contract.GlabAXI.RequiredEnvelope != uxv1.Schema || contract.GlabAXI.RequiredBackend != "official-glab" || contract.GlabAXI.RequiredUpstreamVersion != glab.SupportedVersion {
		t.Fatalf("unexpected consumer identity: %#v", contract)
	}
	if contract.Custody.IntegrationStatus != "not implemented by this contract stage" {
		t.Fatalf("consumer fixture overstates Firstmate integration: %q", contract.Custody.IntegrationStatus)
	}

	replacements := map[string]string{
		"{iid}":                                  "42",
		"{nested_project}":                       "group/subgroup/project",
		"{host}":                                 "gitlab.example.invalid",
		"{canonical_mr_url}":                     "https://gitlab.example.invalid/group/subgroup/project/-/merge_requests/42",
		"{recorded_source_branch}":               mergeTestSource,
		"{recorded_target_branch}":               mergeTestTarget,
		"{recorded_head_sha}":                    mergeTestHead,
		"{captain-explicit|standing-yolo-green}": "captain-explicit",
	}
	argv := make([]string, len(contract.PlannedInvocation.Argv))
	for index, argument := range contract.PlannedInvocation.Argv {
		argv[index] = argument
		for placeholder, value := range replacements {
			argv[index] = strings.ReplaceAll(argv[index], placeholder, value)
		}
	}
	result, err := Parse(argv)
	if err != nil || result.Command == nil {
		t.Fatalf("pinned invocation did not parse: argv=%v result=%#v error=%v", argv, result, err)
	}
	parsed := result.Command
	if strings.Join(parsed.Definition.Path, " ") != "mr merge" || parsed.Positionals[0] != "42" || parsed.Values["--repo"] != "group/subgroup/project" || parsed.Values["--hostname"] != "gitlab.example.invalid" || parsed.Values["--expected-source"] != mergeTestSource || parsed.Values["--expected-target"] != mergeTestTarget || parsed.Values["--expected-head"] != mergeTestHead || parsed.Values["--authority"] != "captain-explicit" || !parsed.Booleans["--squash"] || string(parsed.Format) != "json" {
		t.Fatalf("pinned invocation changed meaning: %#v", parsed)
	}
	for _, flag := range contract.PlannedInvocation.ForbiddenFlags {
		if _, err := Parse(append(append([]string(nil), argv...), flag)); err == nil || uxv1.AsError(err).Code != uxv1.CodeSecurityBoundary {
			t.Fatalf("pinned forbidden flag %q was not a security denial: %v", flag, err)
		}
	}

	wantInputs := []string{"iid", "nested_project", "host", "canonical_mr_url", "recorded_source_branch", "recorded_target_branch", "recorded_head_sha", "authority", "squash"}
	wantActions := []string{"merged", "already_merged", "reconciled_merged"}
	if !reflect.DeepEqual(contract.PlannedInvocation.RequiredExplicitInputs, wantInputs) || contract.SuccessContract.EnvelopeSchema != uxv1.Schema || contract.SuccessContract.DataSchema != "schema/ux-v1/mr-merge.schema.json" || !reflect.DeepEqual(contract.SuccessContract.Actions, wantActions) || contract.ExitContract["conflict_or_ambiguous_merge"] != 6 {
		t.Fatalf("consumer contract fields changed: %#v", contract)
	}
	if len(contract.SuccessContract.RequiredJSONPointers) != 19 {
		t.Fatalf("required output identity is incomplete: %v", contract.SuccessContract.RequiredJSONPointers)
	}
}
