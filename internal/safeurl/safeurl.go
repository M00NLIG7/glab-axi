// Package safeurl validates GitLab authority and returned URLs before credentials
// or identifiers cross a trust boundary.
package safeurl

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gl-axi/internal/contract/v1"
	"gl-axi/internal/limits"
)

var projectSegment = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

type Authority struct {
	LogicalHost string
	API         *url.URL
	Web         *url.URL
}

func NewAuthority(logicalHost, apiBase, webBase string) (Authority, error) {
	if err := ValidateHost(logicalHost); err != nil {
		return Authority{}, err
	}
	api, err := parseBase(apiBase, true)
	if err != nil {
		return Authority{}, err
	}
	web, err := parseBase(webBase, false)
	if err != nil {
		return Authority{}, err
	}
	if !strings.HasSuffix(strings.TrimSuffix(api.Path, "/"), "/api/v4") {
		return Authority{}, v1.NewError(v1.CodeValidation, "API base must end in /api/v4")
	}
	return Authority{LogicalHost: strings.ToLower(logicalHost), API: api, Web: web}, nil
}

func parseBase(raw string, api bool) (*url.URL, error) {
	if len(raw) == 0 || len(raw) > 2048 || strings.ContainsAny(raw, "\x00\r\n\\") {
		return nil, v1.NewError(v1.CodeValidation, "invalid GitLab base URL")
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, v1.NewError(v1.CodeValidation, "GitLab base URL must be an HTTPS origin and path")
	}
	if err := validateURLHost(u); err != nil {
		return nil, err
	}
	if strings.Contains(strings.ToLower(u.EscapedPath()), "%2f") || hasDotSegment(u.Path) {
		return nil, v1.NewError(v1.CodeValidation, "GitLab base URL contains an unsafe path")
	}
	u.Path = strings.TrimSuffix(u.Path, "/")
	u.RawPath = ""
	if !api && strings.HasSuffix(u.Path, "/api/v4") {
		return nil, v1.NewError(v1.CodeValidation, "web base must not be an API base")
	}
	return u, nil
}

func validateURLHost(u *url.URL) error {
	host := u.Hostname()
	if host == "" || len(host) > limits.MaxHostBytes || strings.ContainsAny(host, "_%") {
		return v1.NewError(v1.CodeValidation, "invalid GitLab URL host")
	}
	if port := u.Port(); port != "" {
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return v1.NewError(v1.CodeValidation, "invalid GitLab URL port")
		}
	}
	return nil
}

func ValidateHost(hostport string) error {
	if hostport == "" || len(hostport) > limits.MaxHostBytes || strings.ContainsAny(hostport, "\x00\r\n/@\\") || strings.ContainsFunc(hostport, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) {
		return v1.NewError(v1.CodeValidation, "invalid GitLab host")
	}
	host := hostport
	if strings.HasPrefix(hostport, "[") {
		var err error
		host, _, err = net.SplitHostPort(hostport)
		if err != nil {
			return v1.NewError(v1.CodeValidation, "invalid GitLab host")
		}
	} else if strings.Count(hostport, ":") == 1 {
		var port string
		var err error
		host, port, err = net.SplitHostPort(hostport)
		if err != nil {
			return v1.NewError(v1.CodeValidation, "invalid GitLab host")
		}
		p, err := strconv.Atoi(port)
		if err != nil || p < 1 || p > 65535 {
			return v1.NewError(v1.CodeValidation, "invalid GitLab host port")
		}
	}
	if host == "" || strings.ContainsAny(host, "_%") {
		return v1.NewError(v1.CodeValidation, "invalid GitLab host")
	}
	return nil
}

func IsIPHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	return net.ParseIP(strings.Trim(host, "[]")) != nil
}

func ValidateProject(project string) error {
	if project == "" || len(project) > limits.MaxProjectBytes || !utf8.ValidString(project) || strings.ContainsAny(project, "\x00\r\n\\") {
		return v1.NewError(v1.CodeValidation, "invalid GitLab project path")
	}
	parts := strings.Split(project, "/")
	if len(parts) < 2 || len(parts) > limits.MaxProjectSegments {
		return v1.NewError(v1.CodeValidation, "project must be a namespace/project path")
	}
	for _, part := range parts {
		if !projectSegment.MatchString(part) || part == "." || part == ".." || strings.HasSuffix(part, ".git") {
			return v1.NewError(v1.CodeValidation, "invalid GitLab project path")
		}
	}
	return nil
}

func ValidateBranch(branch string) error {
	if branch == "" || branch == "@" || len(branch) > limits.MaxBranchBytes || !utf8.ValidString(branch) || strings.ContainsAny(branch, "\x00\r\n ~^:?*[\\") || strings.ContainsFunc(branch, func(r rune) bool { return unicode.IsControl(r) || unicode.Is(unicode.Cf, r) }) || strings.Contains(branch, "..") || strings.Contains(branch, "@{") || strings.Contains(branch, "//") || strings.HasPrefix(branch, "/") || strings.HasPrefix(branch, "-") || strings.HasSuffix(branch, "/") || strings.HasSuffix(branch, ".") {
		return v1.NewError(v1.CodeValidation, "invalid Git branch")
	}
	for _, part := range strings.Split(branch, "/") {
		if part == "" || strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return v1.NewError(v1.CodeValidation, "invalid Git branch")
		}
	}
	return nil
}

func (a Authority) Endpoint(path string, query url.Values) (*url.URL, error) {
	if path == "" || strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n\\?#") || strings.Contains(path, "../") {
		return nil, v1.NewError(v1.CodeInternal, "unsafe typed endpoint")
	}
	raw := strings.TrimSuffix(a.API.String(), "/") + "/" + path
	u, err := url.Parse(raw)
	if err != nil || !a.SameAPIOrigin(u) || !a.WithinAPI(u) {
		return nil, v1.NewError(v1.CodeInternal, "unsafe typed endpoint")
	}
	u.RawQuery = query.Encode()
	return u, nil
}

func (a Authority) SameAPIOrigin(u *url.URL) bool {
	return u != nil && u.Scheme == a.API.Scheme && strings.EqualFold(u.Host, a.API.Host) && u.User == nil
}

func (a Authority) WithinAPI(u *url.URL) bool {
	if !a.SameAPIOrigin(u) {
		return false
	}
	base := strings.TrimSuffix(a.API.Path, "/")
	return u.Path == base || strings.HasPrefix(u.Path, base+"/")
}

func (a Authority) ExpectedProjectURL(project string) string {
	return strings.TrimSuffix(a.Web.String(), "/") + "/" + project
}

func (a Authority) ValidateProjectWebURL(raw, project string) error {
	expected := a.ExpectedProjectURL(project)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !sameOrigin(u, a.Web) || strings.TrimSuffix(raw, "/") != expected {
		return v1.NewError(v1.CodeSafety, "GitLab returned a mismatched project URL")
	}
	return nil
}

func (a Authority) ValidateMRWebURL(raw, project string, iid int64) error {
	if iid < 1 {
		return v1.NewError(v1.CodeSafety, "GitLab returned an invalid merge request ID")
	}
	expected := fmt.Sprintf("%s/-/merge_requests/%d", a.ExpectedProjectURL(project), iid)
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !sameOrigin(u, a.Web) || strings.TrimSuffix(raw, "/") != expected {
		return v1.NewError(v1.CodeSafety, "GitLab returned a mismatched merge request URL")
	}
	return nil
}

func (a Authority) ParseMRWebURL(raw, project string) (int64, error) {
	prefix := a.ExpectedProjectURL(project) + "/-/merge_requests/"
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !sameOrigin(u, a.Web) || !strings.HasPrefix(raw, prefix) {
		return 0, v1.NewError(v1.CodeSafety, "merge request URL is outside the configured project")
	}
	tail := strings.TrimSuffix(strings.TrimPrefix(raw, prefix), "/")
	if tail == "" || strings.Contains(tail, "/") {
		return 0, v1.NewError(v1.CodeValidation, "invalid merge request URL")
	}
	iid, err := strconv.ParseInt(tail, 10, 64)
	if err != nil || iid < 1 {
		return 0, v1.NewError(v1.CodeValidation, "invalid merge request URL")
	}
	if err := a.ValidateMRWebURL(raw, project, iid); err != nil {
		return 0, err
	}
	return iid, nil
}

func sameOrigin(a, b *url.URL) bool {
	return a.Scheme == b.Scheme && strings.EqualFold(a.Host, b.Host)
}

func hasDotSegment(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == "." || part == ".." {
			return true
		}
	}
	return false
}
