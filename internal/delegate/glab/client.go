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
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"glab-axi/internal/auth"
	"glab-axi/internal/contract/uxv1"
	"glab-axi/internal/limits"
)

const plaintextFallbackWarning = "The operating system keyring is unavailable. Storing credentials as plaintext in the configuration file."

var versionPattern = regexp.MustCompile(`^glab ([0-9]+\.[0-9]+\.[0-9]+) \(([0-9A-Za-z._+-]+)\)\n?$`)

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
	body, err := c.runCapture(ctx, invocation.args, invocation.host, invocation.maxStdout, false)
	if err != nil {
		return Response{}, err
	}
	if invocation.outputKind == outputJSON && !utf8.Valid(body) {
		return Response{}, uxv1.NewError(uxv1.CodeUpstream, "official glab returned non-UTF-8 JSON")
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
	if _, err := c.runCapture(ctx, []string{"auth", "status", "--hostname", host}, host, limits.MaxErrorReadBytes, false); err != nil {
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
	body, err := c.runCapturePath(ctx, path, []string{"version"}, "", 4096, false)
	if err != nil {
		return "", uxv1.Wrap(uxv1.CodeDependencyUnsupported, "cannot verify official glab version", err)
	}
	match := versionPattern.FindSubmatch(body)
	if match == nil {
		return "", uxv1.NewError(uxv1.CodeDependencyUnsupported, "official glab returned an unsupported version document")
	}
	version := string(match[1])
	if version != SupportedVersion {
		return "", uxv1.NewError(uxv1.CodeDependencyUnsupported, fmt.Sprintf("official glab %s is unsupported; require %s", version, SupportedVersion))
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

func (c *Client) runCapture(ctx context.Context, args []string, host string, maxStdout int, login bool) ([]byte, error) {
	c.mu.Lock()
	path := c.path
	c.mu.Unlock()
	if path == "" {
		return nil, uxv1.NewError(uxv1.CodeDependencyMissing, "official glab path is unavailable")
	}
	return c.runCapturePath(ctx, path, args, host, maxStdout, login)
}

func (c *Client) runCapturePath(ctx context.Context, path string, args []string, host string, maxStdout int, login bool) ([]byte, error) {
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Dir = c.config.Dir
	cmd.Env = sanitizedEnv(c.config.Env, host, login)
	cmd.Stdin = nil
	cmd.WaitDelay = 2 * time.Second
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, uxv1.Wrap(uxv1.CodeInternal, "cannot capture official glab output", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, uxv1.Wrap(uxv1.CodeInternal, "cannot capture official glab error output", err)
	}
	if err := cmd.Start(); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, uxv1.NewError(uxv1.CodeDependencyMissing, "official glab executable disappeared before execution")
		}
		return nil, uxv1.Wrap(uxv1.CodeUpstream, "cannot start official glab", err)
	}

	type capture struct {
		data     []byte
		overflow bool
		err      error
	}
	outCh := make(chan capture, 1)
	errCh := make(chan capture, 1)
	go func() {
		data, overflow, readErr := readBounded(stdout, maxStdout)
		if overflow {
			cancel()
		}
		outCh <- capture{data: data, overflow: overflow, err: readErr}
	}()
	go func() {
		data, overflow, readErr := readBounded(stderr, limits.MaxStderrBytes)
		if overflow {
			cancel()
		}
		errCh <- capture{data: data, overflow: overflow, err: readErr}
	}()
	waitErr := cmd.Wait()
	out, errOut := <-outCh, <-errCh
	if out.overflow || errOut.overflow {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "official glab output exceeded the safety limit")
	}
	if out.err != nil || errOut.err != nil {
		return nil, uxv1.NewError(uxv1.CodeUpstream, "cannot read official glab output")
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return nil, uxv1.Wrap(uxv1.CodeCanceled, "official glab operation was canceled", ctx.Err())
		}
		return nil, uxv1.Wrap(uxv1.CodeUpstream, "official glab operation timed out", ctx.Err())
	}
	if waitErr != nil {
		return nil, classifyChildFailure(errOut.data, waitErr)
	}
	return out.data, nil
}

func (c *Client) runLogin(ctx context.Context, args []string, host string) error {
	c.mu.Lock()
	path := c.path
	c.mu.Unlock()
	childCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd := exec.CommandContext(childCtx, path, args...)
	cmd.Dir = c.config.Dir
	cmd.Env = sanitizedEnv(c.config.Env, host, true)
	cmd.Stdin = c.config.Stdin
	cmd.Stdout = c.config.Stdout
	cmd.WaitDelay = 2 * time.Second
	pipe, err := cmd.StderrPipe()
	if err != nil {
		return uxv1.Wrap(uxv1.CodeInternal, "cannot monitor official glab login", err)
	}
	if err := cmd.Start(); err != nil {
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot start official glab login", err)
	}
	var fallback atomic.Bool
	done := make(chan error, 1)
	go func() {
		detector := &fallbackDetector{destination: c.config.Stderr, cancel: cancel, found: &fallback}
		_, copyErr := io.Copy(detector, pipe)
		done <- copyErr
	}()
	waitErr := cmd.Wait()
	copyErr := <-done
	if fallback.Load() {
		return uxv1.NewError(uxv1.CodeSafety, "official glab reported insecure credential storage; login was aborted")
	}
	if copyErr != nil {
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot monitor official glab login", copyErr)
	}
	if ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.Canceled) {
			return uxv1.Wrap(uxv1.CodeCanceled, "official glab login was canceled", ctx.Err())
		}
		return uxv1.Wrap(uxv1.CodeUpstream, "official glab login timed out", ctx.Err())
	}
	if waitErr != nil {
		return uxv1.Wrap(uxv1.CodeAuthentication, "official glab login did not complete", waitErr)
	}
	return nil
}

func readBounded(reader io.Reader, max int) ([]byte, bool, error) {
	var buffer bytes.Buffer
	chunk := make([]byte, 32<<10)
	overflow := false
	for {
		n, err := reader.Read(chunk)
		if n > 0 {
			remaining := max - buffer.Len()
			if remaining > 0 {
				write := n
				if write > remaining {
					write = remaining
				}
				_, _ = buffer.Write(chunk[:write])
			}
			if n > remaining {
				overflow = true
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return buffer.Bytes(), overflow, nil
			}
			return buffer.Bytes(), overflow, err
		}
	}
}

func classifyChildFailure(stderr []byte, cause error) error {
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

func sanitizedEnv(base []string, host string, login bool) []string {
	blocked := map[string]bool{
		"GLAB_CHECK_UPDATE":        true,
		"GLAB_DEBUG":               true,
		"GLAB_DEBUG_HTTP":          true,
		"GLAB_ENABLE_CI_AUTOLOGIN": true,
		"NO_COLOR":                 true,
		"COLOR_ENABLED":            true,
		"GITLAB_HOST":              true,
	}
	if login {
		blocked["CI"] = true
		blocked["GITLAB_CI"] = true
	} else {
		for _, name := range []string{"PAGER", "GLAB_PAGER", "EDITOR", "VISUAL", "BROWSER", "NO_PROMPT", "PROMPT_DISABLED"} {
			blocked[name] = true
		}
	}
	out := make([]string, 0, len(base)+12)
	for _, item := range base {
		name, _, ok := strings.Cut(item, "=")
		if ok && !blocked[name] {
			out = append(out, item)
		}
	}
	out = append(out,
		"GLAB_CHECK_UPDATE=false",
		"GLAB_DEBUG=false",
		"GLAB_DEBUG_HTTP=false",
		"GLAB_ENABLE_CI_AUTOLOGIN=false",
		"NO_COLOR=1",
		"COLOR_ENABLED=false",
		"GITLAB_HOST="+host,
	)
	if login {
		out = append(out, "CI=false", "GITLAB_CI=false")
	} else {
		out = append(out, "PAGER=", "GLAB_PAGER=", "EDITOR=", "VISUAL=", "BROWSER=", "NO_PROMPT=1", "PROMPT_DISABLED=1")
	}
	return out
}

type fallbackDetector struct {
	destination io.Writer
	cancel      context.CancelFunc
	found       *atomic.Bool
	window      string
}

func (d *fallbackDetector) Write(p []byte) (int, error) {
	combined := d.window + string(p)
	if strings.Contains(combined, plaintextFallbackWarning) {
		d.found.Store(true)
		d.cancel()
	}
	keep := len(plaintextFallbackWarning) - 1
	if keep > len(combined) {
		keep = len(combined)
	}
	d.window = combined[len(combined)-keep:]
	if d.destination == nil {
		return len(p), nil
	}
	written, err := d.destination.Write(p)
	if err != nil {
		return written, err
	}
	if written != len(p) {
		return written, io.ErrShortWrite
	}
	return len(p), nil
}
