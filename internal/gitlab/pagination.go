package gitlab

import (
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"gl-axi/internal/contract/v1"
	"gl-axi/internal/safeurl"
)

var nextLinkPattern = regexp.MustCompile(`^\s*<([^>]+)>\s*;(?:[^,;]+;)*\s*rel\s*=\s*"?next"?(?:\s*;.*)?$`)

func nextPage(authority safeurl.Authority, current *url.URL, header http.Header) (*url.URL, error) {
	link := header.Get("Link")
	if strings.TrimSpace(link) == "" {
		return nil, nil
	}
	var rawNext string
	for _, part := range splitLinks(link) {
		match := nextLinkPattern.FindStringSubmatch(part)
		if len(match) == 2 {
			if rawNext != "" {
				return nil, v1.NewError(v1.CodeUpstream, "GitLab returned multiple next-page links")
			}
			rawNext = match[1]
		}
	}
	if rawNext == "" {
		return nil, nil
	}
	next, err := url.Parse(rawNext)
	if err != nil || !next.IsAbs() || next.Scheme != "https" || next.User != nil || next.Fragment != "" || !authority.SameAPIOrigin(next) || !authority.WithinAPI(next) || next.Path != current.Path {
		return nil, v1.NewError(v1.CodeSafety, "GitLab returned an unsafe pagination URL")
	}
	if err := validatePaginationQuery(current.Query(), next.Query()); err != nil {
		return nil, err
	}
	return next, nil
}

func validatePaginationQuery(current, next url.Values) error {
	for key, values := range next {
		if len(values) != 1 {
			return v1.NewError(v1.CodeSafety, "GitLab pagination query contains duplicate values")
		}
		if key == "page" {
			page, err := strconv.Atoi(values[0])
			if err != nil || page < 1 || page > 1_000_000 {
				return v1.NewError(v1.CodeSafety, "GitLab pagination page is invalid")
			}
			continue
		}
		existing, ok := current[key]
		if !ok || len(existing) != 1 || existing[0] != values[0] {
			return v1.NewError(v1.CodeSafety, "GitLab pagination changed the typed query")
		}
	}
	for key, values := range current {
		if key == "page" {
			continue
		}
		nextValues, ok := next[key]
		if !ok || len(values) != 1 || len(nextValues) != 1 || values[0] != nextValues[0] {
			return v1.NewError(v1.CodeSafety, "GitLab pagination dropped the typed query")
		}
	}
	if next.Get("page") == "" {
		return v1.NewError(v1.CodeSafety, "GitLab pagination omitted the next page")
	}
	return nil
}

func splitLinks(value string) []string {
	var parts []string
	start := 0
	quoted := false
	angle := false
	for i, r := range value {
		switch r {
		case '"':
			if !angle {
				quoted = !quoted
			}
		case '<':
			if !quoted {
				angle = true
			}
		case '>':
			if !quoted {
				angle = false
			}
		case ',':
			if !quoted && !angle {
				parts = append(parts, value[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, value[start:])
	return parts
}
