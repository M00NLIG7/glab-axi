// Package updater verifies a signed release manifest and performs an explicit,
// human-confirmed atomic executable replacement. It is never called implicitly.
package updater

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"glab-axi/internal/contract/uxv1"
)

const (
	ManifestSchema  = "glab-axi/update-manifest/v1"
	maxManifestSize = 1 << 20
	maxArtifactSize = 128 << 20
)

type Manifest struct {
	Schema      string     `json:"schema"`
	Version     string     `json:"version"`
	PublishedAt string     `json:"published_at"`
	Artifacts   []Artifact `json:"artifacts"`
	Signature   string     `json:"signature"`
}

type Artifact struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

type Result struct {
	CurrentVersion  string `json:"current_version"`
	LatestVersion   string `json:"latest_version"`
	UpdateAvailable bool   `json:"update_available"`
	Installed       bool   `json:"installed"`
}

type Config struct {
	CurrentVersion string
	ManifestURL    string
	PublicKey      string
	HTTPClient     *http.Client
	Executable     string
	GOOS           string
	GOARCH         string
	IsTerminal     func() bool
	Stdin          io.Reader
	Stderr         io.Writer
	TestOrigin     string
}

func Run(ctx context.Context, checkOnly bool, config Config) (Result, error) {
	applyDefaults(&config)
	manifest, err := loadManifest(ctx, config)
	if err != nil {
		return Result{}, err
	}
	comparison, err := compareVersions(config.CurrentVersion, manifest.Version)
	if err != nil {
		return Result{}, err
	}
	result := Result{CurrentVersion: config.CurrentVersion, LatestVersion: manifest.Version, UpdateAvailable: comparison < 0}
	if checkOnly || !result.UpdateAvailable {
		return result, nil
	}
	if config.GOOS == "windows" {
		return Result{}, uxv1.NewError(uxv1.CodeSafety, "self-update is unavailable on Windows; use the signed release package channel")
	}
	if config.IsTerminal == nil || !config.IsTerminal() {
		return Result{}, uxv1.NewError(uxv1.CodeInteractiveRequired, "installing an update requires a human terminal; use update --check from agents")
	}
	confirmed, err := confirm(config.Stdin, config.Stderr, manifest.Version)
	if err != nil {
		return Result{}, err
	}
	if !confirmed {
		return Result{}, uxv1.NewError(uxv1.CodeCanceled, "update canceled by human")
	}
	artifact, err := selectArtifact(manifest, config.GOOS, config.GOARCH, config.TestOrigin)
	if err != nil {
		return Result{}, err
	}
	if err := install(ctx, config, manifest.Version, artifact); err != nil {
		return Result{}, err
	}
	result.Installed = true
	return result, nil
}

func applyDefaults(config *Config) {
	if config.HTTPClient == nil {
		client := &http.Client{Timeout: 45 * time.Second}
		client.CheckRedirect = safeRedirect
		config.HTTPClient = client
	}
	if config.GOOS == "" {
		config.GOOS = runtime.GOOS
	}
	if config.GOARCH == "" {
		config.GOARCH = runtime.GOARCH
	}
	if config.Stdin == nil {
		config.Stdin = os.Stdin
	}
	if config.Stderr == nil {
		config.Stderr = os.Stderr
	}
	if config.Executable == "" {
		config.Executable, _ = os.Executable()
	}
}

func loadManifest(ctx context.Context, config Config) (Manifest, error) {
	publicKey, err := base64.StdEncoding.DecodeString(config.PublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return Manifest{}, uxv1.NewError(uxv1.CodeSafety, "this build has no valid release-signing public key; refusing update")
	}
	manifestURL, err := url.Parse(config.ManifestURL)
	if err != nil || !allowedURL(manifestURL, config.TestOrigin) {
		return Manifest{}, uxv1.NewError(uxv1.CodeSafety, "update manifest URL is outside the signed distribution authority")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL.String(), nil)
	if err != nil {
		return Manifest{}, uxv1.Wrap(uxv1.CodeInternal, "cannot construct update manifest request", err)
	}
	response, err := config.HTTPClient.Do(request)
	if err != nil {
		return Manifest{}, uxv1.Wrap(uxv1.CodeUpstream, "cannot fetch signed update manifest", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return Manifest{}, uxv1.NewError(uxv1.CodeUpstream, "signed update manifest is unavailable")
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxManifestSize+1))
	if err != nil || len(body) > maxManifestSize {
		return Manifest{}, uxv1.NewError(uxv1.CodeUpstream, "signed update manifest exceeded the safety limit")
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, uxv1.Wrap(uxv1.CodeUpstream, "signed update manifest is malformed", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return Manifest{}, uxv1.NewError(uxv1.CodeUpstream, "signed update manifest contains trailing data")
	}
	if manifest.Schema != ManifestSchema || len(manifest.Artifacts) == 0 || len(manifest.Artifacts) > 12 {
		return Manifest{}, uxv1.NewError(uxv1.CodeSafety, "signed update manifest has an unsupported schema")
	}
	if _, err := parseVersion(manifest.Version); err != nil {
		return Manifest{}, err
	}
	signature, err := base64.StdEncoding.DecodeString(manifest.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return Manifest{}, uxv1.NewError(uxv1.CodeSafety, "update manifest signature is invalid")
	}
	payload, err := CanonicalPayload(manifest)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), payload, signature) {
		return Manifest{}, uxv1.NewError(uxv1.CodeSafety, "update manifest signature verification failed")
	}
	return manifest, nil
}

func CanonicalPayload(manifest Manifest) ([]byte, error) {
	type signedManifest struct {
		Schema      string     `json:"schema"`
		Version     string     `json:"version"`
		PublishedAt string     `json:"published_at"`
		Artifacts   []Artifact `json:"artifacts"`
	}
	return json.Marshal(signedManifest{Schema: manifest.Schema, Version: manifest.Version, PublishedAt: manifest.PublishedAt, Artifacts: manifest.Artifacts})
}

func selectArtifact(manifest Manifest, goos, goarch, testOrigin string) (Artifact, error) {
	var selected []Artifact
	for _, artifact := range manifest.Artifacts {
		if artifact.GOOS == goos && artifact.GOARCH == goarch {
			selected = append(selected, artifact)
		}
	}
	if len(selected) != 1 {
		return Artifact{}, uxv1.NewError(uxv1.CodeSafety, "signed manifest must contain exactly one artifact for this platform")
	}
	artifact := selected[0]
	if artifact.Size < 1 || artifact.Size > maxArtifactSize || len(artifact.SHA256) != sha256.Size*2 {
		return Artifact{}, uxv1.NewError(uxv1.CodeSafety, "signed artifact metadata violates the safety limits")
	}
	if _, err := hex.DecodeString(artifact.SHA256); err != nil {
		return Artifact{}, uxv1.NewError(uxv1.CodeSafety, "signed artifact checksum is invalid")
	}
	parsed, err := url.Parse(artifact.URL)
	if err != nil || !allowedURL(parsed, testOrigin) {
		return Artifact{}, uxv1.NewError(uxv1.CodeSafety, "signed artifact URL is outside the distribution authority")
	}
	return artifact, nil
}

func install(ctx context.Context, config Config, version string, artifact Artifact) error {
	target, err := filepath.Abs(config.Executable)
	if err != nil {
		return uxv1.Wrap(uxv1.CodeSafety, "cannot resolve current executable", err)
	}
	linkInfo, err := os.Lstat(target)
	if err != nil || linkInfo.Mode()&os.ModeSymlink != 0 || !linkInfo.Mode().IsRegular() {
		return uxv1.NewError(uxv1.CodeSafety, "self-update refuses symlinked or managed executable paths; use the package channel")
	}
	dir := filepath.Dir(target)
	temp, err := os.CreateTemp(dir, ".glab-axi-update-*")
	if err != nil {
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot create update candidate", err)
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	defer temp.Close()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return uxv1.Wrap(uxv1.CodeInternal, "cannot construct artifact request", err)
	}
	response, err := config.HTTPClient.Do(request)
	if err != nil {
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot fetch signed update artifact", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.ContentLength > artifact.Size && response.ContentLength >= 0 {
		return uxv1.NewError(uxv1.CodeUpstream, "update artifact is unavailable or oversized")
	}
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(response.Body, artifact.Size+1))
	if err != nil || written != artifact.Size {
		return uxv1.NewError(uxv1.CodeUpstream, "update artifact size did not match the signed manifest")
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	if subtle.ConstantTimeCompare([]byte(strings.ToLower(actual)), []byte(strings.ToLower(artifact.SHA256))) != 1 {
		return uxv1.NewError(uxv1.CodeSafety, "update artifact checksum verification failed")
	}
	mode := linkInfo.Mode().Perm()
	if mode&0o111 == 0 {
		mode = 0o700
	}
	if err := temp.Chmod(mode); err != nil || temp.Sync() != nil || temp.Close() != nil {
		return uxv1.NewError(uxv1.CodeUpstream, "cannot finalize update candidate")
	}
	if err := verifyCandidate(ctx, tempPath, version); err != nil {
		return err
	}
	currentInfo, err := os.Lstat(target)
	if err != nil || !os.SameFile(linkInfo, currentInfo) {
		return uxv1.NewError(uxv1.CodeSafety, "installed executable changed during update")
	}
	backup := target + ".glab-axi-backup"
	if _, err := os.Lstat(backup); err == nil || !errors.Is(err, os.ErrNotExist) {
		return uxv1.NewError(uxv1.CodeSafety, "update backup path already exists")
	}
	if err := os.Rename(target, backup); err != nil {
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot stage current executable for update", err)
	}
	if err := os.Rename(tempPath, target); err != nil {
		rollbackErr := os.Rename(backup, target)
		if rollbackErr != nil {
			return uxv1.Wrap(uxv1.CodeSafety, "update failed and executable rollback was incomplete", errors.Join(err, rollbackErr))
		}
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot install update candidate", err)
	}
	if err := os.Remove(backup); err != nil {
		return uxv1.Wrap(uxv1.CodeSafety, "update installed but backup cleanup failed", err)
	}
	if directory, err := os.Open(dir); err == nil {
		_ = directory.Sync()
		_ = directory.Close()
	}
	return nil
}

func verifyCandidate(ctx context.Context, path, version string) error {
	verifyCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(verifyCtx, path, "--version")
	cmd.Env = []string{"PATH=/usr/bin:/bin", "NO_COLOR=1"}
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || stdout.Len() > 4096 {
		return uxv1.NewError(uxv1.CodeSafety, "update candidate failed its standalone version handshake")
	}
	want := fmt.Sprintf("glab-axi %s (contract glab-axi/v1)\n", version)
	if stdout.String() != want {
		return uxv1.NewError(uxv1.CodeSafety, "update candidate returned the wrong version or contract handshake")
	}
	return nil
}

func confirm(input io.Reader, output io.Writer, version string) (bool, error) {
	_, _ = fmt.Fprintf(output, "Install signed glab-axi %s now? [y/N] ", version)
	reader := bufio.NewReader(io.LimitReader(input, 32))
	line, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return false, uxv1.Wrap(uxv1.CodeUpstream, "cannot read update confirmation", err)
	}
	value := strings.ToLower(strings.TrimSpace(line))
	return value == "y" || value == "yes", nil
}

func allowedURL(parsed *url.URL, testOrigin string) bool {
	if parsed == nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return false
	}
	if testOrigin != "" {
		origin, err := url.Parse(testOrigin)
		return err == nil && parsed.Scheme == origin.Scheme && parsed.Host == origin.Host
	}
	host := strings.ToLower(parsed.Hostname())
	return host == "github.com" || host == "objects.githubusercontent.com" || host == "release-assets.githubusercontent.com"
}

func safeRedirect(request *http.Request, via []*http.Request) error {
	if len(via) >= 3 || !allowedURL(request.URL, "") {
		return errors.New("unsafe update redirect")
	}
	return nil
}

func compareVersions(current, latest string) (int, error) {
	left, err := parseVersion(current)
	if err != nil {
		return 0, err
	}
	right, err := parseVersion(latest)
	if err != nil {
		return 0, err
	}
	for index := range left {
		if left[index] < right[index] {
			return -1, nil
		}
		if left[index] > right[index] {
			return 1, nil
		}
	}
	return 0, nil
}

func parseVersion(value string) ([3]int, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return [3]int{}, uxv1.NewError(uxv1.CodeSafety, "release version is not canonical semantic version")
	}
	var parsed [3]int
	for index, part := range parts {
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 || strconv.Itoa(value) != part {
			return [3]int{}, uxv1.NewError(uxv1.CodeSafety, "release version is not canonical semantic version")
		}
		parsed[index] = value
	}
	return parsed, nil
}
