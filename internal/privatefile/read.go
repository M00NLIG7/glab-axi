// Package privatefile opens bounded private inputs without following a final
// symlink. Validation is performed on the opened descriptor so a path swap
// cannot redirect a read after inspection.
package privatefile

import (
	"io"
	"path/filepath"
	"strings"
	"unicode/utf8"

	v1 "glab-axi/internal/contract/v1"
)

// Read returns one bounded UTF-8 private-file value. On platforms with Unix
// permission bits the file must be inaccessible to group and other users.
func Read(path string, max int, trimFinalNewline bool) (string, error) {
	if !filepath.IsAbs(path) {
		return "", v1.NewError(v1.CodeValidation, "input file path must be absolute")
	}
	file, info, permissionsMeaningful, err := openNoFollow(filepath.Clean(path))
	if err != nil {
		return "", v1.NewError(v1.CodeSafety, "input file must be a private regular file, not a symlink")
	}
	defer file.Close()
	if !info.Mode().IsRegular() || permissionsMeaningful && info.Mode().Perm()&0o077 != 0 {
		return "", v1.NewError(v1.CodeSafety, "input file must be a private regular file, not a symlink")
	}
	if info.Size() > int64(max+1) {
		return "", v1.NewError(v1.CodeValidation, "input file exceeds the size limit")
	}
	data, err := io.ReadAll(io.LimitReader(file, int64(max+1)))
	if err != nil {
		return "", v1.Wrap(v1.CodeUpstream, "cannot read input file", err)
	}
	if trimFinalNewline {
		data = []byte(strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r"))
	}
	if len(data) > max || !utf8.Valid(data) || strings.ContainsRune(string(data), '\x00') {
		return "", v1.NewError(v1.CodeValidation, "input file violates the content limit")
	}
	return string(data), nil
}
