// Package auth resolves noninteractive GitLab credentials without exposing their
// values through argv, config, output, logs, or prompts.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"

	"glab-axi/internal/config"
	"glab-axi/internal/contract/v1"
	"glab-axi/internal/limits"
)

type Kind string

const (
	PrivateToken Kind = "private"
	OAuthToken   Kind = "oauth"
)

type Credential struct {
	Value  string
	Kind   Kind
	Source string
}

type LookupEnv func(string) (string, bool)

type Resolver struct {
	Lookup  LookupEnv
	Keyring Keyring
}

func NewResolver(keyring Keyring) Resolver {
	return Resolver{Lookup: os.LookupEnv, Keyring: keyring}
}

func (r Resolver) Resolve(ctx context.Context, host config.ResolvedHost) (Credential, error) {
	if r.Lookup == nil {
		r.Lookup = os.LookupEnv
	}
	candidates := []struct {
		name string
		kind Kind
	}{
		{"GLAB_AXI_TOKEN", PrivateToken},
		{"GITLAB_TOKEN", PrivateToken},
		{"GITLAB_ACCESS_TOKEN", PrivateToken},
		{"OAUTH_TOKEN", OAuthToken},
	}
	var selected Credential
	for _, candidate := range candidates {
		value, ok := r.Lookup(candidate.name)
		if !ok || value == "" {
			continue
		}
		if err := ValidateToken(value); err != nil {
			return Credential{}, err
		}
		if selected.Value != "" && selected.Value != value {
			return Credential{}, v1.NewError(v1.CodeAuthentication, "multiple GitLab token environment variables disagree")
		}
		if selected.Value == "" {
			selected = Credential{Value: value, Kind: candidate.kind, Source: candidate.name}
		}
	}
	if selected.Value != "" {
		return selected, nil
	}
	if r.Keyring == nil {
		return Credential{}, v1.NewError(v1.CodeAuthentication, "no noninteractive GitLab credential is available")
	}
	value, err := r.Keyring.Get(ctx, ServiceName(host), host.Name)
	if errors.Is(err, ErrKeyringNotFound) || errors.Is(err, ErrKeyringUnavailable) {
		return Credential{}, v1.NewError(v1.CodeAuthentication, "no noninteractive GitLab credential is available")
	}
	if err != nil {
		return Credential{}, v1.Wrap(v1.CodeAuthentication, "cannot read GitLab credential without interaction", err)
	}
	if err := ValidateToken(value); err != nil {
		return Credential{}, err
	}
	return Credential{Value: value, Kind: PrivateToken, Source: "keyring"}, nil
}

func ServiceName(host config.ResolvedHost) string {
	sum := sha256.Sum256([]byte(host.Authority.API.String()))
	return "glab-axi/" + hex.EncodeToString(sum[:16])
}

func ReadToken(r io.Reader, terminal bool) (string, error) {
	if terminal {
		return "", v1.NewError(v1.CodeSafety, "token import requires piped stdin; terminal input is refused")
	}
	data, err := io.ReadAll(io.LimitReader(r, limits.MaxTokenBytes+2))
	if err != nil {
		return "", v1.Wrap(v1.CodeUpstream, "cannot read token from stdin", err)
	}
	defer zero(data)
	if len(data) > limits.MaxTokenBytes+1 {
		return "", v1.NewError(v1.CodeValidation, "token exceeds the input limit")
	}
	value := strings.TrimSuffix(strings.TrimSuffix(string(data), "\n"), "\r")
	if len(value) != len(data) && len(data)-len(value) > 2 {
		return "", v1.NewError(v1.CodeValidation, "token input contains trailing data")
	}
	if err := ValidateToken(value); err != nil {
		return "", err
	}
	return value, nil
}

func ValidateToken(value string) error {
	if len(value) < 8 || len(value) > limits.MaxTokenBytes {
		return v1.NewError(v1.CodeAuthentication, "GitLab token has an invalid length")
	}
	for _, b := range []byte(value) {
		if b < 0x21 || b > 0x7e {
			return v1.NewError(v1.CodeAuthentication, "GitLab token contains invalid characters")
		}
	}
	return nil
}

func zero(data []byte) {
	for i := range data {
		data[i] = 0
	}
}
