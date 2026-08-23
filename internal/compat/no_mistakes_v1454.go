// Package compat implements only the executable argv contract used by
// no-mistakes v1.45.4. It is not a general GitLab CLI.
package compat

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"gl-axi/internal/app"
	"gl-axi/internal/buildinfo"
	v1 "gl-axi/internal/contract/v1"
	"gl-axi/internal/limits"
	"gl-axi/internal/presentation"
	"gl-axi/internal/redact"
	runtimepkg "gl-axi/internal/runtime"
)

const Version = buildinfo.Version

const Help = `glab-axi compatibility adapter for no-mistakes/v1.45.4

This executable may be built with the filename "glab", but it is NOT the
upstream GitLab CLI. It supports only the pinned no-mistakes MR/CI contract:
  auth status
  mr list | create | update | view
  ci status | get | trace
  api --paginate projects/<configured-project>/pipelines/<id>/jobs

Merge, close, approve, delete, comment, retry, repository mutation, arbitrary
API requests, interactive login, browser flows, and token flags are denied.
`

var pipelineJobsRoute = regexp.MustCompile(`^projects/[^/?#]+/pipelines/[1-9][0-9]*/jobs$`)

func Run(ctx context.Context, args []string, deps runtimepkg.Dependencies) int {
	if len(args) == 1 && args[0] == "--help" {
		_, _ = io.WriteString(deps.Stdout, Help)
		return 0
	}
	if len(args) == 1 && args[0] == "--version" {
		_, _ = fmt.Fprintf(deps.Stdout, "glab-axi compatibility adapter %s (no-mistakes/v1.45.4; not upstream glab)\n", Version)
		return 0
	}
	if err := dispatch(ctx, args, deps); err != nil {
		message := redact.New().Bounded(v1.AsError(err).Message, limits.MaxStderrBytes-len("glab-axi compat: \n"))
		_, _ = fmt.Fprintf(deps.Stderr, "glab-axi compat: %s\n", message)
		return v1.ExitCode(err)
	}
	return 0
}

func dispatch(parent context.Context, args []string, deps runtimepkg.Dependencies) error {
	if len(args) < 2 {
		return v1.NewError(v1.CodeUnsupported, "unsupported command; use --help")
	}
	switch args[0] + " " + args[1] {
	case "auth status":
		parsed, err := parse(args[2:], map[string]bool{"--hostname": true}, 0, 0)
		if err != nil {
			return err
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, parsed.values["--hostname"], "", false)
		if err != nil {
			return err
		}
		return runtime.Service.AuthStatus(ctx, false)
	case "mr list":
		parsed, err := parse(args[2:], map[string]bool{"--source-branch": true, "--target-branch": true, "--output": true}, 0, 0)
		if err != nil {
			return err
		}
		if err := require(parsed, "--source-branch", "--output"); err != nil {
			return err
		}
		if parsed.values["--output"] != "json" {
			return v1.NewError(v1.CodeValidation, "compatibility output must be json")
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, "", "", true)
		if err != nil {
			return err
		}
		mrs, err := runtime.Service.List(ctx, parsed.values["--source-branch"], parsed.values["--target-branch"])
		if err != nil {
			return err
		}
		out := make([]map[string]any, 0, len(mrs))
		for _, mr := range mrs {
			out = append(out, map[string]any{"iid": mr.IID, "web_url": mr.WebURL})
		}
		return writeJSON(deps.Stdout, out)
	case "mr create":
		parsed, err := parse(args[2:], map[string]bool{"--source-branch": true, "--target-branch": true, "--title": true, "--description": true, "--yes": false}, 0, 0)
		if err != nil {
			return err
		}
		if err := require(parsed, "--source-branch", "--target-branch", "--title", "--description", "--yes"); err != nil {
			return err
		}
		ctx, cancel := app.WithTimeout(parent, true)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, "", "", true)
		if err != nil {
			return err
		}
		result, err := runtime.Service.Create(ctx, parsed.values["--source-branch"], parsed.values["--target-branch"], parsed.values["--title"], parsed.values["--description"])
		if err != nil {
			return err
		}
		return writeJSON(deps.Stdout, map[string]any{"iid": result.MR.IID, "web_url": result.MR.WebURL})
	case "mr update":
		parsed, err := parse(args[2:], map[string]bool{"--title": true, "--description": true, "--yes": false}, 1, 1)
		if err != nil {
			return err
		}
		if err := require(parsed, "--title", "--description", "--yes"); err != nil {
			return err
		}
		ctx, cancel := app.WithTimeout(parent, true)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, "", "", true)
		if err != nil {
			return err
		}
		iid, err := parseMRID(parsed.positionals[0], func(raw string) (int64, error) {
			return runtime.Host.Authority.ParseMRWebURL(raw, runtime.Service.Project)
		})
		if err != nil {
			return err
		}
		result, err := runtime.Service.Update(ctx, iid, parsed.values["--title"], parsed.values["--description"])
		if err != nil {
			return err
		}
		return writeJSON(deps.Stdout, map[string]any{"iid": result.MR.IID, "web_url": result.MR.WebURL})
	case "mr view":
		parsed, err := parse(args[2:], map[string]bool{"--output": true}, 1, 1)
		if err != nil {
			return err
		}
		if err := require(parsed, "--output"); err != nil {
			return err
		}
		if parsed.values["--output"] != "json" {
			return v1.NewError(v1.CodeValidation, "compatibility output must be json")
		}
		iid, err := positiveID(parsed.positionals[0], "merge request IID")
		if err != nil {
			return err
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, "", "", true)
		if err != nil {
			return err
		}
		mr, err := runtime.Service.View(ctx, iid)
		if err != nil {
			return err
		}
		return writeJSON(deps.Stdout, presentation.FromMR(mr))
	case "ci status":
		parsed, err := parse(args[2:], map[string]bool{"--mr": true, "--output": true}, 0, 0)
		if err != nil {
			return err
		}
		if err := require(parsed, "--mr", "--output"); err != nil {
			return err
		}
		if parsed.values["--output"] != "json" {
			return v1.NewError(v1.CodeValidation, "compatibility output must be json")
		}
		iid, err := positiveID(parsed.values["--mr"], "merge request IID")
		if err != nil {
			return err
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, "", "", true)
		if err != nil {
			return err
		}
		result, err := runtime.Service.CIStatus(ctx, iid)
		if err != nil {
			return err
		}
		return writeJSON(deps.Stdout, result.Jobs)
	case "ci get":
		parsed, err := parse(args[2:], map[string]bool{"--pipeline-id": true, "--output": true, "--with-job-details": false}, 0, 0)
		if err != nil {
			return err
		}
		if err := require(parsed, "--pipeline-id", "--output", "--with-job-details"); err != nil {
			return err
		}
		if parsed.values["--output"] != "json" {
			return v1.NewError(v1.CodeValidation, "compatibility output must be json")
		}
		pipelineID, err := positiveID(parsed.values["--pipeline-id"], "pipeline ID")
		if err != nil {
			return err
		}
		return writeJobs(parent, deps, pipelineID)
	case "ci trace":
		parsed, err := parse(args[2:], map[string]bool{}, 1, 1)
		if err != nil {
			return err
		}
		jobID, err := positiveID(parsed.positionals[0], "job ID")
		if err != nil {
			return err
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, "", "", true)
		if err != nil {
			return err
		}
		trace, _, err := runtime.Service.Trace(ctx, jobID)
		if err != nil {
			return err
		}
		_, err = deps.Stdout.Write(trace)
		return err
	case "api --paginate":
		parsed, err := parse(args[2:], map[string]bool{}, 1, 1)
		if err != nil {
			return err
		}
		if !pipelineJobsRoute.MatchString(parsed.positionals[0]) {
			return v1.NewError(v1.CodeUnsupported, "only the configured paginated pipeline-jobs route is supported")
		}
		ctx, cancel := app.WithTimeout(parent, false)
		defer cancel()
		runtime, err := deps.ServiceContext(ctx, "", "", true)
		if err != nil {
			return err
		}
		prefix := "projects/" + url.PathEscape(runtime.Service.Project) + "/pipelines/"
		route := parsed.positionals[0]
		if !strings.HasPrefix(route, prefix) || !strings.HasSuffix(route, "/jobs") {
			return v1.NewError(v1.CodeUnsupported, "pipeline-jobs route does not match the configured project")
		}
		idText := strings.TrimSuffix(strings.TrimPrefix(route, prefix), "/jobs")
		pipelineID, err := positiveID(idText, "pipeline ID")
		if err != nil {
			return err
		}
		jobs, err := runtime.Service.NormalizedJobs(ctx, pipelineID)
		if err != nil {
			return err
		}
		return writeJSON(deps.Stdout, jobs)
	default:
		return v1.NewError(v1.CodeUnsupported, "unsupported command; this is not the upstream GitLab CLI")
	}
}

func writeJobs(parent context.Context, deps runtimepkg.Dependencies, pipelineID int64) error {
	ctx, cancel := app.WithTimeout(parent, false)
	defer cancel()
	runtime, err := deps.ServiceContext(ctx, "", "", true)
	if err != nil {
		return err
	}
	jobs, err := runtime.Service.NormalizedJobs(ctx, pipelineID)
	if err != nil {
		return err
	}
	return writeJSON(deps.Stdout, jobs)
}

type parsedArgs struct {
	values      map[string]string
	booleans    map[string]bool
	positionals []string
}

func parse(args []string, flags map[string]bool, minPositionals, maxPositionals int) (parsedArgs, error) {
	result := parsedArgs{values: map[string]string{}, booleans: map[string]bool{}}
	seen := map[string]bool{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--") {
			if arg == "--" || strings.Contains(arg, "=") {
				return parsedArgs{}, v1.NewError(v1.CodeValidation, "unsupported flag syntax")
			}
			needsValue, ok := flags[arg]
			if !ok {
				return parsedArgs{}, v1.NewError(v1.CodeUnsupported, "unsupported flag: "+arg)
			}
			if seen[arg] {
				return parsedArgs{}, v1.NewError(v1.CodeValidation, "duplicate flag: "+arg)
			}
			seen[arg] = true
			if needsValue {
				if i+1 >= len(args) {
					return parsedArgs{}, v1.NewError(v1.CodeValidation, "missing flag value")
				}
				i++
				if args[i] == "" || strings.ContainsRune(args[i], '\x00') {
					return parsedArgs{}, v1.NewError(v1.CodeValidation, "invalid flag value")
				}
				result.values[arg] = args[i]
			} else {
				result.booleans[arg] = true
			}
		} else {
			if strings.ContainsRune(arg, '\x00') {
				return parsedArgs{}, v1.NewError(v1.CodeValidation, "invalid positional argument")
			}
			result.positionals = append(result.positionals, arg)
		}
	}
	if len(result.positionals) < minPositionals || len(result.positionals) > maxPositionals {
		return parsedArgs{}, v1.NewError(v1.CodeValidation, "unexpected positional arguments")
	}
	return result, nil
}

func require(parsed parsedArgs, flags ...string) error {
	for _, flag := range flags {
		if parsed.values[flag] == "" && !parsed.booleans[flag] {
			return v1.NewError(v1.CodeValidation, "missing required flag: "+flag)
		}
	}
	return nil
}

func positiveID(value, label string) (int64, error) {
	var id int64
	if _, err := fmt.Sscan(value, &id); err != nil || id < 1 || fmt.Sprint(id) != value {
		return 0, v1.NewError(v1.CodeValidation, label+" must be a positive integer")
	}
	return id, nil
}

func parseMRID(value string, parseURL func(string) (int64, error)) (int64, error) {
	if strings.HasPrefix(value, "https://") {
		return parseURL(value)
	}
	return positiveID(value, "merge request IID")
}

func writeJSON(w io.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return v1.Wrap(v1.CodeInternal, "cannot encode compatibility output", err)
	}
	if len(data) > limits.MaxOperationBytes {
		return v1.NewError(v1.CodeUpstream, "compatibility output exceeds the operation limit")
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
