package redact

import (
	"strings"
	"testing"
	"unicode/utf8"

	"glab-axi/internal/limits"
)

func TestRedactorRemovesExactHeadersURLsAndPatterns(t *testing.T) {
	secret := strings.Join([]string{"glpat", "runtime", "redaction", "sentinel"}, "-")
	redactor := New(secret)
	input := strings.Join([]string{
		"PRIVATE-TOKEN: " + secret,
		"Authorization: Bearer " + secret,
		"https://user:password@example.invalid/path?access_token=" + secret,
		secret,
	}, "\n")
	output := redactor.String(input)
	if strings.Contains(output, secret) || strings.Contains(output, "password") || !strings.Contains(output, "[REDACTED]") {
		t.Fatalf("redaction failed: %s", output)
	}
}

func TestErrorLimitBoundary(t *testing.T) {
	redactor := New()
	exact := strings.Repeat("e", limits.MaxStderrBytes)
	if got := redactor.Bounded(exact, limits.MaxStderrBytes); len(got) != limits.MaxStderrBytes || strings.Contains(got, "truncated") {
		t.Fatal("exact error boundary was changed")
	}
	if got := redactor.Bounded(exact+"e", limits.MaxStderrBytes); len(got) > limits.MaxStderrBytes || !strings.Contains(got, "truncated") {
		t.Fatal("error boundary+1 was not bounded")
	}
}

func TestBoundedRedactionPreservesUTF8(t *testing.T) {
	redactor := New()
	output := redactor.Bounded(strings.Repeat("界", 100), 40)
	if len(output) > 40 || !utf8.ValidString(output) || !strings.Contains(output, "truncated") {
		t.Fatalf("invalid bounded output: %q (%d)", output, len(output))
	}
}
