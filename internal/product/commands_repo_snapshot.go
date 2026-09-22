package product

import (
	"context"
	"net/url"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/safeurl"
)

// The opt-in administration snapshot shares native authority with subsequent
// administration operations. Ordinary repo view remains delegated by default.
func executeNativeRepoSnapshot(ctx context.Context, p Parsed, deps Dependencies, meta uxv1.Meta) (commandOutput, error) {
	client, err := openNative(ctx, p, deps)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	defer client.Close()
	host := client.Host()
	meta.Backend, meta.UpstreamVersion, meta.Host = "native", "", host.Name
	target := Target{Host: host.Name, Repo: p.Values["--repo"]}
	s := adminSession{client: client, target: target, authority: host.Authority, meta: meta}
	response, err := s.get(ctx, "projects/"+url.PathEscape(target.Repo))
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	project, err := decodeAdminProject(response.Body, false)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	if err := s.bind(project, target.Repo, 0, nil); err != nil {
		return commandOutput{meta: meta}, err
	}
	repository, truncated, err := normalizeRepoObject(response.Body, host.Authority.Web.Host)
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	meta.Truncated = truncated
	if truncated {
		meta.Reason = "field_limit"
	}
	data := map[string]any{"repository": repository, "admin_snapshot": project.adminProject}
	switch project.ImportStatus {
	case "none", "scheduled", "started", "finished", "failed":
		data["import_status"] = project.ImportStatus
	default:
		data["import_status"] = "unknown"
	}
	if from := project.ForkedFrom; from != nil {
		if from.ID < 1 || safeurl.ValidateProject(from.Path) != nil || from.URL != host.Authority.ExpectedProjectURL(from.Path) {
			return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeSafety, "project snapshot contains invalid fork source identity")
		}
		data["forked_from_project"] = from
	}
	return commandOutput{data: data, meta: meta}, nil
}
