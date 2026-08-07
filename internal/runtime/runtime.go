// Package runtime wires config, repository identity, authentication, and the
// typed client without broadening any command's API surface.
package runtime

import (
	"context"
	"io"
	"net/http"
	"os"

	"glab-axi/internal/app"
	"glab-axi/internal/auth"
	"glab-axi/internal/config"
	"glab-axi/internal/contract/v1"
	"glab-axi/internal/gitlab"
	"glab-axi/internal/gitremote"
	"glab-axi/internal/safeurl"
)

type Dependencies struct {
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Cwd        string
	ConfigPath string
	Keyring    auth.Keyring
	LookupEnv  auth.LookupEnv
	HTTPClient *http.Client
	IsTerminal func() bool
}

func Defaults() Dependencies {
	cwd, _ := os.Getwd()
	return Dependencies{
		Stdin:     os.Stdin,
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
		Cwd:       cwd,
		Keyring:   auth.SystemKeyring(),
		LookupEnv: os.LookupEnv,
		IsTerminal: func() bool {
			info, err := os.Stdin.Stat()
			return err == nil && info.Mode()&os.ModeCharDevice != 0
		},
	}
}

func (d Dependencies) ConfigFile() (string, error) {
	if d.ConfigPath != "" {
		return d.ConfigPath, nil
	}
	return config.Path()
}

type Context struct {
	Service *app.Service
	Host    config.ResolvedHost
	Config  config.Config
	Path    string
}

func (d Dependencies) ServiceContext(ctx context.Context, explicitHost, explicitProject string, projectRequired bool) (Context, error) {
	path, err := d.ConfigFile()
	if err != nil {
		return Context{}, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return Context{}, err
	}
	host := explicitHost
	project := explicitProject
	if host == "" || projectRequired && project == "" {
		identity, inferErr := gitremote.Origin(ctx, d.Cwd)
		if inferErr != nil {
			return Context{}, inferErr
		}
		if host == "" {
			host = identity.Host
		}
		if project == "" {
			project = identity.Project
		}
	}
	if host == "" {
		return Context{}, v1.NewError(v1.CodeValidation, "GitLab host is required")
	}
	resolved, err := cfg.Resolve(host)
	if err != nil {
		return Context{}, err
	}
	if safeurl.IsIPHost(host) && !resolved.Explicit {
		return Context{}, v1.NewError(v1.CodeSafety, "IP-literal GitLab hosts require explicit configuration")
	}
	if projectRequired {
		if err := safeurl.ValidateProject(project); err != nil {
			return Context{}, err
		}
	}
	resolver := auth.Resolver{Lookup: d.LookupEnv, Keyring: d.Keyring}
	credential, err := resolver.Resolve(ctx, resolved)
	if err != nil {
		return Context{}, err
	}
	client, err := gitlab.NewClient(resolved, credential, gitlab.Options{HTTPClient: d.HTTPClient})
	if err != nil {
		return Context{}, err
	}
	localSHA := ""
	if project != "" {
		if identity, originErr := gitremote.Origin(ctx, d.Cwd); originErr == nil && identity.Project == project {
			if originHost, resolveErr := cfg.Resolve(identity.Host); resolveErr == nil && originHost.Name == resolved.Name {
				localSHA, _ = gitremote.HeadSHA(ctx, d.Cwd)
			}
		}
	}
	service := &app.Service{Client: client, Host: resolved, Project: project, LocalSHA: localSHA}
	return Context{Service: service, Host: resolved, Config: cfg, Path: path}, nil
}
