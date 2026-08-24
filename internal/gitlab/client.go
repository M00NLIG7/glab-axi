// Package gitlab is a typed, allowlisted GitLab REST client. It intentionally
// has no generic API method in its public surface.
package gitlab

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
	"gl-axi/internal/contract/v1"
	"gl-axi/internal/limits"
	"gl-axi/internal/redact"
)

const userAgent = "gl-axi/0.2"

type SleepFunc func(context.Context, time.Duration) error

type Client struct {
	host     config.ResolvedHost
	cred     auth.Credential
	http     *http.Client
	redactor *redact.Redactor
	sleep    SleepFunc
}

type Options struct {
	HTTPClient *http.Client
	Sleep      SleepFunc
}

func NewClient(host config.ResolvedHost, cred auth.Credential, opts Options) (*Client, error) {
	if err := auth.ValidateToken(cred.Value); err != nil {
		return nil, err
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		transport, err := transportFor(host)
		if err != nil {
			return nil, err
		}
		httpClient = &http.Client{Transport: transport}
	}
	copyClient := *httpClient
	copyClient.CheckRedirect = redirectPolicy(host)
	if opts.Sleep == nil {
		opts.Sleep = sleepContext
	}
	return &Client{host: host, cred: cred, http: &copyClient, redactor: redact.New(cred.Value), sleep: opts.Sleep}, nil
}

func transportFor(host config.ResolvedHost) (*http.Transport, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if host.CABundle != "" {
		pool, err := loadCABundle(host.CABundle)
		if err != nil {
			return nil, err
		}
		tlsConfig.RootCAs = pool
	}
	transport := &http.Transport{
		Proxy:                  http.ProxyFromEnvironment,
		DialContext:            (&net.Dialer{Timeout: limits.ConnectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:      true,
		TLSClientConfig:        tlsConfig,
		TLSHandshakeTimeout:    limits.TLSHandshake,
		ResponseHeaderTimeout:  limits.HeaderTimeout,
		ExpectContinueTimeout:  time.Second,
		IdleConnTimeout:        30 * time.Second,
		MaxIdleConns:           16,
		MaxIdleConnsPerHost:    4,
		MaxConnsPerHost:        4,
		MaxResponseHeaderBytes: 64 << 10,
	}
	if host.ProxyDisabled {
		transport.Proxy = nil
	}
	return transport, nil
}

func loadCABundle(path string) (*x509.CertPool, error) {
	if !filepath.IsAbs(path) {
		return nil, v1.NewError(v1.CodeValidation, "CA bundle path must be absolute")
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > 1<<20 {
		return nil, v1.NewError(v1.CodeSafety, "CA bundle must be a bounded regular file, not a symlink")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return nil, v1.Wrap(v1.CodeUpstream, "cannot read CA bundle", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, v1.NewError(v1.CodeValidation, "CA bundle contains no certificates")
	}
	return pool, nil
}

func redirectPolicy(host config.ResolvedHost) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}
		if len(via) >= 3 {
			return v1.NewError(v1.CodeSafety, "GitLab redirect limit exceeded")
		}
		if via[0].Method != http.MethodGet && via[0].Method != http.MethodHead {
			return v1.NewError(v1.CodeSafety, "GitLab mutation redirect refused")
		}
		if req.URL.Scheme != "https" || req.URL.User != nil || !host.Authority.SameAPIOrigin(req.URL) || !host.Authority.WithinAPI(req.URL) {
			return v1.NewError(v1.CodeSafety, "cross-origin or unsafe GitLab redirect refused")
		}
		if req.URL.Path != via[0].URL.Path || req.URL.RawQuery != via[0].URL.RawQuery {
			return v1.NewError(v1.CodeSafety, "GitLab redirect changed the typed endpoint")
		}
		return nil
	}
}

func (c *Client) getJSON(ctx context.Context, endpoint *url.URL, out any) (http.Header, int, error) {
	body, header, err := c.do(ctx, http.MethodGet, endpoint, nil, limits.MaxJSONPageBytes)
	if err != nil {
		return nil, 0, err
	}
	if !isJSONContentType(header.Get("Content-Type")) {
		return nil, len(body), v1.NewError(v1.CodeUpstream, "GitLab returned a non-JSON response")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(out); err != nil {
		return nil, len(body), v1.Wrap(v1.CodeUpstream, "GitLab returned malformed JSON", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, len(body), v1.NewError(v1.CodeUpstream, "GitLab returned trailing JSON data")
	}
	return header, len(body), nil
}

func (c *Client) sendJSON(ctx context.Context, method string, endpoint *url.URL, input, out any) error {
	encoded, err := json.Marshal(input)
	if err != nil {
		return v1.Wrap(v1.CodeInternal, "cannot encode GitLab request", err)
	}
	if len(encoded) > limits.MaxDescriptionBytes+limits.MaxTitleBytes+4096 {
		return v1.NewError(v1.CodeValidation, "GitLab request exceeds the input limit")
	}
	body, header, err := c.do(ctx, method, endpoint, encoded, limits.MaxJSONPageBytes)
	if err != nil {
		return err
	}
	if !isJSONContentType(header.Get("Content-Type")) {
		return v1.NewError(v1.CodeUpstream, "GitLab returned a non-JSON response")
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	if err := dec.Decode(out); err != nil {
		return v1.Wrap(v1.CodeUpstream, "GitLab returned malformed JSON", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); !errors.Is(err, io.EOF) {
		return v1.NewError(v1.CodeUpstream, "GitLab returned trailing JSON data")
	}
	return nil
}

func (c *Client) getTrace(ctx context.Context, endpoint *url.URL) ([]byte, bool, error) {
	for attempt := 0; attempt < 3; attempt++ {
		request, err := c.newRequest(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, false, err
		}
		response, err := c.http.Do(request)
		if err != nil {
			if attempt < 2 && retryableNetwork(ctx, err) {
				if err := c.sleep(ctx, retryDelay(attempt)); err != nil {
					return nil, false, c.requestError(ctx, http.MethodGet, err)
				}
				continue
			}
			return nil, false, c.requestError(ctx, http.MethodGet, err)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			ring := newTailBuffer(limits.MaxTraceBytes)
			_, copyErr := io.Copy(ring, response.Body)
			response.Body.Close()
			if copyErr != nil {
				return nil, ring.truncated, v1.Wrap(v1.CodeUpstream, "cannot read GitLab job trace", copyErr)
			}
			return ring.Bytes(), ring.truncated, nil
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, limits.MaxErrorReadBytes))
		response.Body.Close()
		retryable := response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode == 502 || response.StatusCode == 503 || response.StatusCode == 504
		httpErr := v1.HTTPError(response.StatusCode, retryable)
		if attempt >= 2 || !retryable {
			return nil, false, httpErr
		}
		delay := retryDelay(attempt)
		if response.StatusCode == 429 {
			delay = retryAfter(response.Header.Get("Retry-After"), time.Now())
			if delay > limits.MaxRetryAfter {
				return nil, false, httpErr
			}
		}
		if err := c.sleep(ctx, delay); err != nil {
			return nil, false, c.requestError(ctx, http.MethodGet, err)
		}
	}
	return nil, false, v1.NewError(v1.CodeUpstream, "GitLab trace retry limit exceeded")
}

func (c *Client) do(ctx context.Context, method string, endpoint *url.URL, body []byte, max int64) ([]byte, http.Header, error) {
	attempts := 1
	if method == http.MethodGet || method == http.MethodHead {
		attempts = 3
	}
	var last error
	for attempt := 0; attempt < attempts; attempt++ {
		request, err := c.newRequest(ctx, method, endpoint, body)
		if err != nil {
			return nil, nil, err
		}
		response, err := c.http.Do(request)
		if err != nil {
			last = c.requestError(ctx, method, err)
			if attempt+1 < attempts && retryableNetwork(ctx, err) {
				if err := c.sleep(ctx, retryDelay(attempt)); err != nil {
					return nil, nil, c.requestError(ctx, method, err)
				}
				continue
			}
			return nil, nil, last
		}
		readLimit := max
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			readLimit = limits.MaxErrorReadBytes
		}
		data, tooLarge, readErr := readBounded(response.Body, readLimit)
		response.Body.Close()
		if readErr != nil {
			return nil, nil, v1.Wrap(v1.CodeUpstream, "cannot read GitLab response", readErr)
		}
		if response.StatusCode >= 200 && response.StatusCode < 300 {
			if tooLarge {
				return nil, nil, v1.NewError(v1.CodeUpstream, "GitLab response exceeds the size limit")
			}
			return data, response.Header.Clone(), nil
		}
		retryable := response.StatusCode == 408 || response.StatusCode == 429 || response.StatusCode == 502 || response.StatusCode == 503 || response.StatusCode == 504
		httpErr := v1.HTTPError(response.StatusCode, retryable)
		if method != http.MethodGet && method != http.MethodHead && (response.StatusCode == 409 || response.StatusCode == 422 || response.StatusCode >= 500) {
			httpErr.Ambiguous = true
		}
		last = httpErr
		if attempt+1 >= attempts || !retryable {
			return nil, nil, httpErr
		}
		delay := retryDelay(attempt)
		if response.StatusCode == 429 {
			delay = retryAfter(response.Header.Get("Retry-After"), time.Now())
			if delay > limits.MaxRetryAfter {
				return nil, nil, httpErr
			}
		}
		if err := c.sleep(ctx, delay); err != nil {
			return nil, nil, c.requestError(ctx, method, err)
		}
	}
	return nil, nil, last
}

func (c *Client) newRequest(ctx context.Context, method string, endpoint *url.URL, body []byte) (*http.Request, error) {
	if endpoint == nil || endpoint.Scheme != "https" || endpoint.User != nil || !c.host.Authority.SameAPIOrigin(endpoint) || !c.host.Authority.WithinAPI(endpoint) {
		return nil, v1.NewError(v1.CodeSafety, "refusing request outside configured GitLab API authority")
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint.String(), reader)
	if err != nil {
		return nil, v1.Wrap(v1.CodeValidation, "cannot construct GitLab request", err)
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("User-Agent", userAgent)
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.cred.Kind == auth.OAuthToken {
		request.Header.Set("Authorization", "Bearer "+c.cred.Value)
	} else {
		request.Header.Set("PRIVATE-TOKEN", c.cred.Value)
	}
	return request, nil
}

func (c *Client) requestError(ctx context.Context, method string, err error) error {
	if errors.Is(ctx.Err(), context.Canceled) {
		return v1.Wrap(v1.CodeCanceled, "operation canceled", context.Canceled)
	}
	if errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) {
		e := v1.Wrap(v1.CodeUpstream, "GitLab request timed out", err)
		e.Retryable = method == http.MethodGet || method == http.MethodHead
		e.Ambiguous = method == http.MethodPost || method == http.MethodPut
		return e
	}
	var typed *v1.Error
	if errors.As(err, &typed) {
		return typed
	}
	e := v1.Wrap(v1.CodeUpstream, "GitLab network request failed", err)
	e.Retryable = method == http.MethodGet || method == http.MethodHead
	e.Ambiguous = method == http.MethodPost || method == http.MethodPut
	return e
}

func readBounded(reader io.Reader, max int64) ([]byte, bool, error) {
	data, err := io.ReadAll(io.LimitReader(reader, max+1))
	if err != nil {
		return nil, false, err
	}
	if int64(len(data)) > max {
		return data[:max], true, nil
	}
	return data, false, nil
}

func isJSONContentType(value string) bool {
	value = strings.ToLower(strings.TrimSpace(strings.Split(value, ";")[0]))
	return value == "application/json" || strings.HasSuffix(value, "+json")
}

func retryableNetwork(ctx context.Context, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	var policyErr *v1.Error
	if errors.As(err, &policyErr) {
		return false
	}
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var certificateError x509.CertificateInvalidError
	if errors.As(err, &unknownAuthority) || errors.As(err, &hostnameError) || errors.As(err, &certificateError) {
		return false
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func retryDelay(attempt int) time.Duration {
	if attempt == 0 {
		return 250 * time.Millisecond
	}
	return time.Second
}

func retryAfter(value string, now time.Time) time.Duration {
	if seconds, err := strconv.Atoi(strings.TrimSpace(value)); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}
	if when, err := http.ParseTime(value); err == nil && when.After(now) {
		return when.Sub(now)
	}
	return retryDelay(0)
}

func sleepContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
