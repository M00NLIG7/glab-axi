// Package gitremote extracts repository identity from local Git metadata without
// contacting a remote.
package gitremote

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"gl-axi/internal/contract/v1"
	"gl-axi/internal/limits"
	"gl-axi/internal/safeurl"
)

type Identity struct {
	Host    string
	Project string
}

func Origin(ctx context.Context, dir string) (Identity, error) {
	value, err := gitOutput(ctx, dir, "config", "--get", "remote.origin.url")
	if err != nil {
		return Identity{}, v1.Wrap(v1.CodeValidation, "cannot read origin Git remote", err)
	}
	return Parse(value)
}

func Parse(raw string) (Identity, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 4096 || strings.ContainsAny(raw, "\x00\r\n\\") {
		return Identity{}, v1.NewError(v1.CodeValidation, "invalid origin Git remote")
	}
	var host, project string
	if strings.Contains(raw, "://") {
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "ssh" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" || u.User == nil && u.Scheme == "ssh" {
			return Identity{}, v1.NewError(v1.CodeValidation, "origin must be an SSH or HTTPS GitLab URL")
		}
		if u.Scheme == "https" && u.User != nil {
			return Identity{}, v1.NewError(v1.CodeSafety, "HTTPS origin URL must not contain userinfo")
		}
		if u.User != nil {
			if _, set := u.User.Password(); set {
				return Identity{}, v1.NewError(v1.CodeSafety, "origin URL must not contain a password")
			}
		}
		host = strings.ToLower(u.Host)
		project = strings.TrimPrefix(u.Path, "/")
	} else {
		at := strings.LastIndex(raw, "@")
		colon := strings.Index(raw[at+1:], ":")
		if at < 1 || colon < 1 {
			return Identity{}, v1.NewError(v1.CodeValidation, "origin must be an SSH or HTTPS GitLab URL")
		}
		colon += at + 1
		host = strings.ToLower(raw[at+1 : colon])
		project = raw[colon+1:]
	}
	project = strings.TrimPrefix(project, "/")
	project = strings.TrimSuffix(project, ".git")
	if err := safeurl.ValidateHost(host); err != nil {
		return Identity{}, err
	}
	if err := safeurl.ValidateProject(project); err != nil {
		return Identity{}, err
	}
	return Identity{Host: host, Project: project}, nil
}

func HeadSHA(ctx context.Context, dir string) (string, bool) {
	value, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", false
	}
	value = strings.TrimSpace(value)
	if len(value) != 40 && len(value) != 64 {
		return "", false
	}
	if _, err := hex.DecodeString(value); err != nil {
		return "", false
	}
	return strings.ToLower(value), true
}

func gitOutput(parent context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(parent, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	var stdout boundedBuffer
	stdout.max = limits.MaxProjectBytes + 4096
	cmd.Stdout = &stdout
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		return "", err
	}
	if stdout.truncated {
		return "", errors.New("git output exceeded limit")
	}
	return stdout.buf.String(), nil
}

type boundedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (b *boundedBuffer) Write(p []byte) (int, error) {
	original := len(p)
	remaining := b.max - b.buf.Len()
	if remaining > 0 {
		if len(p) > remaining {
			p = p[:remaining]
		}
		_, _ = b.buf.Write(p)
	}
	if original > remaining {
		b.truncated = true
	}
	return original, nil
}
