// Package redact removes credential material before untrusted text reaches an
// error, log, or user-visible stream.
package redact

import (
	"net/url"
	"regexp"
	"strings"
	"sync"
	"unicode/utf8"
)

var (
	headerPattern = regexp.MustCompile(`(?i)(authorization|private-token|proxy-authorization)\s*:\s*[^\r\n]+`)
	tokenPattern  = regexp.MustCompile(`(?i)\b(glpat-[A-Za-z0-9_-]{8,}|gloas-[A-Za-z0-9_-]{8,}|glrt-[A-Za-z0-9_-]{8,})\b`)
	queryPattern  = regexp.MustCompile(`(?i)([?&](?:access_token|private_token|token|oauth_token)=)[^&#\s]+`)
)

type Redactor struct {
	mu      sync.RWMutex
	secrets []string
}

func New(secrets ...string) *Redactor {
	r := &Redactor{}
	for _, secret := range secrets {
		r.Add(secret)
	}
	return r
}

func (r *Redactor) Add(secret string) {
	if secret == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, known := range r.secrets {
		if known == secret {
			return
		}
	}
	r.secrets = append(r.secrets, secret)
}

func (r *Redactor) String(input string) string {
	out := input
	r.mu.RLock()
	for _, secret := range r.secrets {
		out = strings.ReplaceAll(out, secret, "[REDACTED]")
	}
	r.mu.RUnlock()
	out = headerPattern.ReplaceAllString(out, "$1: [REDACTED]")
	out = tokenPattern.ReplaceAllString(out, "[REDACTED]")
	out = queryPattern.ReplaceAllString(out, "$1[REDACTED]")
	out = redactURLUserinfo(out)
	return out
}

func (r *Redactor) Bounded(input string, max int) string {
	out := r.String(input)
	if max < 0 || len(out) <= max {
		return out
	}
	if max <= len("…[truncated]") {
		return "[truncated]"[:min(max, len("[truncated]"))]
	}
	cut := max - len("…[truncated]")
	for cut > 0 && !utf8.RuneStart(out[cut]) {
		cut--
	}
	return out[:cut] + "…[truncated]"
}

func redactURLUserinfo(input string) string {
	fields := strings.Fields(input)
	if len(fields) == 0 {
		return input
	}
	out := input
	for _, field := range fields {
		trimmed := strings.Trim(field, "\"'()[]{}<>,;")
		u, err := url.Parse(trimmed)
		if err == nil && u.User != nil && (u.Scheme == "http" || u.Scheme == "https") {
			u.User = url.User("[REDACTED]")
			out = strings.ReplaceAll(out, trimmed, u.String())
		}
	}
	return out
}
