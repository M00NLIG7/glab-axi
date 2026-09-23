package product

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gl-axi/internal/civariable"
	"gl-axi/internal/contract/uxv1"
)

type variableFailWriter struct{}

func (variableFailWriter) Write([]byte) (int, error) {
	return 0, errors.New("synthetic writer failure")
}

func TestVariableMutationCancellationAndOutputPaths(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("private mutation permissions unavailable on Windows")
	}
	for _, mode := range []string{"canceled-write", "post-read-failure", "unchanged", "output-failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newNativeVariableFixture(t, "hidden", mode, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if mode == "canceled-write" {
				f.cancelOnWrite = cancel
			}
			if mode == "unchanged" {
				f.state[0]["hidden"] = false
				f.state[0]["masked"] = false
				f.state[0]["value"] = f.newValue
				if err := os.WriteFile(f.oldFile, []byte(f.newValue), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			stdout, stderr, deps, _, _ := f.deps(t, false)
			if mode == "output-failure" {
				deps.Runtime.Stdout = variableFailWriter{}
			}
			group, class := "secret", "hidden"
			if mode == "unchanged" {
				group, class = "variable", "ordinary"
			}
			exit := Run(ctx, f.args(group, "set", class, "json"), deps)
			f.assertConfidential(t, stdout.String(), stderr.String())
			f.mu.Lock()
			writes := f.writes
			f.mu.Unlock()
			switch mode {
			case "canceled-write", "post-read-failure":
				if exit != 6 || writes != 1 || !strings.Contains(stdout.String(), "ambiguous_variable") || !strings.Contains(stdout.String(), `"mutation_attempted":true`) {
					t.Fatal("uncertain mutation became retryable success")
				}
			case "unchanged":
				if exit != 0 || writes != 0 || !strings.Contains(stdout.String(), "unchanged") {
					t.Fatal("no-op mutation was sent")
				}
			case "output-failure":
				if exit != 8 || writes != 1 || !strings.Contains(stderr.String(), "output failure") {
					t.Fatal("output error path failed")
				}
			}
		})
	}
}

func TestVariableConsumerContractRemainsPinned(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "contracts", "ci-variables", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Schema     string `json:"schema"`
		Comparison struct {
			Revision string   `json:"revision"`
			Leaves   []string `json:"leaves"`
		} `json:"comparison"`
		Provider struct {
			Version string `json:"minimum_version"`
			Atomic  bool   `json:"atomic_precondition"`
		} `json:"provider"`
		Surface struct {
			Mutations              int    `json:"mutations_per_invocation"`
			Retries                int    `json:"retries"`
			MaxValue               int    `json:"max_value_bytes"`
			Ambiguous              string `json:"ambiguous_code"`
			AuthSource             string `json:"auth_source"`
			HiddenVerification     string `json:"hidden_value_verification"`
			HiddenNoop             *bool  `json:"hidden_noop_detection"`
			RequiresAcknowledgment bool   `json:"success_requires_provider_acknowledgment"`
			LostResponseSuccess    *bool  `json:"lost_response_success"`
		} `json:"surface"`
	}
	if json.Unmarshal(data, &contract) != nil || contract.Schema != "glab-axi/ci-variable-contract/v1" || contract.Comparison.Revision != "2bffd9a5b60ded64d6c9851683b27a480173a7ee" || contract.Provider.Version != "17.6.0" || contract.Provider.Atomic || contract.Surface.Mutations != 1 || contract.Surface.Retries != 0 || contract.Surface.MaxValue != civariable.MaxValueBytes || contract.Surface.Ambiguous != string(uxv1.CodeAmbiguousVariable) || contract.Surface.AuthSource != "native" {
		t.Fatal("CI variable consumer contract drifted")
	}
	if contract.Surface.HiddenVerification != "unavailable_hidden" || contract.Surface.HiddenNoop == nil || *contract.Surface.HiddenNoop || !contract.Surface.RequiresAcknowledgment || contract.Surface.LostResponseSuccess == nil || *contract.Surface.LostResponseSuccess {
		t.Fatal("hidden verification contract overstates provider evidence")
	}
	if len(contract.Comparison.Leaves) != 6 {
		t.Fatal("missing reference leaves")
	}
	for _, path := range contract.Comparison.Leaves {
		definition, ok := lookupDefinition(strings.Split(path, " "))
		if !ok || !definition.NativeAuth || !definition.RequireNativeAuth || definition.Backend != "native" {
			t.Fatal("missing native-only executable contract")
		}
	}
}
