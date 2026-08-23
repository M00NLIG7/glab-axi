package product

import (
	"context"
	"os"
	"strings"

	"gl-axi/internal/auth"
	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/gitremote"
	"gl-axi/internal/safeurl"
)

type Target struct {
	Host string
	Repo string
}

func resolveTarget(ctx context.Context, parsed Parsed, cwd string, lookup auth.LookupEnv) (Target, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	target := Target{Host: parsed.Values["--hostname"], Repo: parsed.Values["--repo"]}
	if parsed.Definition.Path != nil && len(parsed.Definition.Path) == 2 && parsed.Definition.Path[0] == "repo" && parsed.Definition.Path[1] == "view" && len(parsed.Positionals) == 1 {
		if target.Repo != "" {
			return Target{}, uxv1.NewError(uxv1.CodeValidation, "repo view accepts either its repository argument or --repo, not both")
		}
		target.Repo = parsed.Positionals[0]
	}
	if target.Host == "" {
		if host, ok := lookup("GITLAB_HOST"); ok && host != "" {
			target.Host = host
		}
	}
	if target.Repo == "" && parsed.Definition.RepoMode != RepoNone {
		identity, identityErr := gitremote.Origin(ctx, cwd)
		if identityErr != nil {
			return Target{}, uxv1.NewError(uxv1.CodeValidation, "repository is required; run inside a GitLab checkout or pass -R namespace/project")
		}
		target.Repo = identity.Project
		if target.Host == "" {
			switch {
			case strings.EqualFold(identity.Host, "gitlab.com"):
				target.Host = "gitlab.com"
			case knownNonGitLabHost(identity.Host):
				return Target{}, uxv1.NewError(uxv1.CodeValidation, "origin is not a GitLab authority; pass an explicit GitLab --hostname and repository")
			default:
				return Target{}, uxv1.NewError(uxv1.CodeSafety, "self-managed GitLab remotes require explicit --hostname or GITLAB_HOST authority")
			}
		}
	}
	if target.Host == "" {
		target.Host = "gitlab.com"
	}
	if err := safeurl.ValidateHost(target.Host); err != nil {
		return Target{}, uxv1.Wrap(uxv1.CodeValidation, "invalid GitLab hostname", err)
	}
	if parsed.Definition.RepoMode != RepoNone {
		if target.Repo == "" {
			return Target{}, uxv1.NewError(uxv1.CodeValidation, "repository is required; run inside a GitLab checkout or pass -R namespace/project")
		}
		if err := safeurl.ValidateProject(target.Repo); err != nil {
			return Target{}, uxv1.Wrap(uxv1.CodeValidation, "invalid repository target", err)
		}
	}
	return target, nil
}

func knownNonGitLabHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || strings.HasSuffix(host, ".github.com")
}
