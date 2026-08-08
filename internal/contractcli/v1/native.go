package v1cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"glab-axi/internal/app"
	"glab-axi/internal/auth"
	"glab-axi/internal/buildinfo"
	"glab-axi/internal/config"
	v1 "glab-axi/internal/contract/v1"
	"glab-axi/internal/gitlab"
	"glab-axi/internal/limits"
	"glab-axi/internal/output"
	"glab-axi/internal/presentation"
	"glab-axi/internal/privatefile"
	runtimepkg "glab-axi/internal/runtime"
)

const Version = buildinfo.Version

const nativeHelp = `glab-axi - bounded native GitLab MR/CI client

Usage:
  glab-axi auth status --host HOST --repo GROUP/PROJECT [--format toon|json]
  glab-axi auth import --host HOST --api-base URL --web-base URL --token-stdin [--format toon|json]
  glab-axi mr ensure --host HOST --repo PROJECT --source BRANCH --target BRANCH --title-file FILE --description-file FILE [--format toon|json]
  glab-axi mr view IID --host HOST --repo PROJECT [--format toon|json]
  glab-axi ci status --mr IID --host HOST --repo PROJECT [--format toon|json]
  glab-axi ci jobs --pipeline-id ID --host HOST --repo PROJECT [--format toon|json]
  glab-axi ci trace JOB_ID --host HOST --repo PROJECT [--format toon|json]

Credentials are accepted only from documented environment variables or the
no-UI OS keyring. Token argv flags, interactive login, generic API access,
merge, close, approve, comment, delete, and pipeline mutation are unsupported.
`

type commandResult struct {
	data   any
	format output.Format
}

func RunNative(ctx context.Context, args []string, deps runtimepkg.Dependencies) int {
	if len(args) == 1 && (args[0] == "--help" || args[0] == "help") {
		_, _ = io.WriteString(deps.Stdout, nativeHelp)
		return 0
	}
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprintf(deps.Stdout, "glab-axi %s (contract %s)\n", Version, v1.Schema)
		return 0
	}
	format := formatHint(args)
	result, err := dispatchNative(ctx, args, deps)
	if err != nil {
		if writeErr := output.Write(deps.Stdout, format, v1.Failure(err)); writeErr != nil {
			_, _ = io.WriteString(deps.Stderr, "glab-axi: output failure\n")
			return 8
		}
		return v1.ExitCode(err)
	}
	if err := output.Write(deps.Stdout, result.format, v1.Success(result.data, false)); err != nil {
		_, _ = io.WriteString(deps.Stderr, "glab-axi: output failure\n")
		return 8
	}
	return 0
}

func dispatchNative(parent context.Context, args []string, deps runtimepkg.Dependencies) (commandResult, error) {
	if len(args) < 2 {
		return commandResult{}, v1.NewError(v1.CodeUnsupported, "unsupported command; use --help")
	}
	switch args[0] + " " + args[1] {
	case "auth status":
		parsed, format, err := parseNative(args[2:], flagSpec{"--host": true, "--repo": true, "--format": true}, 0, 0)
		if err != nil {
			return commandResult{}, err
		}
		if err := parsed.require("--host", "--repo"); err != nil {
			return commandResult{}, err
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, parsed.values["--host"], parsed.values["--repo"], true)
		if err != nil {
			return commandResult{}, err
		}
		if err := runtime.Service.AuthStatus(ctx, true); err != nil {
			return commandResult{}, err
		}
		return commandResult{format: format, data: map[string]any{"authenticated": true, "host": runtime.Host.Name, "project": runtime.Service.Project}}, nil
	case "auth import":
		parsed, format, err := parseNative(args[2:], flagSpec{"--host": true, "--api-base": true, "--web-base": true, "--ca-bundle": true, "--no-proxy": false, "--token-stdin": false, "--format": true}, 0, 0)
		if err != nil {
			return commandResult{}, err
		}
		if err := parsed.require("--host", "--api-base", "--web-base", "--token-stdin"); err != nil {
			return commandResult{}, err
		}
		ctx, cancel := app.WithTimeout(parent, true)
		defer cancel()
		if err := importToken(ctx, deps, parsed); err != nil {
			return commandResult{}, err
		}
		return commandResult{format: format, data: map[string]any{"imported": true, "host": strings.ToLower(parsed.values["--host"]), "storage": "os_keyring"}}, nil
	case "mr ensure":
		parsed, format, err := parseNative(args[2:], flagSpec{"--host": true, "--repo": true, "--source": true, "--target": true, "--title-file": true, "--description-file": true, "--format": true}, 0, 0)
		if err != nil {
			return commandResult{}, err
		}
		if err := parsed.require("--host", "--repo", "--source", "--target", "--title-file", "--description-file"); err != nil {
			return commandResult{}, err
		}
		title, err := readPrivateFile(parsed.values["--title-file"], limits.MaxTitleBytes, true)
		if err != nil {
			return commandResult{}, err
		}
		description, err := readPrivateFile(parsed.values["--description-file"], limits.MaxDescriptionBytes, false)
		if err != nil {
			return commandResult{}, err
		}
		ctx, cancel := app.WithTimeout(parent, true)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, parsed.values["--host"], parsed.values["--repo"], true)
		if err != nil {
			return commandResult{}, err
		}
		result, err := runtime.Service.Ensure(ctx, parsed.values["--source"], parsed.values["--target"], title, description)
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{format: format, data: presentation.FromResult(result)}, nil
	case "mr view":
		parsed, format, err := parseNative(args[2:], flagSpec{"--host": true, "--repo": true, "--format": true}, 1, 1)
		if err != nil {
			return commandResult{}, err
		}
		if err := parsed.require("--host", "--repo"); err != nil {
			return commandResult{}, err
		}
		iid, err := positiveID(parsed.positionals[0], "merge request IID")
		if err != nil {
			return commandResult{}, err
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, parsed.values["--host"], parsed.values["--repo"], true)
		if err != nil {
			return commandResult{}, err
		}
		mr, err := runtime.Service.View(ctx, iid)
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{format: format, data: map[string]any{"mr": presentation.FromMR(mr)}}, nil
	case "ci status":
		parsed, format, err := parseNative(args[2:], flagSpec{"--mr": true, "--host": true, "--repo": true, "--format": true}, 0, 0)
		if err != nil {
			return commandResult{}, err
		}
		if err := parsed.require("--mr", "--host", "--repo"); err != nil {
			return commandResult{}, err
		}
		iid, err := positiveID(parsed.values["--mr"], "merge request IID")
		if err != nil {
			return commandResult{}, err
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, parsed.values["--host"], parsed.values["--repo"], true)
		if err != nil {
			return commandResult{}, err
		}
		result, err := runtime.Service.CIStatus(ctx, iid)
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{format: format, data: presentation.FromCI(result)}, nil
	case "ci jobs":
		parsed, format, err := parseNative(args[2:], flagSpec{"--pipeline-id": true, "--host": true, "--repo": true, "--format": true}, 0, 0)
		if err != nil {
			return commandResult{}, err
		}
		if err := parsed.require("--pipeline-id", "--host", "--repo"); err != nil {
			return commandResult{}, err
		}
		pipelineID, err := positiveID(parsed.values["--pipeline-id"], "pipeline ID")
		if err != nil {
			return commandResult{}, err
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, parsed.values["--host"], parsed.values["--repo"], true)
		if err != nil {
			return commandResult{}, err
		}
		jobs, err := runtime.Service.NormalizedJobs(ctx, pipelineID)
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{format: format, data: map[string]any{"jobs": jobs}}, nil
	case "ci trace":
		parsed, format, err := parseNative(args[2:], flagSpec{"--host": true, "--repo": true, "--format": true}, 1, 1)
		if err != nil {
			return commandResult{}, err
		}
		if err := parsed.require("--host", "--repo"); err != nil {
			return commandResult{}, err
		}
		jobID, err := positiveID(parsed.positionals[0], "job ID")
		if err != nil {
			return commandResult{}, err
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, parsed.values["--host"], parsed.values["--repo"], true)
		if err != nil {
			return commandResult{}, err
		}
		trace, truncated, err := runtime.Service.Trace(ctx, jobID)
		if err != nil {
			return commandResult{}, err
		}
		return commandResult{format: format, data: map[string]any{"trace": string(trace), "truncated": truncated}}, nil
	default:
		return commandResult{}, v1.NewError(v1.CodeUnsupported, "unsupported command; use --help")
	}
}

func parseNative(args []string, spec flagSpec, minPositionals, maxPositionals int) (parsedArgs, output.Format, error) {
	parsed, err := parseStrict(args, spec, minPositionals, maxPositionals)
	if err != nil {
		return parsedArgs{}, output.TOON, err
	}
	format := output.TOON
	if value := parsed.values["--format"]; value != "" {
		format = output.Format(value)
		if format != output.TOON && format != output.JSON {
			return parsedArgs{}, output.TOON, v1.NewError(v1.CodeValidation, "format must be toon or json")
		}
	}
	return parsed, format, nil
}

func formatHint(args []string) output.Format {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--format" && args[i+1] == "json" {
			return output.JSON
		}
	}
	return output.TOON
}

func importToken(ctx context.Context, deps runtimepkg.Dependencies, parsed parsedArgs) error {
	path, err := deps.ConfigFile()
	if err != nil {
		return err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	name := strings.ToLower(parsed.values["--host"])
	entry := config.Host{
		GitHosts:      []string{name},
		APIBase:       parsed.values["--api-base"],
		WebBase:       parsed.values["--web-base"],
		CABundle:      parsed.values["--ca-bundle"],
		ProxyDisabled: parsed.booleans["--no-proxy"],
	}
	if err := cfg.Put(name, entry); err != nil {
		return err
	}
	resolved, err := cfg.Resolve(name)
	if err != nil {
		return err
	}
	terminal := false
	if deps.IsTerminal != nil {
		terminal = deps.IsTerminal()
	}
	token, err := auth.ReadToken(deps.Stdin, terminal)
	if err != nil {
		return err
	}
	client, err := gitlab.NewClient(resolved, auth.Credential{Value: token, Kind: auth.PrivateToken, Source: "stdin"}, gitlab.Options{HTTPClient: deps.HTTPClient})
	if err != nil {
		return err
	}
	if _, err := client.AuthenticatedUser(ctx); err != nil {
		return err
	}
	if deps.Keyring == nil {
		return v1.NewError(v1.CodeAuthentication, "no noninteractive OS keyring is available")
	}
	service, account := auth.ServiceName(resolved), resolved.Name
	previous, getErr := deps.Keyring.Get(ctx, service, account)
	previousExists := getErr == nil
	if getErr != nil && !errors.Is(getErr, auth.ErrKeyringNotFound) {
		return v1.Wrap(v1.CodeAuthentication, "cannot prepare transactional GitLab credential import", getErr)
	}
	if err := deps.Keyring.Set(ctx, service, account, token); err != nil {
		return v1.Wrap(v1.CodeAuthentication, "cannot store GitLab credential without interaction", err)
	}
	if err := config.Save(path, cfg); err != nil {
		rollbackErr := deps.Keyring.Delete(ctx, service, account)
		if previousExists {
			rollbackErr = deps.Keyring.Set(ctx, service, account, previous)
		}
		if rollbackErr != nil {
			return v1.Wrap(v1.CodeSafety, "credential import failed and secure-store rollback was incomplete", rollbackErr)
		}
		return err
	}
	return nil
}

func readPrivateFile(path string, max int, trimFinalNewline bool) (string, error) {
	return privatefile.Read(path, max, trimFinalNewline)
}

func positiveID(value, label string) (int64, error) {
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id < 1 {
		return 0, v1.NewError(v1.CodeValidation, label+" must be a positive integer")
	}
	return id, nil
}

func parseMRIdentifier(value string, authorityURL func(string) (int64, error)) (int64, error) {
	if strings.HasPrefix(value, "https://") {
		return authorityURL(value)
	}
	return positiveID(value, "merge request IID")
}
