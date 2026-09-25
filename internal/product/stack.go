package product

import (
	"context"
	"reflect"
	"strconv"
	"strings"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/delegate/glab"
	"gl-axi/internal/limits"
	"gl-axi/internal/localstack"
	"gl-axi/internal/safeurl"
)

func stackDefinitions() []Definition {
	common := []FlagDefinition{{Name: "--stack", Value: "NAME", Required: true, Description: "Explicit local stack name (lowercase ASCII letters, digits, hyphen, underscore; max 64)."}}
	metadata := []FlagDefinition{{Name: "--base", Value: "BRANCH", Required: true, Description: "Existing local trunk branch; never inferred."}, {Name: "--allow-local-metadata", Boolean: true, Required: true, Description: "Authorize only atomic local stack registration, not branch changes or provider writes."}}
	checkout := []FlagDefinition{{Name: "--allow-checkout", Boolean: true, Required: true, Description: "Authorize switching to the selected existing stack branch without force or stash."}, {Name: "--expected-current", Value: "BRANCH", Required: true, Description: "Exact current local branch."}, {Name: "--expected-head", Value: "SHA", Required: true, Description: "Exact current local branch head."}, {Name: "--expected-target", Value: "SHA", Required: true, Description: "Exact destination local branch head."}}
	var defs []Definition
	for _, action := range []string{"view", "init", "link", "checkout", "up", "down", "top", "bottom", "trunk"} {
		d := Definition{Path: []string{"stack", action}, RepoMode: RepoRequired, RequireExplicitHost: true, RequireExplicitRepo: true, Schema: "stack", Backend: "local", NoLimit: action != "view", Flags: append([]FlagDefinition{}, common...), Usage: "gl-axi stack " + action, Details: "Operates only on the current local repository; explicit host/project must match its unique origin. Linear stack, at most 32 existing local branches. No fetch, push, branch creation, commit, history rewriting or merge. See docs/stacks.md and contracts/stacks/v1.md."}
		switch action {
		case "view":
			d.Summary = "View a bounded local stack and optionally observe linked GitLab MRs."
			d.Flags = append(d.Flags, FlagDefinition{Name: "--mrs", Boolean: true, Description: "Read exact saved MR identities using pinned official glab; not merge readiness."})
			d.Usage += " [--mrs] [--limit N]"
		case "init", "link":
			d.Summary = "Register an existing local branch chain without switching branches."
			d.Positionals = 1
			d.MaxPositions = localstack.MaxBranches
			d.Flags = append(d.Flags, metadata...)
			d.Usage += " <branches...> --base BRANCH --allow-local-metadata"
			if action == "link" {
				d.Positionals = 2
				d.Backend = "official-glab"
				d.Summary = "Bind an existing local chain to exact existing same-project GitLab MRs."
				d.Flags = append(d.Flags, FlagDefinition{Name: "--mr", Value: "BRANCH=IID", Required: true, Repeatable: true, Description: "One exact existing open MR per branch; repeat for every branch. No provider write."})
				d.Usage += " --mr BRANCH=IID ..."
			}
		default:
			d.Summary = "Safely switch to an existing branch in the selected local stack."
			d.Flags = append(d.Flags, checkout...)
			if action == "checkout" {
				d.Positionals = 1
				d.MaxPositions = 1
				d.Usage += " BRANCH"
			}
			if action == "up" || action == "down" {
				d.MaxPositions = 1
				d.Usage += " [N]"
			}
			d.Usage += " --allow-checkout --expected-current BRANCH --expected-head SHA --expected-target SHA"
		}
		d.Usage += " --stack NAME --hostname HOST -R PROJECT [--format toon|json]"
		defs = append(defs, d)
	}
	return defs
}
func isStack(p Parsed) bool { return len(p.Definition.Path) == 2 && p.Definition.Path[0] == "stack" }
func validateStackParsed(p Parsed) error {
	bad := func(s string) error { return uxv1.NewError(uxv1.CodeValidation, s) }
	if !localstack.ValidName(p.Values["--stack"]) || safeurl.ValidateHost(p.Values["--hostname"]) != nil || knownNonGitLabHost(p.Values["--hostname"]) || safeurl.ValidateProject(p.Values["--repo"]) != nil {
		return bad("invalid explicit stack/host/project identity")
	}
	action := p.Definition.Path[1]
	if action == "view" {
		return nil
	}
	if action == "init" || action == "link" {
		r, err := stackRecord(p)
		if err != nil {
			return err
		}
		return localstack.Validate(r)
	}
	if !localstack.ValidBranch(p.Values["--expected-current"]) || !localstack.OID(p.Values["--expected-head"]) || !localstack.OID(p.Values["--expected-target"]) {
		return bad("checkout requires exact current branch and lowercase full head/destination OIDs")
	}
	if action == "checkout" && !localstack.ValidBranch(p.Positionals[0]) {
		return bad("checkout accepts only an existing local member branch, not a revision or MR URL")
	}
	if (action == "up" || action == "down") && len(p.Positionals) > 0 {
		n, err := strconv.Atoi(p.Positionals[0])
		if err != nil || n < 1 || n > localstack.MaxBranches || strconv.Itoa(n) != p.Positionals[0] {
			return bad("navigation distance must be a canonical integer from 1 through 32")
		}
	}
	return nil
}
func stackRecord(p Parsed) (localstack.Record, error) {
	r := localstack.Record{Version: localstack.Version, Host: p.Values["--hostname"], Project: p.Values["--repo"], Base: p.Values["--base"], Branches: []localstack.Branch{}}
	bindings := map[string]int64{}
	for _, value := range p.MultiValues["--mr"] {
		separator := strings.LastIndexByte(value, '=')
		if separator < 0 {
			return r, uxv1.NewError(uxv1.CodeValidation, "invalid or duplicate BRANCH=IID binding")
		}
		branch, raw := value[:separator], value[separator+1:]
		iid, e := strconv.ParseInt(raw, 10, 64)
		if !localstack.ValidBranch(branch) || e != nil || iid < 1 || strconv.FormatInt(iid, 10) != raw || bindings[branch] != 0 {
			return r, uxv1.NewError(uxv1.CodeValidation, "invalid or duplicate BRANCH=IID binding")
		}
		bindings[branch] = iid
	}
	for _, branch := range p.Positionals {
		r.Branches = append(r.Branches, localstack.Branch{Name: branch, MR: bindings[branch]})
		delete(bindings, branch)
	}
	if len(bindings) > 0 {
		return r, uxv1.NewError(uxv1.CodeValidation, "MR binding names a branch outside the selected chain")
	}
	if p.Definition.Path[1] == "link" {
		for _, b := range r.Branches {
			if b.MR == 0 {
				return r, uxv1.NewError(uxv1.CodeValidation, "link requires exactly one MR for every branch")
			}
		}
	}
	return r, nil
}

type stackData struct {
	Stack stackResult `json:"stack"`
}
type stackResult struct {
	Name          string      `json:"name"`
	Base          string      `json:"base"`
	BaseHead      string      `json:"base_head"`
	Current       string      `json:"current_branch"`
	Action        string      `json:"action"`
	MRObservation string      `json:"mr_observation"`
	Total         int         `json:"total_branches"`
	Branches      []stackNode `json:"branches"`
}
type stackNode struct {
	localstack.Node
	MRState string `json:"mr_state"`
	MRURL   string `json:"mr_url"`
}

func executeStack(parent context.Context, p Parsed, deps Dependencies) (commandOutput, error) {
	meta := uxv1.Meta{Backend: "local", Host: p.Values["--hostname"], Repo: p.Values["--repo"], Complete: true, Limit: p.Limit}
	out := commandOutput{meta: meta}
	ctx, cancel := context.WithTimeout(parent, limits.ShortOperation)
	defer cancel()
	action := p.Definition.Path[1]
	repo, err := localstack.Open(ctx, deps.Runtime.Cwd, meta.Host, meta.Repo, p.Values["--stack"], action != "view")
	if err != nil {
		return out, err
	}
	defer repo.Close()
	old, oldOID, err := repo.Load()
	if err != nil {
		return out, err
	}
	var record localstack.Record
	if action == "init" || action == "link" {
		record, err = stackRecord(p)
		if err != nil {
			return out, err
		}
		if !localstack.Compatible(old, record) {
			return out, uxv1.NewError(uxv1.CodeConflict, "stack already names a different chain or MR binding; registration was not replaced")
		}
	} else {
		if old == nil {
			return out, uxv1.NewError(uxv1.CodeNotFound, "selected local stack does not exist; initialize it explicitly")
		}
		record = *old
	}
	snap, err := repo.Inspect(record)
	if err != nil {
		return out, err
	}
	result := stackResult{Name: p.Values["--stack"], Base: record.Base, BaseHead: snap.Heads[record.Base], Current: snap.Current, Action: "view", MRObservation: "not_requested", Total: len(record.Branches), Branches: []stackNode{}}
	for _, node := range snap.Nodes {
		result.Branches = append(result.Branches, stackNode{Node: node, MRState: "not_observed"})
	}
	if !snap.Valid {
		if action != "view" {
			return out, uxv1.NewError(uxv1.CodeConflict, "local stack is missing branches or has diverged ancestry; no local action performed")
		}
		out.meta.Complete = false
		out.meta.Reason = "local_graph_stale"
	}
	if action == "link" || p.Booleans["--mrs"] {
		out.meta.Backend = "official-glab"
		if !snap.Valid {
			return out, uxv1.NewError(uxv1.CodeConflict, "live MR observation requires a valid local chain")
		}
		client := deps.delegate()
		budget := &mergeReadBudget{}
		states, e := observeStackMRs(ctx, client, record, snap, &out.meta, budget)
		if e != nil {
			return out, e
		}
		if action == "link" {
			again, e := observeStackMRs(ctx, client, record, snap, &out.meta, budget)
			if e != nil {
				return out, e
			}
			if !reflect.DeepEqual(states, again) {
				return out, uxv1.NewError(uxv1.CodeConflict, "MR identities changed during binding; no metadata was published")
			}
		}
		result.MRObservation = "observed"
		for i, b := range record.Branches {
			if b.MR == 0 {
				result.Branches[i].MRState = "unbound"
				result.MRObservation = "partial"
				out.meta.Complete = false
				out.meta.Reason = "unbound_mrs"
				continue
			}
			result.Branches[i].MRState = states[i].State
			result.Branches[i].MRURL = states[i].WebURL
		}
	}
	switch action {
	case "view":
		err = repo.Verify(record, oldOID, snap)
	case "init", "link":
		result.Action, err = repo.Save(old, oldOID, record, snap)
	default:
		var destination string
		destination, err = stackDestination(action, p.Positionals, record, snap.Current)
		if err == nil {
			result.Action, err = repo.Checkout(record, oldOID, snap, destination, p.Values["--expected-current"], p.Values["--expected-head"], p.Values["--expected-target"])
		}
		if err == nil {
			result.Current = destination
			for i := range result.Branches {
				result.Branches[i].Current = result.Branches[i].Name == destination
			}
		}
	}
	if err != nil {
		return out, err
	}
	if action == "view" && len(result.Branches) > p.Limit {
		result.Branches = result.Branches[:p.Limit]
		out.meta.Complete = false
		out.meta.Truncated = true
		out.meta.Reason = "display_limit"
	}
	out.meta.Count = len(result.Branches)
	out.data = stackData{Stack: result}
	return out, nil
}
func stackDestination(action string, args []string, r localstack.Record, current string) (string, error) {
	chain := []string{r.Base}
	for _, b := range r.Branches {
		chain = append(chain, b.Name)
	}
	index := -1
	for i, b := range chain {
		if b == current {
			index = i
		}
	}
	if index < 0 {
		return "", uxv1.NewError(uxv1.CodeConflict, "current branch is not in the selected stack (detached HEAD is not navigable)")
	}
	switch action {
	case "checkout":
		for _, b := range chain {
			if b == args[0] {
				return b, nil
			}
		}
		return "", uxv1.NewError(uxv1.CodeValidation, "checkout destination is not in the selected stack")
	case "trunk":
		return r.Base, nil
	case "bottom":
		return chain[1], nil
	case "top":
		return chain[len(chain)-1], nil
	case "up", "down":
		n := 1
		if len(args) > 0 {
			n, _ = strconv.Atoi(args[0])
		}
		if action == "down" {
			n = -n
		}
		index += n
		if index < 0 || index >= len(chain) {
			return "", uxv1.NewError(uxv1.CodeValidation, "navigation would leave the selected stack")
		}
		return chain[index], nil
	}
	return "", uxv1.NewError(uxv1.CodeInternal, "invalid stack navigation")
}

type stackMRIdentity struct {
	ID, IID, SourceProject, TargetProject int64
	Source, Target, Head, State, WebURL   string
}

func observeStackMRs(ctx context.Context, client delegateClient, r localstack.Record, snap localstack.Snapshot, meta *uxv1.Meta, budget *mergeReadBudget) ([]stackMRIdentity, error) {
	target := Target{Host: r.Host, Repo: r.Project}
	response, err := client.Do(ctx, glab.Request{Operation: glab.OpEnsureProject, Host: r.Host, Repo: r.Project})
	if err != nil {
		return nil, err
	}
	meta.UpstreamVersion = response.UpstreamVersion
	if err = budget.add(response.Body); err != nil {
		return nil, err
	}
	var project mergeProject
	if err = decodeStrict(response.Body, &project); err != nil {
		return nil, err
	}
	if project.ID < 1 || project.PathWithNamespace != r.Project || project.WebURL != canonicalProjectURL(r.Host, r.Project) {
		return nil, uxv1.NewError(uxv1.CodeSafety, "stack provider project identity mismatch")
	}
	results := make([]stackMRIdentity, len(r.Branches))
	for i, b := range r.Branches {
		if b.MR == 0 {
			continue
		}
		mr, e := loadMergeMR(ctx, client, target, b.MR, meta, budget)
		if e != nil {
			return nil, e
		}
		expectation := mergeExpectation{URL: canonicalMRURL(r.Host, r.Project, b.MR), Head: snap.Heads[b.Name], SourceBranch: b.Name, TargetBranch: snap.Nodes[i].Parent}
		if e = validateMergeMRIdentity(mr, target, project.ID, b.MR, expectation); e != nil {
			return nil, e
		}
		if mr.State != "opened" {
			return nil, uxv1.NewError(uxv1.CodeConflict, "stack binding requires existing open MRs; merged dependencies need explicit follow-on reconciliation")
		}
		results[i] = stackMRIdentity{mr.ID, mr.IID, mr.SourceProjectID, mr.TargetProjectID, mr.SourceBranch, mr.TargetBranch, mr.SHA, mr.State, mr.WebURL}
	}
	return results, nil
}
