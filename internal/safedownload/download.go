// Package safedownload stages one bounded download in a private directory and
// publishes it as a new directory without replacing caller files. Archive
// validation is complete before any extracted-content files are written.
package safedownload

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

const (
	MaxArchiveBytes  = 64 << 20
	MaxExpandedBytes = 256 << 20
	MaxEntries       = 1000
	MaxDirectories   = 128
)

type ownedEntry struct {
	parent    *ownedEntry
	name      string
	info      os.FileInfo
	directory bool
	file      *os.File
}
type Transaction struct {
	parent                      *os.File
	stage                       *os.File
	parentPath, name, stageName string
	owned                       []*ownedEntry
	directories                 map[string]*ownedEntry
	committed                   bool
	closed                      bool
}

type Receipt struct {
	Files int   `json:"files"`
	Bytes int64 `json:"expanded_bytes"`
}

func ValidRelativePath(name string) bool { return len(name) <= 1024 && validName(name) }

func ValidFileName(name string) bool { return validName(name) && !strings.Contains(name, "/") }

func ValidateDestination(destination string) error {
	if !filepath.IsAbs(destination) || filepath.Clean(destination) != destination || len(destination) > 4096 || !validName(filepath.Base(destination)) || strings.ContainsAny(destination, "\x00\r\n") {
		return errors.New("destination must be an absolute clean new-directory path")
	}
	return nil
}

// Prepare requires an existing symlink-free parent and a nonexistent final
// component. It must run before network access. Mutations stay relative to
// opened directory descriptors; Commit reopens the absolute parent path only
// to verify that it still identifies the pinned directory.
func Prepare(destination string) (*Transaction, error) {
	if err := ValidateDestination(destination); err != nil {
		return nil, err
	}
	parentPath := filepath.Dir(destination)
	parent, err := openAbsoluteDirectory(parentPath)
	if err != nil {
		return nil, errors.New("destination parent must be an accessible symlink-free directory")
	}
	name := filepath.Base(destination)
	if err := ensureAbsent(parent, name); err != nil {
		parent.Close()
		return nil, errors.New("destination already exists or cannot be inspected")
	}
	var entropy [16]byte
	if _, err := rand.Read(entropy[:]); err != nil {
		parent.Close()
		return nil, err
	}
	stageName := ".gl-axi-download-" + hex.EncodeToString(entropy[:])
	stage, err := makeDirectory(parent, stageName)
	if err != nil {
		parent.Close()
		return nil, errors.New("cannot create private download staging directory")
	}
	info, err := stage.Stat()
	if err != nil {
		stage.Close()
		parent.Close()
		return nil, err
	}
	entry := &ownedEntry{parent: &ownedEntry{file: parent}, name: stageName, info: info, directory: true, file: stage}
	return &Transaction{parent: parent, stage: stage, parentPath: parentPath, name: name, stageName: stageName, owned: []*ownedEntry{entry}, directories: map[string]*ownedEntry{"": entry}}, nil
}

func (e *ownedEntry) openDirectory() (*os.File, error) {
	if e.file != nil {
		return e.file, nil
	}
	parent, err := e.parent.openDirectory()
	if err != nil {
		return nil, err
	}
	file, err := openDirectory(parent, e.name)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !os.SameFile(info, e.info) {
		file.Close()
		return nil, errors.New("staged directory was replaced or cannot be inspected")
	}
	e.file = file
	return file, nil
}

func (e *ownedEntry) closeDirectory() error {
	if e.file == nil {
		return nil
	}
	err := e.file.Close()
	e.file = nil
	return err
}

func (t *Transaction) Write(ctx context.Context, name string, src io.Reader, size int64) error {
	if t.closed || t.committed || !validName(name) || strings.Contains(name, "/") || size < 0 || size > MaxArchiveBytes {
		return errors.New("invalid staged file")
	}
	return t.writeFile(ctx, t.directories[""], name, src, size)
}

func (t *Transaction) writeFile(ctx context.Context, dir *ownedEntry, name string, src io.Reader, size int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	parent, err := dir.openDirectory()
	if err != nil {
		return err
	}
	file, err := createFile(parent, name)
	if err != nil {
		return errors.New("staged filename collision or unsafe path")
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	t.owned = append(t.owned, &ownedEntry{parent: dir, name: name, info: info})
	n, copyErr := io.Copy(file, io.LimitReader(contextReader{ctx, src}, size+1))
	syncErr := file.Sync()
	closeErr := file.Close()
	if copyErr != nil {
		return fmt.Errorf("staged transfer failed: %w", copyErr)
	}
	if syncErr != nil || closeErr != nil || n != size {
		return errors.New("staged transfer failed or changed size")
	}
	return ctx.Err()
}

type archiveEntry struct {
	file *zip.File
	name string
	dir  bool
}

func inspectArchive(ctx context.Context, data []byte) ([]archiveEntry, Receipt, error) {
	if len(data) > MaxArchiveBytes {
		return nil, Receipt{}, errors.New("archive byte limit exceeded")
	}
	if err := validateZIPDirectory(data); err != nil {
		return nil, Receipt{}, err
	}
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, Receipt{}, errors.New("invalid ZIP archive")
	}
	if len(archive.File) == 0 || len(archive.File) > MaxEntries {
		return nil, Receipt{}, errors.New("archive item limit exceeded or empty archive")
	}
	entries := make([]archiveEntry, 0, len(archive.File))
	type seenEntry struct {
		name          string
		dir, explicit bool
	}
	seen := map[string]seenEntry{}
	receipt := Receipt{}
	for _, file := range archive.File {
		if err := ctx.Err(); err != nil {
			return nil, receipt, err
		}
		dir := file.FileInfo().IsDir()
		name := file.Name
		if dir {
			name = strings.TrimSuffix(name, "/")
		}
		if !validName(name) || len(name) > 1024 || file.Mode()&os.ModeType != 0 && file.Mode()&os.ModeType != os.ModeDir || file.Flags&1 != 0 {
			return nil, receipt, errors.New("archive has an unsafe entry type or path")
		}
		parts := strings.Split(name, "/")
		for i := 1; i <= len(parts); i++ {
			p := strings.Join(parts[:i], "/")
			key := strings.ToLower(p)
			isDir := i < len(parts) || dir
			old, ok := seen[key]
			if ok && (old.name != p || old.dir != isDir || i == len(parts) && old.explicit) {
				return nil, receipt, errors.New("archive paths collide")
			}
			seen[key] = seenEntry{p, isDir, old.explicit || i == len(parts)}
		}
		if dir {
			if file.UncompressedSize64 != 0 {
				return nil, receipt, errors.New("archive directory has content")
			}
		} else {
			if file.CompressedSize64 > uint64(len(data)) || file.UncompressedSize64 > MaxExpandedBytes || file.UncompressedSize64 > uint64(MaxExpandedBytes-receipt.Bytes) || file.UncompressedSize64 > max(uint64(1), file.CompressedSize64)*1000 {
				return nil, receipt, errors.New("archive expansion limit exceeded")
			}
			receipt.Bytes += int64(file.UncompressedSize64)
			receipt.Files++
		}
		entries = append(entries, archiveEntry{file, name, dir})
	}
	dirs := 0
	for _, entry := range seen {
		if entry.dir {
			dirs++
		}
	}
	if dirs > MaxDirectories || len(seen) > MaxEntries {
		return nil, receipt, errors.New("archive directory or total path limit exceeded")
	}
	// Read every entry through EOF now, including CRC and actual expanded size,
	// before creating any extracted files. ZIP declared sizes alone are not proof.
	for _, entry := range entries {
		if entry.dir {
			continue
		}
		r, err := entry.file.Open()
		if err != nil {
			return nil, receipt, errors.New("invalid ZIP entry")
		}
		n, readErr := io.Copy(io.Discard, io.LimitReader(contextReader{ctx, r}, int64(entry.file.UncompressedSize64)+1))
		closeErr := r.Close()
		if readErr != nil {
			return nil, receipt, fmt.Errorf("archive entry read failed: %w", readErr)
		}
		if closeErr != nil || n != int64(entry.file.UncompressedSize64) {
			return nil, receipt, errors.New("archive entry size or checksum mismatch")
		}
	}
	return entries, receipt, nil
}

// Bound central-directory parsing before archive/zip can allocate its file
// table. ZIP64 and multidisk archives are unnecessary within this slice's caps.
func validateZIPDirectory(data []byte) error {
	bad := errors.New("unsupported or oversized ZIP directory")
	start := max(0, len(data)-65557)
	end := bytes.LastIndex(data[start:], []byte("PK\x05\x06"))
	if end < 0 {
		return bad
	}
	end += start
	if end+22 > len(data) {
		return bad
	}
	eocd := data[end:]
	if end+22+int(binary.LittleEndian.Uint16(eocd[20:])) != len(data) || binary.LittleEndian.Uint32(eocd[4:]) != 0 {
		return bad
	}
	count := int(binary.LittleEndian.Uint16(eocd[10:]))
	if count == 0 || count > MaxEntries || int(binary.LittleEndian.Uint16(eocd[8:])) != count {
		return bad
	}
	size := uint64(binary.LittleEndian.Uint32(eocd[12:]))
	offset := uint64(binary.LittleEndian.Uint32(eocd[16:]))
	if offset+size != uint64(end) {
		return bad
	}
	pos := int(offset)
	for i := 0; i < count; i++ {
		if pos+46 > end || !bytes.Equal(data[pos:pos+4], []byte("PK\x01\x02")) {
			return bad
		}
		length := 46 + int(binary.LittleEndian.Uint16(data[pos+28:])) + int(binary.LittleEndian.Uint16(data[pos+30:])) + int(binary.LittleEndian.Uint16(data[pos+32:]))
		pos += length
	}
	if pos != end {
		return bad
	}
	return nil
}

func (t *Transaction) Extract(ctx context.Context, data []byte) (Receipt, error) {
	entries, receipt, err := inspectArchive(ctx, data)
	if err != nil {
		return Receipt{}, err
	}
	if t.closed || t.committed {
		return Receipt{}, errors.New("closed download transaction")
	}
	// Create inferred and explicit directories in parent-first order.
	names := map[string]bool{}
	for _, entry := range entries {
		dir := path.Dir(entry.name)
		if entry.dir {
			dir = entry.name
		}
		for dir != "." && dir != "" {
			names[dir] = true
			dir = path.Dir(dir)
		}
	}
	dirs := make([]string, 0, len(names))
	for name := range names {
		dirs = append(dirs, name)
	}
	sort.Strings(dirs)
	for _, name := range dirs {
		if err := ctx.Err(); err != nil {
			return Receipt{}, err
		}
		parent := path.Dir(name)
		if parent == "." {
			parent = ""
		}
		parentDir, err := t.directories[parent].openDirectory()
		if err != nil {
			return Receipt{}, err
		}
		dir, err := makeDirectory(parentDir, path.Base(name))
		if err != nil {
			return Receipt{}, errors.New("cannot create staged archive directory")
		}
		info, err := dir.Stat()
		if err != nil {
			dir.Close()
			return Receipt{}, err
		}
		entry := &ownedEntry{parent: t.directories[parent], name: path.Base(name), info: info, directory: true, file: dir}
		t.directories[name] = entry
		t.owned = append(t.owned, entry)
	}
	for _, entry := range entries {
		if entry.dir {
			continue
		}
		parent := path.Dir(entry.name)
		if parent == "." {
			parent = ""
		}
		r, err := entry.file.Open()
		if err != nil {
			return Receipt{}, err
		}
		writeErr := t.writeFile(ctx, t.directories[parent], path.Base(entry.name), r, int64(entry.file.UncompressedSize64))
		closeErr := r.Close()
		if writeErr != nil {
			return Receipt{}, writeErr
		}
		if closeErr != nil {
			return Receipt{}, closeErr
		}
	}
	return receipt, nil
}

func (t *Transaction) Commit(ctx context.Context) error {
	if t.closed || t.committed {
		return errors.New("closed download transaction")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := openAbsoluteDirectory(t.parentPath)
	if err != nil {
		return errors.New("destination parent changed")
	}
	a, aerr := current.Stat()
	b, berr := t.parent.Stat()
	current.Close()
	if aerr != nil || berr != nil || !os.SameFile(a, b) {
		return errors.New("destination parent changed")
	}
	// Windows directory publication requires closing descendant handles first.
	// Rollback can reopen recorded directories with no-follow and identity checks.
	for name, dir := range t.directories {
		if name == "" {
			continue
		}
		if err := dir.closeDirectory(); err != nil {
			return errors.New("cannot close staged archive directory")
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := publishDirectory(t.parent, t.stage, t.stageName, t.name); err != nil {
		return errors.New("destination appeared or atomic no-clobber publication failed")
	}
	t.committed = true
	return nil
}

func (t *Transaction) Close() error {
	if t.closed {
		return nil
	}
	t.closed = true
	var cleanup error
	if !t.committed {
		for i := len(t.owned) - 1; i >= 0; i-- {
			entry := t.owned[i]
			if err := entry.closeDirectory(); err != nil {
				cleanup = err
			}
			parent, err := entry.parent.openDirectory()
			if err != nil {
				cleanup = errors.New("download cleanup incomplete; unrelated entries preserved")
				continue
			}
			if err := removeOwned(parent, entry.name, entry.info, entry.directory); err != nil {
				cleanup = errors.New("download cleanup incomplete; unrelated entries preserved")
			}
		}
	}
	for _, dir := range t.directories {
		if err := dir.closeDirectory(); err != nil && cleanup == nil {
			cleanup = err
		}
	}
	if err := t.parent.Close(); err != nil && cleanup == nil {
		cleanup = err
	}
	return cleanup
}

func validName(name string) bool {
	if name == "" || strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 || strings.HasSuffix(part, ".") || strings.HasSuffix(part, " ") {
			return false
		}
		for _, c := range []byte(part) {
			if c < 0x21 || c > 0x7e || strings.ContainsRune(`\:*?"<>|`, rune(c)) {
				return false
			}
		}
		base := strings.ToUpper(strings.SplitN(part, ".", 2)[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" {
			return false
		}
		for i := 1; i <= 9; i++ {
			if base == fmt.Sprint("COM", i) || base == fmt.Sprint("LPT", i) {
				return false
			}
		}
	}
	return true
}

type contextReader struct {
	ctx    context.Context
	source io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.source.Read(p)
}
