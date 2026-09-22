package product

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Exercise the CLI entry point with an injected TLS client and the built-in
// host mapping. Publication does not depend on persisting native config, whose
// Unix permission checks cannot be satisfied on Windows.
func TestDownloadPublicationExecutableAliases(t *testing.T) {
	for _, program := range []string{"gl-axi", "glab-axi"} {
		t.Run(program, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), program)
			if runtime.GOOS == "windows" {
				binary += ".exe"
			}
			build := exec.Command("go", "build", "-trimpath", "-o", binary, "./testdata/download-cli")
			if out, err := build.CombinedOutput(); err != nil {
				t.Fatalf("build: %v %s", err, out)
			}
			testDownloadExecutablePublication(t, binary, func(t *testing.T, mode string) *downloadCLIFixture {
				f := newDownloadTLSFixture(t, mode)
				f.mu.Lock()
				f.hostname, f.origin = "gitlab.com", "https://gitlab.com"
				f.env = []string{
					"GL_AXI_TEST_TLS_ADDRESS=" + f.server.Listener.Addr().String(),
					"GL_AXI_TEST_CA_BUNDLE=" + f.caPath,
				}
				f.mu.Unlock()
				return f
			})
		})
	}
}

func testDownloadExecutablePublication(t *testing.T, binary string, fixture func(*testing.T, string) *downloadCLIFixture) {
	t.Helper()
	for _, tc := range []struct {
		name, kind, mode, destination string
	}{
		{"nested-archive", "download", "ok", "download"},
		{"nested-archive-one-character", "download", "ok", "x"},
		{"release-one-character", "release", "ok", "x"},
		{"nested-owned-rollback", "download", "race-destination", "x"},
		{"release-owned-rollback", "release", "race-destination", "x"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := fixture(t, tc.mode)
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			for _, entry := range []struct{ name, body string }{
				{"bin/app.txt", "artifact contents"},
				{"bin/lib/data.txt", "nested data"},
				{"empty/", ""},
			} {
				file, err := writer.Create(entry.name)
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
			f.mu.Lock()
			f.destination = filepath.Join(f.home, tc.destination)
			f.archive, f.archiveSize = archive.Bytes(), int64(archive.Len())
			f.mu.Unlock()
			command := f.command(binary, tc.kind)
			var stdout, stderr bytes.Buffer
			command.Stdout, command.Stderr = &stdout, &stderr
			runErr := command.Run()
			if strings.Contains(stdout.String()+stderr.String(), f.token) {
				t.Fatal("credential appeared in output")
			}
			t.Logf("argv=%q\nstdout=%s\nstderr=%s", command.Args, &stdout, &stderr)
			var result downloadCLIResult
			if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
				t.Fatalf("decode: %v", err)
			}
			f.mu.Lock()
			transferred := f.transferred
			requests := append([]string(nil), f.requests...)
			f.mu.Unlock()
			t.Logf("provider requests=%q", requests)
			ok := tc.mode == "ok"
			if result.OK != ok || (runErr == nil) != ok || result.Meta.Backend != "native" || !transferred {
				t.Fatalf("result=%+v run=%v transferred=%v", result, runErr, transferred)
			}
			if !ok {
				if command.ProcessState.ExitCode() != 9 || result.Error.Code != "safety_violation" || result.Data.Download != (downloadReceipt{}) {
					t.Fatalf("failed publication returned result=%+v exit=%d", result, command.ProcessState.ExitCode())
				}
				entries, err := os.ReadDir(f.destination)
				if err != nil || len(entries) != 1 || entries[0].Name() != "caller" {
					t.Fatalf("raced destination changed: entries=%v err=%v", entries, err)
				}
				body, err := os.ReadFile(filepath.Join(f.destination, "caller"))
				if err != nil || string(body) != "keep" {
					t.Fatalf("caller file changed: body=%q err=%v", body, err)
				}
				t.Logf("destination contains only caller=%q", body)
			} else {
				want := map[string]string{"bin/app.txt": "artifact contents", "bin/lib/data.txt": "nested data"}
				transferredBytes := f.archive
				checksum := "zip_crc32_and_size;sha256_receipt_only"
				if tc.kind == "release" {
					want = map[string]string{"app.bin": string(f.asset)}
					transferredBytes = f.asset
					checksum = "provider_sha256_and_size"
				}
				var expanded int64
				for name, contents := range want {
					body, err := os.ReadFile(filepath.Join(f.destination, filepath.FromSlash(name)))
					if err != nil || string(body) != contents {
						t.Fatalf("%s: body=%q err=%v", name, body, err)
					}
					expanded += int64(len(body))
					t.Logf("published %s=%q", name, body)
				}
				if tc.kind == "download" {
					entries, err := os.ReadDir(filepath.Join(f.destination, "empty"))
					if err != nil || len(entries) != 0 {
						t.Fatalf("empty archive directory: entries=%v err=%v", entries, err)
					}
				}
				digest := sha256.Sum256(transferredBytes)
				receipt := result.Data.Download
				if receipt.ProjectID != 101 || receipt.Destination != f.destination || receipt.Bytes != int64(len(transferredBytes)) || receipt.SHA256 != hex.EncodeToString(digest[:]) || receipt.Files != len(want) || receipt.ExpandedBytes != expanded || receipt.Checksum != checksum {
					t.Fatalf("receipt=%+v", receipt)
				}
			}
			assertDownloadCleanup(t, f)
			t.Log("no owned staging remains")
		})
	}
}
