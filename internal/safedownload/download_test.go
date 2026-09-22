package safedownload

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type zipEntry struct {
	name, body string
	mode       os.FileMode
}

func archiveBytes(t *testing.T, entries ...zipEntry) []byte {
	t.Helper()
	var data bytes.Buffer
	writer := zip.NewWriter(&data)
	for _, entry := range entries {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Deflate}
		header.SetMode(entry.mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(entry.body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}
func parentDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}
func noStaging(t *testing.T, parent string) {
	t.Helper()
	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".gl-axi-download-") {
			t.Fatalf("staging survived: %s", entry.Name())
		}
	}
}

func TestArchiveTransaction(t *testing.T) {
	parent := parentDir(t)
	destination := filepath.Join(parent, "result")
	tx, err := Prepare(destination)
	if err != nil {
		t.Fatal(err)
	}
	data := archiveBytes(t, zipEntry{"dir/ok.txt", "payload", 0o644}, zipEntry{"other", "second", 0o755})
	receipt, err := tx.Extract(context.Background(), data)
	if err != nil || receipt.Files != 2 || receipt.Bytes != 13 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("destination visible before commit")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(destination, "dir", "ok.txt"))
	if err != nil || string(body) != "payload" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	noStaging(t, parent)
}

func TestMaliciousArchivesRejectedBeforeContentWrites(t *testing.T) {
	cases := map[string][]zipEntry{
		"traversal":                {{"good", "good", 0o644}, {"../outside", "bad", 0o644}},
		"absolute":                 {{"/outside", "bad", 0o644}},
		"backslash":                {{`dir\evil`, "bad", 0o644}},
		"link":                     {{"link", "outside", os.ModeSymlink | 0o777}},
		"device":                   {{"device", "", os.ModeDevice | 0o600}},
		"collision":                {{"a", "one", 0o644}, {"a", "two", 0o644}},
		"case-collision":           {{"A", "one", 0o644}, {"a", "two", 0o644}},
		"file-directory-collision": {{"a", "one", 0o644}, {"a/b", "two", 0o644}},
		"windows-device":           {{"CON.txt", "bad", 0o644}},
		"trailing-dot":             {{"file.", "bad", 0o644}},
		"bomb":                     {{"zeros", strings.Repeat("0", 2<<20), 0o644}},
	}
	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			parent := parentDir(t)
			tx, err := Prepare(filepath.Join(parent, "result"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Extract(context.Background(), archiveBytes(t, entries...)); err == nil {
				t.Fatal("unsafe archive accepted")
			}
			staged, err := tx.stage.ReadDir(-1)
			if err != nil || len(staged) != 0 {
				t.Fatalf("content written before rejection: %v %v", staged, err)
			}
			if err := tx.Close(); err != nil {
				t.Fatal(err)
			}
			noStaging(t, parent)
		})
	}
	parent := parentDir(t)
	tx, err := Prepare(filepath.Join(parent, "result"))
	if err != nil {
		t.Fatal(err)
	}
	data := archiveBytes(t, zipEntry{"ok", "payload", 0o644})
	offset := bytes.Index(data, []byte("PK\x01\x02"))
	data[offset+16] ^= 1
	if _, err := tx.Extract(context.Background(), data); err == nil {
		t.Fatal("CRC mismatch accepted")
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	noStaging(t, parent)
}

func TestNoClobberCancellationAndDestinationRace(t *testing.T) {
	parent := parentDir(t)
	destination := filepath.Join(parent, "result")
	if err := os.WriteFile(destination, []byte("caller"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(destination); err == nil {
		t.Fatal("existing destination accepted")
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	tx, err := Prepare(destination)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Write(context.Background(), "asset", strings.NewReader("bytes"), 5); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	caller := filepath.Join(destination, "caller")
	if err := os.WriteFile(caller, []byte("preserved"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err == nil {
		t.Fatal("raced destination replaced")
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(caller)
	if err != nil || string(body) != "preserved" {
		t.Fatal("caller file changed")
	}
	noStaging(t, parent)
	tx, err = Prepare(filepath.Join(parent, "cancelled"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := tx.Write(ctx, "file", strings.NewReader("bytes"), 5); err == nil {
		t.Fatal("canceled write accepted")
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	noStaging(t, parent)
}

func TestSymlinkAndParentPathRace(t *testing.T) {
	base := parentDir(t)
	parent := filepath.Join(base, "parent")
	outside := filepath.Join(base, "outside")
	for _, dir := range []string{parent, outside} {
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink creation unavailable: %v", err)
	}
	if _, err := Prepare(filepath.Join(link, "result")); err == nil {
		t.Fatal("symlink parent accepted")
	}
	if _, err := Prepare(link); err == nil {
		t.Fatal("symlink destination accepted")
	}
	tx, err := Prepare(filepath.Join(parent, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Write(context.Background(), "asset", strings.NewReader("bytes"), 5); err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(base, "moved")
	if err := os.Rename(parent, moved); err != nil {
		tx.Close()
		t.Skipf("OS pins opened parent: %v", err)
	}
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err == nil {
		t.Fatal("changed parent accepted")
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(outside)
	if err != nil || len(entries) != 0 {
		t.Fatal("write escaped to substituted parent")
	}
	noStaging(t, moved)
}

func TestCleanupPreservesForeignEntries(t *testing.T) {
	parent := parentDir(t)
	tx, err := Prepare(filepath.Join(parent, "result"))
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Write(context.Background(), "ours", strings.NewReader("bytes"), 5); err != nil {
		t.Fatal(err)
	}
	foreign := filepath.Join(parent, tx.stageName, "foreign")
	if err := os.WriteFile(foreign, []byte("caller"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err == nil {
		t.Fatal("foreign staged entry silently removed")
	}
	body, err := os.ReadFile(foreign)
	if err != nil || string(body) != "caller" {
		t.Fatal("foreign content changed")
	}
	if _, err := os.Stat(filepath.Join(parent, tx.stageName, "ours")); !os.IsNotExist(err) {
		t.Fatal("owned file not cleaned")
	}
}
