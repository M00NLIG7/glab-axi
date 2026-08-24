// Package config owns non-secret, security-sensitive host authority metadata.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gl-axi/internal/contract/v1"
	"gl-axi/internal/safeurl"
)

const Schema = "glab-axi/config-v1"

type Config struct {
	Schema string          `json:"schema"`
	Hosts  map[string]Host `json:"hosts"`
}

type Host struct {
	GitHosts      []string `json:"git_hosts"`
	APIBase       string   `json:"api_base"`
	WebBase       string   `json:"web_base"`
	CABundle      string   `json:"ca_bundle,omitempty"`
	ProxyDisabled bool     `json:"proxy_disabled,omitempty"`
}

type ResolvedHost struct {
	Name          string
	Authority     safeurl.Authority
	CABundle      string
	ProxyDisabled bool
	Explicit      bool
}

func New() Config { return Config{Schema: Schema, Hosts: make(map[string]Host)} }

func Path() (string, error) {
	explicit, err := compatiblePathEnv("GL_AXI_CONFIG", "GLAB_AXI_CONFIG")
	if err != nil {
		return "", err
	}
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", v1.NewError(v1.CodeValidation, "GL_AXI_CONFIG and GLAB_AXI_CONFIG must be absolute")
		}
		return filepath.Clean(explicit), nil
	}
	dir, err := compatiblePathEnv("GL_AXI_CONFIG_DIR", "GLAB_AXI_CONFIG_DIR")
	if err != nil {
		return "", err
	}
	if dir != "" {
		if !filepath.IsAbs(dir) {
			return "", v1.NewError(v1.CodeValidation, "GL_AXI_CONFIG_DIR and GLAB_AXI_CONFIG_DIR must be absolute")
		}
		return filepath.Join(filepath.Clean(dir), "config.json"), nil
	}
	dir, err = os.UserConfigDir()
	if err != nil {
		return "", v1.Wrap(v1.CodeUpstream, "cannot locate user config directory", err)
	}
	// Keep the established non-secret authority path so both executable names
	// share one configuration without copying or migrating it.
	return filepath.Join(dir, "glab-axi", "config.json"), nil
}

func compatiblePathEnv(canonical, legacy string) (string, error) {
	canonicalValue := os.Getenv(canonical)
	legacyValue := os.Getenv(legacy)
	if canonicalValue != "" && legacyValue != "" && filepath.Clean(canonicalValue) != filepath.Clean(legacyValue) {
		return "", v1.NewError(v1.CodeValidation, canonical+" and "+legacy+" disagree")
	}
	if canonicalValue != "" {
		return canonicalValue, nil
	}
	return legacyValue, nil
}

func Load(path string) (Config, error) {
	cfg := New()
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, v1.Wrap(v1.CodeUpstream, "cannot inspect glab-axi config", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Config{}, v1.NewError(v1.CodeSafety, "glab-axi config must be a regular file, not a symlink")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return Config{}, v1.NewError(v1.CodeSafety, "glab-axi config permissions must be 0600 or stricter")
	}
	if err := verifyOwner(info); err != nil {
		return Config{}, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Config{}, v1.Wrap(v1.CodeUpstream, "cannot open glab-axi config", err)
	}
	defer f.Close()
	dec := json.NewDecoder(io.LimitReader(f, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, v1.Wrap(v1.CodeValidation, "invalid glab-axi config", err)
	}
	if err := requireJSONEOF(dec); err != nil {
		return Config{}, err
	}
	if cfg.Schema != Schema || cfg.Hosts == nil {
		return Config{}, v1.NewError(v1.CodeValidation, "unsupported glab-axi config schema")
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func requireJSONEOF(dec *json.Decoder) error {
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return v1.NewError(v1.CodeValidation, "glab-axi config contains trailing data")
		}
		return v1.Wrap(v1.CodeValidation, "invalid glab-axi config", err)
	}
	return nil
}

func (c Config) Validate() error {
	if c.Schema != Schema || c.Hosts == nil {
		return v1.NewError(v1.CodeValidation, "unsupported glab-axi config schema")
	}
	allGitHosts := map[string]string{}
	for name := range c.Hosts {
		allGitHosts[name] = name
	}
	for name, host := range c.Hosts {
		if name != strings.ToLower(name) {
			return v1.NewError(v1.CodeValidation, "configured host names must be lowercase")
		}
		if err := safeurl.ValidateHost(name); err != nil {
			return err
		}
		if _, err := safeurl.NewAuthority(name, host.APIBase, host.WebBase); err != nil {
			return fmt.Errorf("host %s: %w", name, err)
		}
		if len(host.GitHosts) == 0 {
			return v1.NewError(v1.CodeValidation, "each configured host requires at least one git host")
		}
		seen := map[string]bool{}
		for _, gitHost := range host.GitHosts {
			if gitHost != strings.ToLower(gitHost) {
				return v1.NewError(v1.CodeValidation, "configured git hosts must be lowercase")
			}
			if err := safeurl.ValidateHost(gitHost); err != nil {
				return err
			}
			if seen[gitHost] {
				return v1.NewError(v1.CodeValidation, "duplicate configured git host")
			}
			seen[gitHost] = true
			if owner, exists := allGitHosts[gitHost]; exists && owner != name {
				return v1.NewError(v1.CodeConflict, "a git host maps to multiple configured authorities")
			}
			allGitHosts[gitHost] = name
		}
		if host.CABundle != "" && !filepath.IsAbs(host.CABundle) {
			return v1.NewError(v1.CodeValidation, "CA bundle path must be absolute")
		}
	}
	return nil
}

func (c Config) Resolve(host string) (ResolvedHost, error) {
	host = strings.ToLower(host)
	if err := safeurl.ValidateHost(host); err != nil {
		return ResolvedHost{}, err
	}
	matches := make([]string, 0, 1)
	if _, ok := c.Hosts[host]; ok {
		matches = append(matches, host)
	} else {
		for name, entry := range c.Hosts {
			for _, gitHost := range entry.GitHosts {
				if strings.EqualFold(gitHost, host) {
					matches = append(matches, name)
					break
				}
			}
		}
	}
	if len(matches) > 1 {
		sort.Strings(matches)
		return ResolvedHost{}, v1.NewError(v1.CodeConflict, "Git host maps to multiple configured authorities")
	}
	if len(matches) == 1 {
		name := matches[0]
		entry := c.Hosts[name]
		authority, err := safeurl.NewAuthority(name, entry.APIBase, entry.WebBase)
		if err != nil {
			return ResolvedHost{}, err
		}
		return ResolvedHost{Name: name, Authority: authority, CABundle: entry.CABundle, ProxyDisabled: entry.ProxyDisabled, Explicit: true}, nil
	}
	if host == "gitlab.com" {
		authority, _ := safeurl.NewAuthority("gitlab.com", "https://gitlab.com/api/v4", "https://gitlab.com")
		return ResolvedHost{Name: "gitlab.com", Authority: authority}, nil
	}
	return ResolvedHost{}, v1.NewError(v1.CodeSafety, "private GitLab host is not explicitly configured")
}

func (c *Config) Put(name string, host Host) error {
	if c.Hosts == nil {
		c.Hosts = make(map[string]Host)
	}
	name = strings.ToLower(name)
	host.GitHosts = normalizeHosts(host.GitHosts)
	old, existed := c.Hosts[name]
	oldSchema := c.Schema
	c.Schema = Schema
	c.Hosts[name] = host
	if err := c.Validate(); err != nil {
		if existed {
			c.Hosts[name] = old
		} else {
			delete(c.Hosts, name)
		}
		c.Schema = oldSchema
		return err
	}
	return nil
}

func normalizeHosts(hosts []string) []string {
	out := make([]string, 0, len(hosts))
	seen := map[string]bool{}
	for _, host := range hosts {
		host = strings.ToLower(host)
		if !seen[host] {
			seen[host] = true
			out = append(out, host)
		}
	}
	sort.Strings(out)
	return out
}

func Save(path string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := secureDir(dir); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return v1.NewError(v1.CodeSafety, "refusing to replace unsafe glab-axi config")
		}
		if err := verifyOwner(info); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return v1.Wrap(v1.CodeUpstream, "cannot inspect glab-axi config", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return v1.Wrap(v1.CodeInternal, "cannot encode glab-axi config", err)
	}
	data = append(data, '\n')
	tmp, err := os.CreateTemp(dir, ".config-*")
	if err != nil {
		return v1.Wrap(v1.CodeUpstream, "cannot create glab-axi config", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return v1.Wrap(v1.CodeUpstream, "cannot secure glab-axi config", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return v1.Wrap(v1.CodeUpstream, "cannot write glab-axi config", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return v1.Wrap(v1.CodeUpstream, "cannot sync glab-axi config", err)
	}
	if err := tmp.Close(); err != nil {
		return v1.Wrap(v1.CodeUpstream, "cannot close glab-axi config", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return v1.Wrap(v1.CodeUpstream, "cannot install glab-axi config", err)
	}
	return nil
}

func secureDir(dir string) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return v1.Wrap(v1.CodeUpstream, "cannot create glab-axi config directory", err)
	}
	info, err := os.Lstat(dir)
	if err != nil {
		return v1.Wrap(v1.CodeUpstream, "cannot inspect glab-axi config directory", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return v1.NewError(v1.CodeSafety, "glab-axi config directory permissions must be 0700 or stricter")
	}
	return verifyOwner(info)
}
