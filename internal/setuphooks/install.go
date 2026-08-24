// Package setuphooks installs generated, non-authenticating agent context
// hooks without overwriting unrelated configuration.
package setuphooks

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gl-axi/internal/contract/uxv1"
)

const maxConfigBytes = 1 << 20

type Result struct {
	Status       string   `json:"status"`
	Integrations []string `json:"integrations"`
	Skill        string   `json:"skill"`
}

type update struct {
	path    string
	content []byte
	before  []byte
	existed bool
}

func Install(home, command, skill string) (Result, error) {
	if !filepath.IsAbs(home) || (command != "gl-axi" && command != "gl-axi.exe" && command != "glab-axi" && command != "glab-axi.exe") {
		return Result{}, uxv1.NewError(uxv1.CodeSafety, "setup requires an installed gl-axi or glab-axi compatibility executable on PATH and an absolute home directory")
	}
	targets := []struct {
		relative string
		compute  func([]byte) ([]byte, error)
	}{
		{filepath.Join(".claude", "settings.json"), func(current []byte) ([]byte, error) { return updateHookJSON(current, command) }},
		{filepath.Join(".codex", "hooks.json"), func(current []byte) ([]byte, error) { return updateHookJSON(current, command) }},
		{filepath.Join(".codex", "config.toml"), updateCodexConfig},
		{filepath.Join(".agents", "skills", "gl-axi", "SKILL.md"), func([]byte) ([]byte, error) { return []byte(skill), nil }},
		{filepath.Join(".claude", "skills", "gl-axi", "SKILL.md"), func([]byte) ([]byte, error) { return []byte(skill), nil }},
	}
	updates := make([]update, 0, len(targets))
	for _, target := range targets {
		path := filepath.Join(home, target.relative)
		current, existed, err := readSafeTarget(home, path)
		if err != nil {
			return Result{}, err
		}
		next, err := target.compute(current)
		if err != nil {
			return Result{}, err
		}
		if len(next) > maxConfigBytes {
			return Result{}, uxv1.NewError(uxv1.CodeSafety, "generated setup file exceeds the safety limit")
		}
		updates = append(updates, update{path: path, content: next, before: current, existed: existed})
	}

	written := make([]update, 0, len(updates))
	for _, item := range updates {
		if bytes.Equal(item.content, item.before) && item.existed {
			continue
		}
		if err := writeSafeTarget(home, item.path, item.content); err != nil {
			if rollbackErr := rollback(home, written); rollbackErr != nil {
				return Result{}, uxv1.Wrap(uxv1.CodeSafety, "setup failed and rollback was incomplete", errors.Join(err, rollbackErr))
			}
			return Result{}, err
		}
		written = append(written, item)
	}
	return Result{Status: "installed", Integrations: []string{"Claude Code", "Codex"}, Skill: "glab-axi"}, nil
}

func updateHookJSON(current []byte, command string) ([]byte, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(current)) > 0 {
		decoder := json.NewDecoder(bytes.NewReader(current))
		decoder.UseNumber()
		if err := decoder.Decode(&root); err != nil {
			return nil, uxv1.Wrap(uxv1.CodeValidation, "agent hook config is not valid JSON", err)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return nil, uxv1.NewError(uxv1.CodeValidation, "agent hook config contains trailing JSON")
		}
	}
	if root == nil {
		return nil, uxv1.NewError(uxv1.CodeValidation, "agent hook config must contain a JSON object")
	}
	hooks := map[string]any{}
	if rawHooks, exists := root["hooks"]; exists {
		var ok bool
		hooks, ok = rawHooks.(map[string]any)
		if !ok {
			return nil, uxv1.NewError(uxv1.CodeValidation, "agent hook config hooks field must be an object")
		}
	} else {
		root["hooks"] = hooks
	}
	groups := []any{}
	if rawGroups, exists := hooks["SessionStart"]; exists {
		var ok bool
		groups, ok = rawGroups.([]any)
		if !ok {
			return nil, uxv1.NewError(uxv1.CodeValidation, "agent SessionStart hooks must be an array")
		}
	}
	found := false
	for _, rawGroup := range groups {
		group, ok := rawGroup.(map[string]any)
		if !ok {
			return nil, uxv1.NewError(uxv1.CodeValidation, "agent SessionStart hook group must be an object")
		}
		entries, ok := group["hooks"].([]any)
		if !ok {
			return nil, uxv1.NewError(uxv1.CodeValidation, "agent SessionStart hook entries must be an array")
		}
		for _, rawEntry := range entries {
			entry, ok := rawEntry.(map[string]any)
			if !ok {
				return nil, uxv1.NewError(uxv1.CodeValidation, "agent SessionStart hook entry must be an object")
			}
			if entry["command"] == command {
				entry["type"] = "command"
				entry["timeout"] = json.Number("10")
				found = true
			}
		}
	}
	if !found {
		groups = append(groups, map[string]any{"matcher": "", "hooks": []any{map[string]any{"type": "command", "command": command, "timeout": 10}}})
	}
	hooks["SessionStart"] = groups
	encoded, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, uxv1.Wrap(uxv1.CodeInternal, "cannot encode agent hook config", err)
	}
	return append(encoded, '\n'), nil
}

func updateCodexConfig(current []byte) ([]byte, error) {
	if bytes.Contains(current, []byte{0}) || len(current) > maxConfigBytes {
		return nil, uxv1.NewError(uxv1.CodeValidation, "Codex config is invalid or oversized")
	}
	text := string(current)
	newline := "\n"
	if strings.Contains(text, "\r\n") {
		newline = "\r\n"
	}
	if strings.TrimSpace(text) == "" {
		return []byte("[features]" + newline + "hooks = true" + newline), nil
	}
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	inFeatures := false
	featuresIndex := -1
	for index, line := range lines {
		trimmed := strings.TrimSpace(strings.SplitN(line, "#", 2)[0])
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inFeatures {
				lines = append(lines[:index], append([]string{"hooks = true"}, lines[index:]...)...)
				return []byte(strings.Join(lines, newline)), nil
			}
			inFeatures = trimmed == "[features]"
			if inFeatures {
				featuresIndex = index
			}
			continue
		}
		if inFeatures {
			compact := strings.ReplaceAll(trimmed, " ", "")
			if strings.HasPrefix(compact, "hooks=") {
				switch compact {
				case "hooks=true":
					return current, nil
				case "hooks=false":
					lines[index] = "hooks = true"
					return []byte(strings.Join(lines, newline)), nil
				default:
					return nil, uxv1.NewError(uxv1.CodeValidation, "Codex features.hooks must be a boolean")
				}
			}
		}
	}
	if featuresIndex >= 0 {
		lines = append(lines, "hooks = true")
	} else {
		if len(lines) > 0 && lines[len(lines)-1] != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "[features]", "hooks = true")
	}
	return []byte(strings.Join(lines, newline)), nil
}

func readSafeTarget(home, path string) ([]byte, bool, error) {
	if err := ensureContained(home, path); err != nil {
		return nil, false, err
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, uxv1.Wrap(uxv1.CodeUpstream, "cannot inspect agent setup target", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > maxConfigBytes {
		return nil, false, uxv1.NewError(uxv1.CodeSafety, "agent setup target must be a bounded regular file, not a symlink")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false, uxv1.Wrap(uxv1.CodeUpstream, "cannot read agent setup target", err)
	}
	return data, true, nil
}

func writeSafeTarget(home, path string, content []byte) error {
	if err := ensureContained(home, path); err != nil {
		return err
	}
	dir := filepath.Dir(path)
	if err := mkdirNoSymlink(home, dir); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return uxv1.NewError(uxv1.CodeSafety, "refusing to replace unsafe agent setup target")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot inspect agent setup target", err)
	}
	temp, err := os.CreateTemp(dir, ".gl-axi-setup-*")
	if err != nil {
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot create agent setup file", err)
	}
	name := temp.Name()
	defer os.Remove(name)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(content); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return uxv1.Wrap(uxv1.CodeUpstream, "cannot install agent setup file", err)
	}
	return nil
}

func rollback(home string, written []update) error {
	var failures []error
	for index := len(written) - 1; index >= 0; index-- {
		item := written[index]
		if item.existed {
			if err := writeSafeTarget(home, item.path, item.before); err != nil {
				failures = append(failures, err)
			}
		} else if err := os.Remove(item.path); err != nil && !errors.Is(err, os.ErrNotExist) {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

func mkdirNoSymlink(home, dir string) error {
	relative, err := filepath.Rel(home, dir)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return uxv1.NewError(uxv1.CodeSafety, "agent setup path escapes the selected home")
	}
	current := home
	for _, part := range strings.Split(relative, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if errors.Is(err, os.ErrNotExist) {
			if err := os.Mkdir(current, 0o700); err != nil {
				return uxv1.Wrap(uxv1.CodeUpstream, "cannot create agent setup directory", err)
			}
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return uxv1.NewError(uxv1.CodeSafety, "agent setup directory must not be a symlink")
		}
	}
	return nil
}

func ensureContained(home, path string) error {
	home = filepath.Clean(home)
	path = filepath.Clean(path)
	relative, err := filepath.Rel(home, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return uxv1.NewError(uxv1.CodeSafety, fmt.Sprintf("agent setup target %q escapes the selected home", filepath.Base(path)))
	}
	return nil
}
