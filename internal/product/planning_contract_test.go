package product

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
)

func TestPlanningConsumerContractMatchesRegistry(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "contracts", "gitlab-planning", "v19.3.0", "consumer.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema    string `json:"schema"`
		Reference struct {
			Commit string `json:"commit"`
		} `json:"reference"`
		Provider struct {
			Commit                    string `json:"commit"`
			RuntimeVersionAttestation bool   `json:"runtime_version_attestation"`
		} `json:"provider"`
		Commands []struct {
			Argv   []string `json:"argv"`
			Schema string   `json:"schema"`
			Write  bool     `json:"write_capable"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(body, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "glab-axi/planning-consumer/v1" || fixture.Reference.Commit != "2bffd9a5b60ded64d6c9851683b27a480173a7ee" || fixture.Provider.Commit != "8f83039bebbbb61d3b7b8b7e2342d1deec1e14c6" || fixture.Provider.RuntimeVersionAttestation || len(fixture.Commands) != 5 {
		t.Fatalf("invalid fixture %+v", fixture)
	}
	for _, command := range fixture.Commands {
		parsed, err := Parse(command.Argv)
		if err != nil {
			t.Fatalf("%v: %v", command.Argv, err)
		}
		if parsed.Command == nil || parsed.Command.Definition.Schema != command.Schema || parsed.Command.Definition.Write != command.Write {
			t.Fatalf("contract drift %+v", command)
		}
	}
}

func TestPlanningResponseIdentityAndLimitAdversaries(t *testing.T) {
	tests := []struct {
		name  string
		op    glab.Operation
		group bool
		doc   func() planJSON
		code  uxv1.Code
	}{
		{"group path suffix is not membership", glab.OpBoardIssues, true, func() planJSON {
			item := planItem(9, false, false)
			item["project"] = planJSON{"id": "gid://gitlab/Project/10", "fullPath": "other/team/nested/project", "webUrl": "https://gitlab.com/other/team/nested/project"}
			item["webUrl"] = "https://gitlab.com/other/team/nested/project/-/issues/9"
			return planDoc(glab.OpBoardIssues, true, item)
		}, uxv1.CodeSafety},
		{"wrong selected list", glab.OpBoardIssues, false, func() planJSON {
			d := planDoc(glab.OpBoardIssues, false)
			d["data"].(planJSON)["scope"].(planJSON)["board"].(planJSON)["lists"].(planJSON)["nodes"].([]any)[0].(planJSON)["id"] = "gid://gitlab/List/99"
			return d
		}, uxv1.CodeSafety},
		{"same scoped iid different global id", glab.OpWorkItemHierarchy, false, func() planJSON {
			a, b := planItem(8, true, false), planItem(8, true, false)
			b["id"] = "gid://gitlab/WorkItem/999"
			return planDoc(glab.OpWorkItemHierarchy, false, a, b)
		}, uxv1.CodeSafety},
		{"self scoped iid different global id", glab.OpWorkItemHierarchy, false, func() planJSON {
			a := planItem(7, true, false)
			a["id"] = "gid://gitlab/WorkItem/999"
			return planDoc(glab.OpWorkItemHierarchy, false, a)
		}, uxv1.CodeSafety},
		{"too many nodes", glab.OpBoardList, false, func() planJSON {
			nodes := []any{}
			for i := 1; i <= 32; i++ {
				nodes = append(nodes, planBoard(false, i))
			}
			return planDoc(glab.OpBoardList, false, nodes...)
		}, uxv1.CodeUpstream},
		{"cursor missing with more data", glab.OpBoardList, false, func() planJSON {
			d := planDoc(glab.OpBoardList, false, planBoard(false, 1))
			planMore(d, glab.OpBoardList, "")
			return d
		}, uxv1.CodeUpstream},
		{"cursor too long", glab.OpBoardList, false, func() planJSON {
			d := planDoc(glab.OpBoardList, false, planBoard(false, 1))
			planMore(d, glab.OpBoardList, strings.Repeat("c", 1025))
			return d
		}, uxv1.CodeUpstream},
		{"unknown state", glab.OpWorkItemHierarchy, false, func() planJSON {
			d := planDoc(glab.OpWorkItemHierarchy, false)
			planRoot(d)["state"] = "future"
			return d
		}, uxv1.CodeUpstream},
		{"group entitlement unavailable", glab.OpWorkItemHierarchy, true, func() planJSON {
			d := planDoc(glab.OpWorkItemHierarchy, true)
			d["data"].(planJSON)["namespace"].(planJSON)["workItem"] = nil
			return d
		}, uxv1.CodeNotFound},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out, _ := planRun(t, test.op, test.group, 30, test.doc())
			if out.OK || out.Meta.Complete || out.Error == nil || out.Error.Code != test.code {
				t.Fatalf("out=%+v err=%+v", out, out.Error)
			}
		})
	}
}

func TestPlanningOperationBudgetAndMetadataDrift(t *testing.T) {
	pages := []planJSON{}
	for i := 1; i <= 5; i++ {
		doc := planDoc(glab.OpBoardList, false, planBoard(false, i))
		doc["ignored"] = strings.Repeat("z", limits.MaxJSONPageBytes-4096)
		planMore(doc, glab.OpBoardList, fmt.Sprint(i))
		pages = append(pages, doc)
	}
	out, d := planRun(t, glab.OpBoardList, false, 1000, pages...)
	if out.OK || out.Error.Code != uxv1.CodeUpstream || len(d.requests) != 5 {
		t.Fatalf("budget %+v", out)
	}
	a, b := planDoc(glab.OpBoardView, false, planColumn(1)), planDoc(glab.OpBoardView, false, planColumn(2))
	planMore(a, glab.OpBoardView, "next")
	b["data"].(planJSON)["scope"].(planJSON)["board"].(planJSON)["name"] = "changed"
	out, _ = planRun(t, glab.OpBoardView, false, 30, a, b)
	if out.OK || out.Error.Code != uxv1.CodeConflict {
		t.Fatalf("board drift %+v", out)
	}
}

func TestBoardIssuesMalformedGuardFormsDoNoChildWork(t *testing.T) {
	base := []string{"board", "issues", "7", "--list-id", "8", "-R", planPath, "--hostname", "gitlab.com", "--format=json"}
	for _, flags := range [][]string{{}, {"--allow-ordering-initialization=false"}, {"--allow-ordering-initialization=true"}, {"--allow-ordering-initialization", "--allow-ordering-initialization"}} {
		d := &fakeDelegate{}
		stdout, _, deps := productTestDeps(t, d)
		constructed := false
		deps.NewDelegate = func() delegateClient { constructed = true; return d }
		if Run(context.Background(), append(append([]string{}, base...), flags...), deps) == 0 || constructed || len(d.requests) != 0 {
			t.Fatalf("guard=%v out=%s", flags, stdout.String())
		}
	}
}
