package product

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	runtimepkg "gl-axi/internal/runtime"
	"gl-axi/internal/safedownload"
)

func runDownloadFixture(t *testing.T, ctx context.Context, f *downloadCLIFixture, kind string, client *http.Client) (downloadCLIResult, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	deps := Dependencies{
		Runtime: runtimepkg.Dependencies{
			Stdout: &stdout, Stderr: &stderr, Cwd: f.home, ConfigPath: f.configPath,
			HTTPClient: client,
			LookupEnv: func(name string) (string, bool) {
				if name == "GL_AXI_TOKEN" {
					return f.token, true
				}
				return "", false
			},
		},
		NewDelegate: func() delegateClient {
			t.Fatal("native download delegated")
			return nil
		},
	}
	code := Run(ctx, f.command("unused", kind).Args[1:], deps)
	var result downloadCLIResult
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatalf("decode: %v output=%s stderr=%s", err, &stdout, &stderr)
	}
	if strings.Contains(stdout.String()+stderr.String(), f.token) {
		t.Fatal("credential appeared in output")
	}
	if result.Meta.Backend != "native" {
		t.Fatalf("backend=%q", result.Meta.Backend)
	}
	return result, code
}

func TestDownloadRawAssetWithLargeArchive(t *testing.T) {
	asset := bytes.Repeat([]byte("x"), 1<<20)
	for _, tc := range []struct {
		kind, mode, errorCode string
		transferred           bool
	}{
		{"release", "ok", "", true},
		{"artifacts", "ok", "", false},
		{"download", "ok", "safety_violation", false},
		{"release", "wrong-job", "safety_violation", false},
		{"release", "wrong-pipeline", "safety_violation", false},
		{"release", "wrong-ref", "safety_violation", false},
		{"release", "wrong-sha", "safety_violation", false},
		{"release", "drift", "safety_violation", true},
		{"release", "oversize", "upstream_error", true},
	} {
		t.Run(tc.kind+"/"+tc.mode, func(t *testing.T) {
			f := newDownloadCLIFixture(t, tc.mode)
			f.rawJob = true
			f.archiveSize = safedownload.MaxArchiveBytes + 1
			f.asset = asset
			result, code := runDownloadFixture(t, context.Background(), f, tc.kind, nil)
			ok := tc.errorCode == ""
			if result.OK != ok || (code == 0) != ok || result.Error.Code != tc.errorCode {
				t.Fatalf("result=%+v exit=%d", result, code)
			}
			f.mu.Lock()
			transferred := f.transferred
			f.mu.Unlock()
			if transferred != tc.transferred {
				t.Fatalf("transferred=%v want=%v", transferred, tc.transferred)
			}
			if ok && tc.kind == "release" {
				body, err := os.ReadFile(filepath.Join(f.destination, "app.bin"))
				if err != nil || !bytes.Equal(body, asset) {
					t.Fatalf("raw asset bytes=%d err=%v", len(body), err)
				}
				receipt := result.Data.Download
				if receipt.Bytes != int64(len(asset)) || receipt.JobID != 42 || receipt.PipelineID != 71 || receipt.Checksum != "sha256_receipt_only" {
					t.Fatalf("receipt=%+v", receipt)
				}
			} else {
				if _, err := os.Stat(f.destination); !os.IsNotExist(err) {
					t.Fatal("destination published without a successful download")
				}
				if ok && result.Data.Artifacts.Size != f.archiveSize {
					t.Fatalf("metadata=%+v", result.Data.Artifacts)
				}
			}
			assertDownloadCleanup(t, f)
		})
	}
}

type downloadTestWriter struct{ io.Writer }

func (downloadTestWriter) Close() error { return nil }

type downloadTestReader struct {
	io.Reader
	afterRead, onClose func()
}

func (r downloadTestReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p[:min(len(p), 1)])
	if r.afterRead != nil {
		r.afterRead()
	}
	return n, err
}

func (r downloadTestReader) Close() error {
	if r.onClose != nil {
		r.onClose()
	}
	return nil
}

var downloadTestZIPMethod atomic.Uint32

func TestDownloadCancellationDuringExtractionAndPublication(t *testing.T) {
	for _, phase := range []string{"inspection", "extraction", "publication"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f := newDownloadCLIFixture(t, "ok")
			method := uint16(60000 + downloadTestZIPMethod.Add(1))
			opens, canceled := 0, false
			zip.RegisterDecompressor(method, func(r io.Reader) io.ReadCloser {
				opens++
				reader := downloadTestReader{Reader: r}
				stop := func() { canceled = true; cancel() }
				if phase == "inspection" && opens == 1 || phase == "extraction" && opens == 2 {
					reader.afterRead = stop
				}
				if phase == "publication" && opens == 2 {
					reader.onClose = stop
				}
				return reader
			})
			var data bytes.Buffer
			archive := zip.NewWriter(&data)
			archive.RegisterCompressor(method, func(w io.Writer) (io.WriteCloser, error) { return downloadTestWriter{w}, nil })
			file, err := archive.CreateHeader(&zip.FileHeader{Name: "bin/app.txt", Method: method})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(file, "artifact contents"); err != nil {
				t.Fatal(err)
			}
			if err := archive.Close(); err != nil {
				t.Fatal(err)
			}
			f.archive, f.archiveSize = data.Bytes(), int64(data.Len())
			result, code := runDownloadFixture(t, ctx, f, "download", nil)
			if !canceled || result.OK || result.Error.Code != "canceled" || code != 130 {
				t.Fatalf("canceled=%v result=%+v exit=%d", canceled, result, code)
			}
			if _, err := os.Stat(f.destination); !os.IsNotExist(err) {
				t.Fatal("cancellation published destination")
			}
			assertDownloadCleanup(t, f)
		})
	}
}

type downloadTestTransport func(*http.Request) (*http.Response, error)

func (f downloadTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type downloadTestBody struct {
	io.ReadCloser
	afterClose func()
}

func (b downloadTestBody) Close() error {
	err := b.ReadCloser.Close()
	b.afterClose()
	return err
}

func TestDownloadReleaseCancellationBeforeStaging(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newDownloadCLIFixture(t, "ok")
	transport := f.server.Client().Transport
	canceled := false
	client := &http.Client{Transport: downloadTestTransport(func(r *http.Request) (*http.Response, error) {
		response, err := transport.RoundTrip(r)
		f.mu.Lock()
		transferred := f.transferred
		f.mu.Unlock()
		if err == nil && transferred && r.URL.Path == "/api/v4/projects/group/project" {
			response.Body = downloadTestBody{response.Body, func() { canceled = true; cancel() }}
		}
		return response, err
	})}
	result, code := runDownloadFixture(t, ctx, f, "release", client)
	if !canceled || result.OK || result.Error.Code != "canceled" || code != 130 {
		t.Fatalf("canceled=%v result=%+v exit=%d", canceled, result, code)
	}
	if _, err := os.Stat(f.destination); !os.IsNotExist(err) {
		t.Fatal("cancellation published destination")
	}
	assertDownloadCleanup(t, f)
}
