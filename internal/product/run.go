package product

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"glab-axi/internal/buildinfo"
	"glab-axi/internal/contract/uxv1"
	"glab-axi/internal/delegate/glab"
	"glab-axi/internal/limits"
	"glab-axi/internal/output"
	runtimepkg "glab-axi/internal/runtime"
	"glab-axi/internal/securestore"
	"glab-axi/internal/setuphooks"
	"glab-axi/internal/updater"

	"golang.org/x/term"
)

type delegateClient interface {
	Version(context.Context) (string, error)
	Do(context.Context, glab.Request) (glab.Response, error)
	AuthStatus(context.Context, string) (string, error)
	Login(context.Context, string) (string, error)
}

type Dependencies struct {
	Runtime         runtimepkg.Dependencies
	Env             []string
	GlabPath        string
	IsHumanTerminal func() bool
	NewDelegate     func() delegateClient
	SetupHooks      func(context.Context) (any, error)
	Update          func(context.Context, bool) (any, uxv1.Meta, error)
}

func Defaults(runtimeDeps runtimepkg.Dependencies) Dependencies {
	deps := Dependencies{Runtime: runtimeDeps, Env: os.Environ()}
	deps.IsHumanTerminal = allStandardStreamsAreTerminals
	deps.SetupHooks = func(context.Context) (any, error) {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, uxv1.Wrap(uxv1.CodeUpstream, "cannot locate home directory for agent setup", err)
		}
		return setuphooks.Install(home, portableInstalledCommand(), SkillMarkdown())
	}
	deps.Update = func(ctx context.Context, checkOnly bool) (any, uxv1.Meta, error) {
		result, err := updater.Run(ctx, checkOnly, updater.Config{
			CurrentVersion: buildinfo.Version,
			ManifestURL:    buildinfo.UpdateManifestURL,
			PublicKey:      buildinfo.UpdatePublicKey,
			IsTerminal:     deps.IsHumanTerminal,
			Stdin:          runtimeDeps.Stdin,
			Stderr:         runtimeDeps.Stderr,
		})
		return result, localMeta(), err
	}
	return deps
}

func portableInstalledCommand() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return ""
	}
	for _, name := range []string{"glab-axi", "glab-axi.exe"} {
		candidate, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		candidate, err = filepath.EvalSymlinks(candidate)
		if err == nil && candidate == executable {
			return name
		}
	}
	return ""
}

func allStandardStreamsAreTerminals() bool {
	return streamsAreTerminals(os.Stdin, os.Stdout, os.Stderr)
}

func streamsAreTerminals(files ...*os.File) bool {
	for _, file := range files {
		if file == nil || !term.IsTerminal(int(file.Fd())) {
			return false
		}
	}
	return true
}

func (d Dependencies) delegate() delegateClient {
	if d.NewDelegate != nil {
		return d.NewDelegate()
	}
	return glab.NewClient(glab.ClientConfig{
		Path: d.GlabPath, Dir: d.Runtime.Cwd, Env: d.Env,
		Stdin: d.Runtime.Stdin, Stdout: d.Runtime.Stdout, Stderr: d.Runtime.Stderr,
		IsTerminal: d.IsHumanTerminal, Keyring: d.Runtime.Keyring, SecureStoreProbe: securestore.Probe,
	})
}

type commandOutput struct {
	data any
	meta uxv1.Meta
}

func Run(ctx context.Context, args []string, deps Dependencies) int {
	parsedResult, err := Parse(args)
	if err != nil {
		return writeFailure(deps.Runtime.Stdout, deps.Runtime.Stderr, productFormatHint(args), err, uxv1.Meta{Complete: false})
	}
	if parsedResult.Help != "" {
		_, _ = io.WriteString(deps.Runtime.Stdout, parsedResult.Help)
		return 0
	}
	parsed := *parsedResult.Command
	result, err := execute(ctx, parsed, deps)
	if err != nil {
		meta := result.meta
		meta.Complete = false
		return writeFailure(deps.Runtime.Stdout, deps.Runtime.Stderr, parsed.Format, err, meta)
	}
	if err := output.WriteValue(deps.Runtime.Stdout, parsed.Format, uxv1.Success(result.data, result.meta)); err != nil {
		_, _ = io.WriteString(deps.Runtime.Stderr, "glab-axi: output failure\n")
		return 8
	}
	return 0
}

func writeFailure(stdout, stderr io.Writer, format output.Format, err error, meta uxv1.Meta) int {
	if writeErr := output.WriteValue(stdout, format, uxv1.Failure(err, meta)); writeErr != nil {
		_, _ = io.WriteString(stderr, "glab-axi: output failure\n")
		return 8
	}
	return uxv1.ExitCode(err)
}

func execute(parent context.Context, parsed Parsed, deps Dependencies) (commandOutput, error) {
	path := strings.Join(parsed.Definition.Path, " ")
	if path == "setup hooks" {
		if deps.SetupHooks == nil {
			return commandOutput{meta: localMeta()}, uxv1.NewError(uxv1.CodeInternal, "setup integration is unavailable")
		}
		data, err := deps.SetupHooks(parent)
		return commandOutput{data: data, meta: localMeta()}, err
	}
	if path == "update" {
		if deps.Update == nil {
			return commandOutput{meta: localMeta()}, uxv1.NewError(uxv1.CodeInternal, "update integration is unavailable")
		}
		data, meta, err := deps.Update(parent, parsed.Booleans["--check"])
		return commandOutput{data: data, meta: meta}, err
	}

	target, err := resolveTarget(parent, parsed, deps.Runtime.Cwd, deps.Runtime.LookupEnv)
	meta := uxv1.Meta{Backend: "official-glab", Host: target.Host, Repo: target.Repo, Complete: true, Limit: parsed.Limit}
	if err != nil {
		return commandOutput{meta: meta}, err
	}
	meta.Host, meta.Repo = target.Host, target.Repo
	client := deps.delegate()
	if path == "auth login" {
		// Human authorization follows only caller cancellation. The ordinary
		// noninteractive operation deadline is too short for a human prompt.
		version, err := client.Login(parent, target.Host)
		meta.UpstreamVersion = version
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		return commandOutput{data: map[string]any{"authenticated": true, "host": target.Host, "storage": "operating_system_keyring"}, meta: meta}, nil
	}

	timeout := limits.ShortOperation
	if parsed.Definition.Write {
		timeout = limits.WriteOperation
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	switch path {
	case "auth status":
		version, err := client.AuthStatus(ctx, target.Host)
		meta.UpstreamVersion = version
		if err != nil {
			return commandOutput{meta: meta}, err
		}
		return commandOutput{data: map[string]any{"authenticated": true, "host": target.Host}, meta: meta}, nil
	case "issue list":
		items, listMeta, err := fetchIssues(ctx, client, target, parsed.Limit)
		return listOutput("issues", items, meta, listMeta), err
	case "issue view":
		return executeIssueView(ctx, client, target, parsed, meta)
	case "mr list":
		items, listMeta, err := fetchMRs(ctx, client, target, parsed.Limit)
		return listOutput("mrs", items, meta, listMeta), err
	case "mr view":
		return executeMRView(ctx, client, target, parsed, meta)
	case "mr checks":
		return executeMRChecks(ctx, client, target, parsed, meta)
	case "mr diff":
		return executeMRDiff(ctx, client, target, parsed, meta)
	case "mr ensure", "mr create-or-update":
		return executeMREnsure(ctx, client, target, parsed, meta)
	case "pipeline list":
		items, listMeta, err := fetchPipelines(ctx, client, target, parsed.Limit)
		return listOutput("pipelines", items, meta, listMeta), err
	case "pipeline view":
		return executePipelineView(ctx, client, target, parsed, meta)
	case "job list":
		items, listMeta, err := fetchJobs(ctx, client, target, parsed)
		return listOutput("jobs", items, meta, listMeta), err
	case "job view":
		return executeJobView(ctx, client, target, parsed, meta)
	case "job trace":
		return executeJobTrace(ctx, client, target, parsed, meta)
	case "release list":
		items, listMeta, err := fetchReleases(ctx, client, target, parsed.Limit)
		return listOutput("releases", items, meta, listMeta), err
	case "release view":
		return executeReleaseView(ctx, client, target, parsed, meta)
	case "repo list":
		items, listMeta, err := fetchRepos(ctx, client, target, parsed.Limit)
		return listOutput("repositories", items, meta, listMeta), err
	case "repo view":
		return executeRepoView(ctx, client, target, parsed, meta)
	case "label list":
		items, listMeta, err := fetchLabels(ctx, client, target, parsed.Limit)
		return listOutput("labels", items, meta, listMeta), err
	case "search issues", "search mrs", "search repos", "search commits", "search code":
		items, listMeta, err := fetchSearch(ctx, client, target, parsed)
		return listOutput("results", items, meta, listMeta), err
	case "":
		return executeDashboard(ctx, client, target, parsed, meta)
	default:
		return commandOutput{meta: meta}, uxv1.NewError(uxv1.CodeUnsupported, "command is not implemented")
	}
}

type listState struct {
	complete        bool
	truncated       bool
	count           int
	reason          string
	upstreamVersion string
}

func listOutput(key string, items any, meta uxv1.Meta, state listState) commandOutput {
	meta.Complete = state.complete
	meta.Truncated = state.truncated
	meta.Count = state.count
	meta.Reason = state.reason
	meta.UpstreamVersion = state.upstreamVersion
	return commandOutput{data: map[string]any{key: items}, meta: meta}
}

func localMeta() uxv1.Meta {
	return uxv1.Meta{Backend: "local", Complete: true}
}

func positivePosition(parsed Parsed, label string) (int64, error) {
	if len(parsed.Positionals) != 1 {
		return 0, uxv1.NewError(uxv1.CodeValidation, label+" is required")
	}
	value, err := strconv.ParseInt(parsed.Positionals[0], 10, 64)
	if err != nil || value < 1 {
		return 0, uxv1.NewError(uxv1.CodeValidation, label+" must be a positive integer")
	}
	return value, nil
}

func productFormatHint(args []string) output.Format {
	for index, arg := range args {
		if arg == "--format=json" || arg == "--format" && index+1 < len(args) && args[index+1] == "json" {
			return output.JSON
		}
	}
	return output.TOON
}

func mergeMeta(base uxv1.Meta, state listState) uxv1.Meta {
	base.Complete = state.complete
	base.Truncated = state.truncated
	base.Count = state.count
	base.Reason = state.reason
	base.UpstreamVersion = state.upstreamVersion
	return base
}

func boundedDiff(body []byte) (string, bool, error) {
	if !utf8Valid(body) {
		return "", false, uxv1.NewError(uxv1.CodeUpstream, "official glab returned a non-UTF-8 diff")
	}
	return boundedText(string(body), "merge request diff", 1<<20, false)
}

func utf8Valid(body []byte) bool {
	return strings.ToValidUTF8(string(body), "\x00") == string(body)
}

func formatAction(action string) string {
	if action == "" {
		return "unchanged"
	}
	return action
}

func controlledMessage(format string, values ...any) error {
	return uxv1.NewError(uxv1.CodeValidation, fmt.Sprintf(format, values...))
}
