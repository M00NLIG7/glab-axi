// Package productnative is the explicit product-native HTTP boundary. Feature
// handlers own operation authority; this package owns one native identity,
// configured API binding, no redirects/retries, and bounded raw responses.
package productnative

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"gl-axi/internal/auth"
	"gl-axi/internal/config"
	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/gitlab"
	"gl-axi/internal/safeurl"
)

const (
	MaxBytes       = int64(8 << 20)
	MaxStreamBytes = int64(64 << 20)
	MaxRequests    = 64
	Lifetime       = 45 * time.Second
)

type Options struct {
	Host       string
	ConfigPath string
	LookupEnv  auth.LookupEnv
	Keyring    auth.Keyring
	HTTPClient *http.Client
}

type Request struct {
	Method   string
	Path     string
	Query    url.Values
	Headers  http.Header
	Body     []byte
	MaxBytes int64
}

type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
	Bytes      int64
}

type Client struct {
	host       config.ResolvedHost
	credential auth.Credential
	http       *http.Client
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	requests   int
	buffered   int64
	streamed   int64
}

func Open(parent context.Context, opts Options) (*Client, error) {
	if err := safeurl.ValidateHost(opts.Host); err != nil {
		return nil, uxv1.NewError(uxv1.CodeValidation, "explicit native hostname is invalid")
	}
	cfg, err := config.Load(opts.ConfigPath)
	if err != nil {
		return nil, uxv1.AsError(err)
	}
	host, err := cfg.Resolve(opts.Host)
	if err != nil {
		return nil, uxv1.AsError(err)
	}
	ctx, cancel := context.WithTimeout(parent, Lifetime)
	resolver := auth.Resolver{Lookup: opts.LookupEnv, Keyring: opts.Keyring}
	credential, err := resolver.Resolve(ctx, host)
	if err != nil {
		cancel()
		return nil, uxv1.AsError(err)
	}
	client, err := gitlab.NewProductHTTPClient(host, opts.HTTPClient)
	if err != nil {
		cancel()
		return nil, uxv1.AsError(err)
	}
	return &Client{host: host, credential: credential, http: client, ctx: ctx, cancel: cancel}, nil
}

func (c *Client) Close() { c.cancel() }

// Host returns an independent authority snapshot, not mutable client state.
func (c *Client) Host() config.ResolvedHost {
	host := c.host
	api, web := *host.Authority.API, *host.Authority.Web
	host.Authority.API, host.Authority.Web = &api, &web
	return host
}

func (c *Client) Do(ctx context.Context, request Request) (Response, error) {
	var body bytes.Buffer
	response, err := c.transfer(ctx, request, &body, false)
	if err == nil {
		if c.containsCredential(body.Bytes()) || c.jsonContainsCredential(body.Bytes()) {
			return Response{StatusCode: response.StatusCode}, uxv1.NewError(uxv1.CodeSafety, "native response contained credential material")
		}
		response.Body = body.Bytes()
	}
	return response, err
}

// Stream only writes into private staging owned by the operation. A failure
// may leave bytes in that writer; the caller must not publish them.
func (c *Client) Stream(ctx context.Context, request Request, dst io.Writer) (Response, error) {
	if request.Method != http.MethodGet || dst == nil {
		return Response{}, uxv1.NewError(uxv1.CodeValidation, "native streaming requires GET and a private staging writer")
	}
	return c.transfer(ctx, request, dst, true)
}

func (c *Client) transfer(parent context.Context, input Request, dst io.Writer, stream bool) (Response, error) {
	endpoint, err := c.validate(input, stream)
	if err != nil {
		return Response{}, err
	}
	// Serialize one operation's budget and transfers. Independent operations
	// use separate clients and do not share identity or counters.
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ctx.Err() != nil {
		return Response{}, contextError(c.ctx.Err())
	}
	if parent.Err() != nil {
		return Response{}, contextError(parent.Err())
	}
	remaining := MaxBytes - c.buffered
	if stream {
		remaining = MaxStreamBytes - c.streamed
	}
	if remaining <= 0 || c.requests >= MaxRequests {
		return Response{}, uxv1.NewError(uxv1.CodeSafety, "native operation budget exceeded")
	}
	bound := min(input.MaxBytes, remaining)
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(c.ctx, cancel)
	defer func() { stop(); cancel() }()
	request, err := http.NewRequestWithContext(ctx, input.Method, endpoint.String(), bytes.NewReader(input.Body))
	if err != nil {
		return Response{}, uxv1.NewError(uxv1.CodeValidation, "invalid native request")
	}
	request.Header = input.Headers.Clone()
	if request.Header == nil {
		request.Header = make(http.Header)
	}
	request.Header.Set("User-Agent", "gl-axi/product-native")
	request.Header.Set("Accept-Encoding", "identity")
	if c.credential.Kind == auth.OAuthToken {
		request.Header.Set("Authorization", "Bearer "+c.credential.Value)
	} else {
		request.Header.Set("Private-Token", c.credential.Value)
	}
	// No body replay, including by an injected HTTP transport.
	request.GetBody = nil
	c.requests++
	raw, err := c.http.Do(request)
	if err != nil {
		if c.ctx.Err() != nil {
			return Response{}, contextError(c.ctx.Err())
		}
		if ctx.Err() != nil {
			return Response{}, contextError(ctx.Err())
		}
		return Response{}, uxv1.NewError(uxv1.CodeUpstream, "native GitLab request failed; outcome may be unknown")
	}
	defer raw.Body.Close()
	response := Response{StatusCode: raw.StatusCode}
	if raw.StatusCode >= 300 && raw.StatusCode <= 399 {
		return response, &uxv1.Error{Code: uxv1.CodeSafety, Message: "native GitLab redirect refused before a second request", StatusCode: raw.StatusCode}
	}
	if raw.StatusCode < 200 || raw.StatusCode >= 300 {
		if rejected, ok := uxv1.NewHTTPRejection(raw.StatusCode); ok {
			return response, rejected
		}
		return response, &uxv1.Error{Code: uxv1.CodeUpstream, Message: "native GitLab returned an unsuccessful response", StatusCode: raw.StatusCode}
	}
	response.Header, err = c.responseHeaders(raw.Header)
	if err != nil {
		return response, err
	}
	if raw.Uncompressed || raw.Header.Get("Content-Encoding") != "" && raw.Header.Get("Content-Encoding") != "identity" {
		return response, uxv1.NewError(uxv1.CodeUpstream, "native GitLab returned an unsupported content encoding")
	}
	if input.Method != http.MethodHead && raw.ContentLength > bound {
		return response, uxv1.NewError(uxv1.CodeUpstream, "native GitLab response exceeds the byte limit")
	}
	// Scan for exact credential bytes across chunk boundaries even for binary
	// downloads; a rejected transfer is never published by the feature.
	scanner := &credentialWriter{dst: dst, secrets: c.credentialForms()}
	n, copyErr := io.Copy(scanner, io.LimitReader(raw.Body, bound+1))
	response.Bytes = n
	if stream {
		c.streamed += n
	} else {
		c.buffered += n
	}
	if c.ctx.Err() != nil {
		return response, contextError(c.ctx.Err())
	}
	if ctx.Err() != nil {
		return response, contextError(ctx.Err())
	}
	if copyErr != nil || n > bound || input.Method != http.MethodHead && raw.ContentLength >= 0 && raw.ContentLength != n {
		return response, uxv1.NewError(uxv1.CodeUpstream, "native GitLab transfer failed or exceeded its bound; outcome may be unknown")
	}
	return response, nil
}

func (c *Client) validate(r Request, stream bool) (*url.URL, error) {
	invalid := func() (*url.URL, error) {
		return nil, uxv1.NewError(uxv1.CodeValidation, "invalid native request route, method, headers, or bounds")
	}
	switch r.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
	default:
		return invalid()
	}
	maximum := MaxBytes
	if stream {
		maximum = MaxStreamBytes
	}
	if r.MaxBytes <= 0 || r.MaxBytes > maximum || int64(len(r.Body)) > MaxBytes || (r.Method == "GET" || r.Method == "HEAD") && len(r.Body) != 0 {
		return invalid()
	}
	if r.Path == "" || len(r.Path) > 4096 || strings.HasPrefix(r.Path, "/") || strings.ContainsAny(r.Path, "\\?#") || strings.Contains(r.Path, "://") || !safeText(r.Path) {
		return invalid()
	}
	decoded, err := url.PathUnescape(r.Path)
	if err != nil || !safeText(decoded) || strings.ContainsAny(decoded, "\\%") {
		return invalid()
	}
	for _, segment := range strings.Split(decoded, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return invalid()
		}
	}
	if len(r.Query) > 100 || len(r.Query.Encode()) > 16384 || len(r.Headers) > 32 {
		return invalid()
	}
	for k, values := range r.Query {
		if !safeText(k) || len(values) > 100 || secretName(k) {
			return invalid()
		}
		for _, value := range values {
			if !safeText(value) {
				return invalid()
			}
		}
	}
	headerBytes := 0
	for k, values := range r.Headers {
		if !validHeaderName(k) || deniedHeader(k) || len(values) > 16 {
			return invalid()
		}
		for _, value := range values {
			headerBytes += len(k) + len(value)
			if !safeText(value) || headerBytes > 16384 {
				return invalid()
			}
		}
	}
	endpoint, err := c.host.Authority.Endpoint(r.Path, r.Query)
	if err != nil {
		return invalid()
	}
	if c.containsCredential([]byte(endpoint.String())) || c.containsCredential(r.Body) {
		return invalid()
	}
	for _, values := range r.Headers {
		for _, value := range values {
			if c.containsCredential([]byte(value)) {
				return invalid()
			}
		}
	}
	return endpoint, nil
}

func safeText(s string) bool {
	return utf8.ValidString(s) && !strings.ContainsFunc(s, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) })
}
func validHeaderName(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range []byte(s) {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(c))) {
			return false
		}
	}
	return true
}
func secretName(s string) bool {
	s = strings.ToLower(strings.ReplaceAll(s, "-", "_"))
	return strings.Contains(s, "token") || strings.Contains(s, "authorization") || s == "password"
}
func deniedHeader(s string) bool {
	if secretName(s) || strings.HasPrefix(strings.ToLower(s), "proxy-") || strings.HasPrefix(strings.ToLower(s), "x-forwarded-") {
		return true
	}
	switch strings.ToLower(s) {
	case "host", "cookie", "cookie2", "set-cookie", "referer", "origin", "forwarded", "connection", "keep-alive", "te", "trailer", "transfer-encoding", "upgrade", "content-length", "content-encoding", "accept-encoding", "expect", "idempotency-key", "x-idempotency-key":
		return true
	}
	return false
}
func (c *Client) responseHeaders(input http.Header) (http.Header, error) {
	out := make(http.Header)
	size := 0
	for _, name := range []string{"Content-Type", "Content-Length", "ETag", "Last-Modified", "Digest", "X-Page", "X-Next-Page", "X-Total", "X-Total-Pages", "Retry-After", "Link"} {
		for _, value := range input.Values(name) {
			size += len(name) + len(value)
			if size > 16384 || !safeText(value) || c.containsCredential([]byte(value)) {
				return nil, uxv1.NewError(uxv1.CodeSafety, "native response metadata is unsafe")
			}
			out.Add(name, value)
		}
	}
	return out, nil
}
func (c *Client) credentialForms() [][]byte {
	return [][]byte{[]byte(c.credential.Value), []byte(url.QueryEscape(c.credential.Value)), []byte(url.PathEscape(c.credential.Value))}
}
func (c *Client) containsCredential(body []byte) bool {
	for _, secret := range c.credentialForms() {
		if len(secret) != 0 && bytes.Contains(body, secret) {
			return true
		}
	}
	return false
}

func (c *Client) jsonContainsCredential(body []byte) bool {
	if !json.Valid(body) {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	for {
		token, err := decoder.Token()
		if err != nil {
			return false
		}
		if text, ok := token.(string); ok && c.containsCredential([]byte(text)) {
			return true
		}
	}
}

type credentialWriter struct {
	dst     io.Writer
	secrets [][]byte
	tail    []byte
}

func (w *credentialWriter) Write(p []byte) (int, error) {
	check := make([]byte, 0, len(w.tail)+len(p))
	check = append(check, w.tail...)
	check = append(check, p...)
	keep := 0
	for _, secret := range w.secrets {
		if len(secret) > 0 && bytes.Contains(check, secret) {
			return 0, errors.New("credential echo refused")
		}
		keep = max(keep, len(secret)-1)
	}
	w.tail = append(w.tail[:0], check[max(0, len(check)-keep):]...)
	return w.dst.Write(p)
}
func contextError(err error) error {
	if errors.Is(err, context.Canceled) {
		return uxv1.NewError(uxv1.CodeCanceled, "native GitLab operation canceled")
	}
	return uxv1.NewError(uxv1.CodeUpstream, "native GitLab operation deadline exceeded")
}
