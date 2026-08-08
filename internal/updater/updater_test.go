package updater

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestSignedCheckAndHumanConfirmedAtomicInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("self-update is intentionally package-managed on Windows")
	}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	artifact := []byte("#!/bin/sh\nif [ \"${1:-}\" = \"--version\" ]; then printf 'glab-axi 0.3.0 (contract glab-axi/v1)\\n'; exit 0; fi\nexit 2\n")
	hash := sha256.Sum256(artifact)
	var manifestBytes []byte
	var artifactRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(manifestBytes)
		case "/artifact":
			artifactRequests.Add(1)
			_, _ = w.Write(artifact)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	manifest := Manifest{
		Schema: ManifestSchema, Version: "0.3.0", PublishedAt: "2026-08-08T00:00:00Z",
		Artifacts: []Artifact{{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, URL: server.URL + "/artifact", SHA256: hex.EncodeToString(hash[:]), Size: int64(len(artifact))}},
	}
	manifestBytes = signManifest(t, manifest, private)
	target := filepath.Join(t.TempDir(), "glab-axi")
	if err := os.WriteFile(target, []byte("old-binary"), 0o700); err != nil {
		t.Fatal(err)
	}
	base := Config{
		CurrentVersion: "0.2.0", ManifestURL: server.URL + "/manifest",
		PublicKey: base64.StdEncoding.EncodeToString(public), HTTPClient: server.Client(),
		Executable: target, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, TestOrigin: server.URL,
	}
	result, err := Run(context.Background(), true, base)
	if err != nil || !result.UpdateAvailable || result.Installed || artifactRequests.Load() != 0 {
		t.Fatalf("check result=%#v requests=%d err=%v", result, artifactRequests.Load(), err)
	}
	before, _ := os.ReadFile(target)
	if string(before) != "old-binary" {
		t.Fatal("check-only changed executable")
	}

	base.IsTerminal = func() bool { return true }
	base.Stdin = strings.NewReader("yes\n")
	base.Stderr = &strings.Builder{}
	result, err = Run(context.Background(), false, base)
	if err != nil || !result.Installed || artifactRequests.Load() != 1 {
		t.Fatalf("install result=%#v requests=%d err=%v", result, artifactRequests.Load(), err)
	}
	after, err := os.ReadFile(target)
	if err != nil || string(after) != string(artifact) {
		t.Fatalf("installed bytes mismatch: %v", err)
	}
	if _, err := os.Stat(target + ".glab-axi-backup"); !os.IsNotExist(err) {
		t.Fatalf("backup remained: %v", err)
	}
}

func TestUpdateRejectsSignatureAndChecksumBeforeReplacement(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	artifact := []byte("not-the-signed-bytes")
	var manifestBytes []byte
	var artifactRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest" {
			_, _ = w.Write(manifestBytes)
			return
		}
		artifactRequests.Add(1)
		_, _ = w.Write(artifact)
	}))
	defer server.Close()
	target := filepath.Join(t.TempDir(), "glab-axi")
	if err := os.WriteFile(target, []byte("old"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Schema: ManifestSchema, Version: "0.3.0", Artifacts: []Artifact{{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, URL: server.URL + "/artifact", SHA256: strings.Repeat("0", 64), Size: int64(len(artifact))}}}
	manifestBytes = signManifest(t, manifest, private)
	config := Config{CurrentVersion: "0.2.0", ManifestURL: server.URL + "/manifest", PublicKey: base64.StdEncoding.EncodeToString(public), HTTPClient: server.Client(), Executable: target, GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, TestOrigin: server.URL, IsTerminal: func() bool { return true }, Stdin: strings.NewReader("yes\n"), Stderr: &strings.Builder{}}
	if runtime.GOOS != "windows" {
		if _, err := Run(context.Background(), false, config); err == nil {
			t.Fatal("checksum mismatch was accepted")
		}
		data, _ := os.ReadFile(target)
		if string(data) != "old" {
			t.Fatal("checksum failure changed executable")
		}
	}

	manifest.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	manifestBytes, _ = json.Marshal(manifest)
	artifactRequests.Store(0)
	if _, err := Run(context.Background(), true, config); err == nil {
		t.Fatal("invalid signature was accepted")
	}
	if artifactRequests.Load() != 0 {
		t.Fatal("invalid signature reached artifact download")
	}
}

func TestApplyRequiresTTYBeforeArtifactDownload(t *testing.T) {
	public, private, _ := ed25519.GenerateKey(rand.Reader)
	artifact := []byte("candidate")
	hash := sha256.Sum256(artifact)
	var manifestBytes []byte
	var artifactRequests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/manifest" {
			_, _ = w.Write(manifestBytes)
			return
		}
		artifactRequests.Add(1)
		_, _ = w.Write(artifact)
	}))
	defer server.Close()
	manifestBytes = signManifest(t, Manifest{Schema: ManifestSchema, Version: "0.3.0", Artifacts: []Artifact{{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH, URL: server.URL + "/artifact", SHA256: hex.EncodeToString(hash[:]), Size: int64(len(artifact))}}}, private)
	config := Config{CurrentVersion: "0.2.0", ManifestURL: server.URL + "/manifest", PublicKey: base64.StdEncoding.EncodeToString(public), HTTPClient: server.Client(), TestOrigin: server.URL, IsTerminal: func() bool { return false }}
	if _, err := Run(context.Background(), false, config); err == nil {
		t.Fatal("non-TTY apply was accepted")
	}
	if artifactRequests.Load() != 0 {
		t.Fatal("non-TTY apply downloaded artifact")
	}
}

func TestManagedExecutablePathDetection(t *testing.T) {
	for _, path := range []string{
		"/usr/local/bin/glab-axi",
		"/opt/homebrew/Cellar/glab-axi/0.2.0/bin/glab-axi",
		"/home/linuxbrew/.linuxbrew/Cellar/glab-axi/0.2.0/bin/glab-axi",
		"/nix/store/abc-glab-axi/bin/glab-axi",
		"/home/user/.local/share/mise/installs/glab-axi/0.2.0/bin/glab-axi",
	} {
		if !managedExecutablePath(path) {
			t.Fatalf("managed path was accepted: %s", path)
		}
	}
	if path := filepath.Join(t.TempDir(), "glab-axi"); managedExecutablePath(path) {
		t.Fatalf("private standalone path was classified as managed: %s", path)
	}
}

func signManifest(t *testing.T, manifest Manifest, private ed25519.PrivateKey) []byte {
	t.Helper()
	payload, err := CanonicalPayload(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Signature = base64.StdEncoding.EncodeToString(ed25519.Sign(private, payload))
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
