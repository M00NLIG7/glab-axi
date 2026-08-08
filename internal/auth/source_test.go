package auth

import (
	"context"
	"strings"
	"testing"

	"glab-axi/internal/config"
	v1 "glab-axi/internal/contract/v1"
	"glab-axi/internal/limits"
	"glab-axi/internal/safeurl"
)

type fakeKeyring struct{ value string }

func (f fakeKeyring) Get(context.Context, string, string) (string, error) { return f.value, nil }
func (f fakeKeyring) Set(context.Context, string, string, string) error   { return nil }
func (f fakeKeyring) Delete(context.Context, string, string) error        { return nil }

func TestResolverRejectsDisagreeingEnvironmentTokens(t *testing.T) {
	values := map[string]string{
		"GLAB_AXI_TOKEN": runtimeCredential("one"),
		"GITLAB_TOKEN":   runtimeCredential("two"),
	}
	resolver := Resolver{Lookup: func(name string) (string, bool) { value, ok := values[name]; return value, ok }}
	_, err := resolver.Resolve(context.Background(), resolvedHost(t))
	if v1.ExitCode(err) != 3 {
		t.Fatalf("exit=%d, want authentication failure", v1.ExitCode(err))
	}
}

func TestResolverUsesOAuthHeaderKindAndKeyringFallback(t *testing.T) {
	oauth := runtimeCredential("oauth")
	resolver := Resolver{Lookup: func(name string) (string, bool) {
		if name == "OAUTH_TOKEN" {
			return oauth, true
		}
		return "", false
	}}
	credential, err := resolver.Resolve(context.Background(), resolvedHost(t))
	if err != nil || credential.Kind != OAuthToken || credential.Value != oauth {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}

	stored := runtimeCredential("stored")
	resolver = Resolver{Lookup: func(string) (string, bool) { return "", false }, Keyring: fakeKeyring{value: stored}}
	credential, err = resolver.Resolve(context.Background(), resolvedHost(t))
	if err != nil || credential.Kind != PrivateToken || credential.Value != stored || credential.Source != "keyring" {
		t.Fatalf("credential=%+v err=%v", credential, err)
	}
}

func TestReadTokenBoundaries(t *testing.T) {
	value := runtimeCredential("stdin")
	got, err := ReadToken(strings.NewReader(value+"\r\n"), false)
	if err != nil || got != value {
		t.Fatalf("got token mismatch err=%v", err)
	}
	if _, err := ReadToken(strings.NewReader(value), true); v1.ExitCode(err) != 9 {
		t.Fatal("terminal input was not refused")
	}
	if _, err := ReadToken(strings.NewReader(strings.Repeat("x", limits.MaxTokenBytes+1)), false); err == nil {
		t.Fatal("oversized token was accepted")
	}
	if _, err := ReadToken(strings.NewReader("invalid token"), false); err == nil {
		t.Fatal("token whitespace was accepted")
	}
}

func resolvedHost(t *testing.T) config.ResolvedHost {
	t.Helper()
	authority, err := safeurl.NewAuthority("gitlab.example.invalid", "https://gitlab.example.invalid/api/v4", "https://gitlab.example.invalid")
	if err != nil {
		t.Fatal(err)
	}
	return config.ResolvedHost{Name: "gitlab.example.invalid", Authority: authority, Explicit: true}
}

func runtimeCredential(suffix string) string {
	return strings.Join([]string{"glpat", "runtime", suffix, "sentinel"}, "-")
}
