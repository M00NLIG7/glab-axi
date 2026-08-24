package glab

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gl-axi/internal/auth"
	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/limits"

	"golang.org/x/term"
)

const plaintextFallbackWarning = "The operating system keyring is unavailable. Storing credentials as plaintext in the configuration file."

var versionPattern = regexp.MustCompile(`^glab ([0-9]+\.[0-9]+\.[0-9]+) \(([0-9A-Za-z._+-]+)\)\n?$`)

// Only pinned wrapper framing can prove a status; an unframed provider body
// may still influence the broad safe category below, but never StatusCode.
var childHTTPRejectionPattern = regexp.MustCompile(`(?im)(?:\bhttp(?:\s+status)?(?:\s+code)?\s*[:=]?\s*|\bglab:\s*|\bapi\s+(?:request|call)\s+(?:failed|error)\s*:\s*|\b(?:get|post|put|patch|delete|head)\s+(?:"https://[^"\s]+"|https://[^\s]+):\s*)(400|401|403|404|405|406|409|422|429)\b`)

type ClientConfig struct {
	Path             string
	Dir              string
	Env              []string
	Stdin            io.Reader
	Stdout           io.Writer
	Stderr           io.Writer
	IsTerminal       func() bool
	Keyring          auth.Keyring
	SecureStoreProbe func(context.Context) error
	LookPath         func(string) (string, error)
}

type Client struct {
	config ClientConfig

	mu      sync.Mutex
	path    string
	version string
}

type Response struct {
	Body            []byte
	UpstreamVersion string
	Write           bool
}

func NewClient(config ClientConfig) *Client {
	if config.Env == nil {
		config.Env = os.Environ()
	}
	if config.Stdin == nil {
		config.Stdin = os.Stdin
	}
	if config.Stdout == nil {
		config.Stdout = os.Stdout
	}
	if config.Stderr == nil {
		config.Stderr = os.Stderr
	}
	if config.LookPath == nil {
		config.LookPath = exec.LookPath
	}
	return &Client{config: config}
}

func (c *Client) Version(ctx context.Context) (string, error) {
	return c.ensureVersion(ctx)
}

func (c *Client) Do(ctx context.Context, request Request) (Response, error) {
	invocation, err := build(request)
	if err != nil {
		return Response{}, err
	}
	version, err := c.ensureVersion(ctx)
	if err != nil {
		return Response{}, err
	}
	body, err := c.runCapture(ctx, invocation.args, invocation.host, invocation.maxStdout, invocation.write, false, request.Operation)
	if err != nil {
		return Response{UpstreamVersion: version, Write: invocation.write}, err
	}
	if invocation.outputKind == outputJSON && !utf8.Valid(body) {
		return Response{UpstreamVersion: version, Write: invocation.write}, uxv1.NewError(uxv1.CodeUpstream, "official glab returned non-UTF-8 JSON")
	}
	if request.Operation == OpMRView {
		body, err = normalizeMRViewResponse(body)
		if err != nil {
			return Response{UpstreamVersion: version, Write: invocation.write}, err
		}
	}
	return Response{Body: body, UpstreamVersion: version, Write: invocation.write}, nil
}

func (c *Client) AuthStatus(ctx context.Context, host string) (string, error) {
	if err := validateHost(host); err != nil {
		return "", err
	}
	version, err := c.ensureVersion(ctx)
	if err != nil {
		return "", err
	}
	if _, err := c.runCapture(ctx, []string{"auth", "status", "--hostname", host}, host, limits.MaxErrorReadBytes, false, false, ""); err != nil {
		if uxv1.AsError(err).Code == uxv1.CodeUpstream {
			return "", uxv1.Wrap(uxv1.CodeAuthentication, "official glab is not authenticated for the selected host", err)
		}
		return "", err
	}
	return version, nil
}

// Login attaches official glab only to an explicitly human terminal. A
// non-secret keyring probe and an exact pinned-warning kill switch prevent the
// upstream CLI's documented plaintext fallback from reaching token acquisition.
func (c *Client) Login(ctx context.Context, host string) (string, error) {
	if err := validateHost(host); err != nil {
		return "", err
	}
	if c.config.IsTerminal == nil || !c.config.IsTerminal() {
		return "", uxv1.NewError(uxv1.CodeInteractiveRequired, "human authentication requires a real terminal")
	}
	if _, _, _, err := c.loginTerminalFiles(); err != nil {
		return "", err
	}
	probe := c.config.SecureStoreProbe
	if probe == nil {
		probe = func(ctx context.Context) error { return auth.Probe(ctx, c.config.Keyring) }
	}
	if err := probe(ctx); err != nil {
		return "", uxv1.Wrap(uxv1.CodeSafety, "secure credential storage is unavailable; refusing official glab plaintext fallback", err)
	}
	version, err := c.ensureVersion(ctx)
	if err != nil {
		return "", err
	}
	if err := c.runLogin(ctx, []string{"auth", "login", "--hostname", host}, host); err != nil {
		return "", err
	}
	return version, nil
}

func (c *Client) loginTerminalFiles() (stdin, stdout, stderr *os.File, err error) {
	stdin, stdinOK := c.config.Stdin.(*os.File)
	stdout, stdoutOK := c.config.Stdout.(*os.File)
	stderr, stderrOK := c.config.Stderr.(*os.File)
	if !stdinOK || !stdoutOK || !stderrOK ||
		!term.IsTerminal(int(stdin.Fd())) ||
		!term.IsTerminal(int(stdout.Fd())) ||
		!term.IsTerminal(int(stderr.Fd())) {
		return nil, nil, nil, uxv1.NewError(uxv1.CodeInteractiveRequired, "human authentication requires three real terminal streams")
	}
	return stdin, stdout, stderr, nil
}

func (c *Client) ensureVersion(parent context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.version != "" {
		return c.version, nil
	}
	path, err := c.resolvePath()
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	body, err := c.runCapturePath(ctx, path, []string{"version"}, "", 4096, false, false, "")
	if err != nil {
		return "", uxv1.Wrap(uxv1.CodeDependencyUnsupported, "cannot verify official glab version", err)
	}
	match := versionPattern.FindSubmatch(body)
	if match == nil {
		return "", uxv1.NewError(uxv1.CodeDependencyUnsupported, "official glab returned an unsupported version document")
	}
	version, build := string(match[1]), string(match[2])
	if version != SupportedVersion || build != SupportedBuild {
		return "", uxv1.NewError(uxv1.CodeDependencyUnsupported, fmt.Sprintf("official glab build is unsupported; require %s (%s)", SupportedVersion, SupportedBuild))
	}
	c.path, c.version = path, version
	return version, nil
}

func (c *Client) resolvePath() (string, error) {
	path := c.config.Path
	if path == "" {
		resolved, err := c.config.LookPath("glab")
		if err != nil {
			return "", uxv1.NewError(uxv1.CodeDependencyMissing, "official glab 1.112.0 was not found on PATH")
		}
		path = resolved
	}
	if !filepath.IsAbs(path) {
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", uxv1.Wrap(uxv1.CodeDependencyUnsupported, "cannot resolve official glab executable", err)
		}
		path = absolute
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", uxv1.Wrap(uxv1.CodeDependencyUnsupported, "cannot inspect official glab executable", err)
	}
	info, err := os.Stat(resolved)
	if err != nil || !info.Mode().IsRegular() || runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return "", uxv1.NewError(uxv1.CodeDependencyUnsupported, "official glab path is not an executable regular file")
	}
	return resolved, nil
}

func (c *Client) runCapture(ctx context.Context, args []string, host string, maxStdout int, write, login bool, operation Operation) ([]byte, error) {
	c.mu.Lock()
	path := c.path
	c.mu.Unlock()
	if path == "" {
		return nil, uxv1.NewError(uxv1.CodeDependencyMissing, "official glab path is unavailable")
	}
	return c.runCapturePath(ctx, path, args, host, maxStdout, write, login, operation)
}

func (c *Client) runCapturePath(ctx context.Context, path string, args []string, host string, maxStdout int, write, login bool, operation Operation) ([]byte, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Dir = c.config.Dir
	cmd.Env = sanitizedEnv(c.config.Env, host, login)
	cmd.Stdin = nil
	cmd.WaitDelay = 2 * time.Second
	stdout := &boundedCapture{max: maxStdout, cancel: cancel}
	stderr := &boundedCapture{max: limits.MaxStderrBytes, cancel: cancel}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, contextFailure(ctx, "official glab operation")
		}
		if errors.Is(err, os.ErrNotExist) {
			return nil, uxv1.NewError(uxv1.CodeDependencyMissing, "official glab executable disappeared before execution")
		}
		return nil, uxv1.Wrap(uxv1.CodeUpstream, "cannot start official glab", err)
	}
	waitErr := cmd.Wait()
	if stdout.overflow || stderr.overflow {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab output exceeded the safety limit")
	}
	if ctx.Err() != nil {
		return nil, contextFailure(ctx, "official glab operation")
	}
	if waitErr != nil {
		return nil, classifyChildFailure(stderr.buffer.Bytes(), waitErr, write, operation)
	}
	return stdout.buffer.Bytes(), nil
}

func contextFailure(ctx context.Context, operation string) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return uxv1.Wrap(uxv1.CodeCanceled, operation+" was canceled", ctx.Err())
	}
	return uxv1.Wrap(uxv1.CodeUpstream, operation+" timed out", ctx.Err())
}

type boundedCapture struct {
	buffer   bytes.Buffer
	max      int
	overflow bool
	cancel   context.CancelFunc
}

func (c *boundedCapture) Write(p []byte) (int, error) {
	original := len(p)
	remaining := c.max - c.buffer.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = c.buffer.Write(p)
	}
	if original > remaining {
		c.overflow = true
		c.cancel()
	}
	return original, nil
}

func classifyChildFailure(stderr []byte, cause error, write bool, operation Operation) error {
	if match := childHTTPRejectionPattern.FindSubmatch(stderr); len(match) == 2 {
		status, parseErr := strconv.Atoi(string(match[1]))
		if parseErr == nil {
			if write {
				if rejection, ok := operationHTTPRejection(operation, status); ok {
					rejection.Cause = cause
					return rejection
				}
				return uxv1.Wrap(uxv1.CodeUpstream, "official glab operation failed", cause)
			}
			switch status {
			case 401:
				return uxv1.Wrap(uxv1.CodeAuthentication, "official glab authentication failed", cause)
			case 403:
				return uxv1.Wrap(uxv1.CodeForbidden, "official glab operation was forbidden", cause)
			case 404:
				return uxv1.Wrap(uxv1.CodeNotFound, "GitLab resource was not found", cause)
			case 429:
				return uxv1.Wrap(uxv1.CodeRateLimited, "GitLab rate limit was reached", cause)
			default:
				return uxv1.Wrap(uxv1.CodeUpstream, "official glab operation failed", cause)
			}
		}
	}

	text := strings.ToLower(string(stderr))
	switch {
	case strings.Contains(text, "401"), strings.Contains(text, "authentication"), strings.Contains(text, "authenticate"):
		return uxv1.Wrap(uxv1.CodeAuthentication, "official glab authentication failed", cause)
	case strings.Contains(text, "403"), strings.Contains(text, "forbidden"), strings.Contains(text, "permission"):
		return uxv1.Wrap(uxv1.CodeForbidden, "official glab operation was forbidden", cause)
	case strings.Contains(text, "404"), strings.Contains(text, "not found"), strings.Contains(text, "does not exist"):
		return uxv1.Wrap(uxv1.CodeNotFound, "GitLab resource was not found", cause)
	case strings.Contains(text, "429"), strings.Contains(text, "rate limit"):
		return uxv1.Wrap(uxv1.CodeRateLimited, "GitLab rate limit was reached", cause)
	default:
		return uxv1.Wrap(uxv1.CodeUpstream, "official glab operation failed", cause)
	}
}

func operationHTTPRejection(operation Operation, status int) (*uxv1.Error, bool) {
	if operation == OpMRMerge && (status == 405 || status == 406) {
		return &uxv1.Error{
			Code:       uxv1.CodeConflict,
			Message:    fmt.Sprintf("GitLab refused to merge the merge request (HTTP %d)", status),
			StatusCode: status,
		}, true
	}
	return uxv1.NewHTTPRejection(status)
}

func sanitizedEnv(base []string, host string, login bool) []string {
	blocked := map[string]bool{
		"GLAB_CHECK_UPDATE":        true,
		"GLAB_DEBUG":               true,
		"GLAB_DEBUG_HTTP":          true,
		"GLAB_ENABLE_CI_AUTOLOGIN": true,
		"GLAB_SEND_TELEMETRY":      true,
		"NO_COLOR":                 true,
		"COLOR_ENABLED":            true,
		"GITLAB_HOST":              true,
	}
	if login {
		for _, name := range []string{"CI", "GITLAB_CI"} {
			blocked[name] = true
		}
	}
	if login || host == "" {
		for _, name := range []string{"GL_AXI_TOKEN", "GLAB_AXI_TOKEN", "GITLAB_TOKEN", "GITLAB_ACCESS_TOKEN", "OAUTH_TOKEN", "CI_JOB_TOKEN"} {
			blocked[name] = true
		}
	}
	if !login {
		for _, name := range []string{"PAGER", "GLAB_PAGER", "EDITOR", "VISUAL", "BROWSER", "TERM", "NO_PROMPT", "PROMPT_DISABLED", "GLAB_NO_PROMPT"} {
			blocked[name] = true
		}
	}
	out := make([]string, 0, len(base)+12)
	for _, item := range base {
		name, _, ok := strings.Cut(item, "=")
		if ok && !blocked[strings.ToUpper(name)] {
			out = append(out, item)
		}
	}
	out = append(out,
		"GLAB_CHECK_UPDATE=false",
		"GLAB_DEBUG=false",
		"GLAB_DEBUG_HTTP=false",
		"GLAB_ENABLE_CI_AUTOLOGIN=false",
		"GLAB_SEND_TELEMETRY=false",
		"NO_COLOR=1",
		"COLOR_ENABLED=false",
		"GITLAB_HOST="+host,
	)
	if login {
		out = append(out, "CI=false", "GITLAB_CI=false")
	} else {
		out = append(out, "PAGER=", "GLAB_PAGER=", "EDITOR=", "VISUAL=", "BROWSER=", "TERM=dumb", "NO_PROMPT=1", "PROMPT_DISABLED=1", "GLAB_NO_PROMPT=1")
	}
	return out
}
