package glab

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"gl-axi/internal/contract/uxv1"
)

// ValidateSnippetFilename validates a repository-relative logical path, never a
// local filesystem path. Each entire path is escaped as one API path segment.
func ValidateSnippetFilename(name string) error {
	if name == "" || len(name) > 1024 || !utf8.ValidString(name) || strings.ContainsFunc(name, unicode.IsControl) || strings.ContainsAny(name, "\\%?#") {
		return uxv1.NewError(uxv1.CodeValidation, "invalid snippet filename")
	}
	for _, segment := range strings.Split(name, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return uxv1.NewError(uxv1.CodeValidation, "snippet filename must be a relative file path without traversal")
		}
	}
	return nil
}
