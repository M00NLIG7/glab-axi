package glab

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/safeurl"
)

// DiscoverySelectors is a closed provider selector, not arbitrary API fields.
// Owner always means a user namespace. Group always means a full group path.
type DiscoverySelectors struct {
	Owner, Group, Visibility, Language string
	Archived                           string // unset, true, false
	IncludeSubgroups                   bool
}

type SearchSelectors struct {
	Area, Group, State, Sort string
}

var usernamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,254}$`)
var quotedProjectTermPattern = regexp.MustCompile(`(?:^| )"([^"]+)"(?: |$)`)

func ValidateGroup(group string) error {
	if safeurl.ValidateProject(group+"/project") != nil || len(group) > 512 {
		return uxv1.NewError(uxv1.CodeValidation, "group must be an exact GitLab namespace path")
	}
	// Numeric paths are interpreted by GitLab as IDs, not namespace names.
	if strings.Trim(group, "0123456789") == "" {
		return uxv1.NewError(uxv1.CodeValidation, "group must be a path, not a numeric ID")
	}
	return nil
}

func (s DiscoverySelectors) Validate() error {
	if s.Owner != "" && (!usernamePattern.MatchString(s.Owner) || strings.Contains(s.Owner, "..") || strings.Trim(s.Owner, "0123456789") == "") {
		return uxv1.NewError(uxv1.CodeValidation, "owner must be a literal GitLab username, not a group or @me")
	}
	if s.Group != "" {
		if err := ValidateGroup(s.Group); err != nil {
			return err
		}
		if s.Owner != "" || s.Language != "" {
			return uxv1.NewError(uxv1.CodeValidation, "group cannot be combined with owner or language")
		}
	}
	if s.IncludeSubgroups && s.Group == "" {
		return uxv1.NewError(uxv1.CodeValidation, "include-subgroups requires group")
	}
	if s.Visibility != "" && s.Visibility != "public" && s.Visibility != "internal" && s.Visibility != "private" {
		return uxv1.NewError(uxv1.CodeValidation, "visibility must be public, internal, or private")
	}
	if s.Archived != "" && s.Archived != "true" && s.Archived != "false" {
		return uxv1.NewError(uxv1.CodeValidation, "archive selector must be true or false")
	}
	if s.Language != "" && (validateText(s.Language, "language", 64) != nil || strings.TrimSpace(s.Language) != s.Language || strings.ContainsFunc(s.Language, unicode.IsControl)) {
		return uxv1.NewError(uxv1.CodeValidation, "language must be a literal programming language name (1..64 bytes)")
	}
	return nil
}

func (s SearchSelectors) Validate(scope string) error {
	if s.Area != "" && s.Area != "project" && s.Area != "host" {
		return uxv1.NewError(uxv1.CodeValidation, "search scope must be project or host")
	}
	if s.Group != "" {
		if err := ValidateGroup(s.Group); err != nil {
			return err
		}
		if s.Area != "" {
			return uxv1.NewError(uxv1.CodeValidation, "group and scope are mutually exclusive")
		}
	}
	if scope == "code" || scope == "commits" {
		if s != (SearchSelectors{}) {
			return uxv1.NewError(uxv1.CodeUnsupported, "commit/code qualifiers and host/group search are not supported")
		}
		return nil
	}
	if s.Sort != "" && s.Sort != "created" {
		return uxv1.NewError(uxv1.CodeUnsupported, "only created sorting maps to GitLab search (created_at descending)")
	}
	if scope == "repos" && (s.State != "" || s.Area == "project") {
		return uxv1.NewError(uxv1.CodeValidation, "repository search is host/group scoped and has no state selector")
	}
	if s.State != "" && s.State != "opened" && s.State != "closed" && s.State != "all" && !(scope == "mrs" && s.State == "merged") {
		return uxv1.NewError(uxv1.CodeValidation, "invalid GitLab issue/merge-request search state")
	}
	return nil
}

func discoveryEndpoint(r Request) (string, error) {
	if err := r.Discovery.Validate(); err != nil {
		return "", err
	}
	s := r.Discovery
	q := url.Values{"page": {strconv.Itoa(r.Page)}, "per_page": {strconv.Itoa(r.PerPage)}}
	path := "projects"
	if s.Owner != "" {
		path = "users/" + url.PathEscape(s.Owner) + "/projects"
	}
	if s.Group != "" {
		path = "groups/" + url.PathEscape(s.Group) + "/projects"
		q.Set("with_shared", "false")
		q.Set("include_subgroups", strconv.FormatBool(s.IncludeSubgroups))
	}
	if s.Owner == "" && s.Group == "" && r.Query == "" {
		q.Set("owned", "true")
	}
	for key, value := range map[string]string{"visibility": s.Visibility, "archived": s.Archived, "with_programming_language": s.Language, "search": r.Query} {
		if value != "" {
			q.Set(key, value)
		}
	}
	if r.Query != "" {
		if err := validateText(r.Query, "search query", 1024); err != nil {
			return "", err
		}
	}
	if r.Operation == OpSearch {
		if err := ValidateCreatedProjectQuery(r.Query); err != nil {
			return "", err
		}
		q.Set("search_namespaces", "true")
	}
	if r.Search.Sort != "" {
		if r.Search.Sort != "created" {
			return "", uxv1.NewError(uxv1.CodeValidation, "unsupported discovery sort")
		}
		q.Set("order_by", "created_at")
		q.Set("sort", "desc")
	}
	return path + "?" + q.Encode(), nil
}

func ValidateCreatedProjectQuery(query string) error {
	query = strings.Join(strings.Fields(query), " ")
	var terms []string
	for {
		match := quotedProjectTermPattern.FindStringSubmatchIndex(query)
		if match == nil {
			break
		}
		terms = append(terms, strings.Fields(query[:match[0]])...)
		terms = append(terms, query[match[2]:match[3]])
		query = query[match[3]+1:]
	}
	terms = append(terms, strings.Fields(query)...)
	if len(terms) == 0 {
		return uxv1.NewError(uxv1.CodeUnsupported, "created repository sorting requires query terms of at least three characters")
	}
	for _, term := range terms {
		if utf8.RuneCountInString(term) < 3 {
			return uxv1.NewError(uxv1.CodeUnsupported, "created repository sorting requires query terms of at least three characters")
		}
	}
	return nil
}
