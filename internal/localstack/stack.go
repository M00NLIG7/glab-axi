// Package localstack owns the repository-local linear stack contract. It never
// contacts remotes, changes branch tips, creates commits, or publishes refs.
package localstack

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"time"

	"gl-axi/internal/contract/uxv1"
	"gl-axi/internal/gitremote"
	"gl-axi/internal/safeurl"
)

const MaxBranches = 32
const MaxBytes = 64 << 10
const Version = "glab-axi/stack/v1"

var namePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,63}$`)

type Branch struct {
	Name string `json:"name"`
	MR   int64  `json:"mr"`
}
type Record struct {
	Version  string   `json:"version"`
	Host     string   `json:"host"`
	Project  string   `json:"project"`
	Base     string   `json:"base"`
	Branches []Branch `json:"branches"`
}
type Node struct {
	Name    string `json:"name"`
	Parent  string `json:"parent"`
	Head    string `json:"head"`
	Status  string `json:"status"`
	MR      int64  `json:"mr"`
	Current bool   `json:"current"`
}
type Snapshot struct {
	Current string
	Heads   map[string]string
	Nodes   []Node
	Valid   bool
}
type Repo struct {
	ctx                                                 context.Context
	dir, common, gitdir, host, project, name, ref, lock string
	calls                                               int
}

func failure(message string) error { return uxv1.NewError(uxv1.CodeSafety, message) }
func ValidName(name string) bool   { return namePattern.MatchString(name) }
func ValidBranch(name string) bool {
	return safeurl.ValidateBranch(name) == nil && name != "HEAD" && !strings.HasPrefix(name, "refs/") && !OID(name)
}
func OID(s string) bool {
	if (len(s) != 40 && len(s) != 64) || strings.ToLower(s) != s || strings.Trim(s, "0") == "" {
		return false
	}
	_, err := hex.DecodeString(s)
	return err == nil
}
func Validate(r Record) error {
	if r.Version != Version || safeurl.ValidateHost(r.Host) != nil || safeurl.ValidateProject(r.Project) != nil || !ValidBranch(r.Base) || len(r.Branches) == 0 || len(r.Branches) > MaxBranches {
		return failure("invalid or oversized stack record")
	}
	names := map[string]bool{r.Base: true}
	mrs := map[int64]bool{}
	for _, b := range r.Branches {
		if !ValidBranch(b.Name) || names[b.Name] || b.MR < 0 || b.MR > 0 && mrs[b.MR] {
			return failure("duplicate, cyclic or invalid stack branch/MR")
		}
		names[b.Name] = true
		mrs[b.MR] = true
	}
	return nil
}

// Open resolves Git paths in the selected working directory with all ambient
// GIT_* repository overrides removed. The exact origin is a local identity
// assertion, not an authentication or remote-existence assertion.
func Open(ctx context.Context, dir, host, project, name string, mutate bool) (*Repo, error) {
	if !ValidName(name) || safeurl.ValidateHost(host) != nil || safeurl.ValidateProject(project) != nil {
		return nil, failure("invalid stack identity")
	}
	r := &Repo{ctx: ctx, dir: dir, host: host, project: project, name: name, ref: "refs/glab-axi/stacks/" + name}
	root, err := r.git(nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, failure("stack requires a non-bare local Git repository")
	}
	r.dir = strings.TrimSuffix(root, "\n")
	if r.dir == "" || strings.ContainsAny(r.dir, "\r\n\x00") || !filepath.IsAbs(r.dir) {
		return nil, failure("missing local repository root")
	}
	for _, p := range []struct {
		flag string
		into *string
	}{{"--absolute-git-dir", &r.gitdir}, {"--git-common-dir", &r.common}} {
		v, e := r.git(nil, "rev-parse", p.flag)
		if e != nil {
			return nil, e
		}
		v = strings.TrimSuffix(v, "\n")
		if v == "" || strings.ContainsAny(v, "\r\n\x00") {
			return nil, failure("ambiguous Git metadata path")
		}
		if !filepath.IsAbs(v) {
			v = filepath.Join(r.dir, v)
		}
		v, e = filepath.EvalSymlinks(v)
		if e != nil {
			return nil, failure("cannot resolve Git metadata directory")
		}
		*p.into = v
	}
	if err = r.identity(); err != nil {
		return nil, err
	}
	shallow, e := r.git(nil, "rev-parse", "--is-shallow-repository")
	if e != nil || strings.TrimSpace(shallow) != "false" {
		return nil, failure("stack requires complete local history, not a shallow repository")
	}
	if _, e = os.Lstat(filepath.Join(r.common, "info", "grafts")); !os.IsNotExist(e) {
		return nil, failure("legacy grafted history is not supported")
	}
	if mutate {
		r.lock = filepath.Join(r.common, "glab-axi-stack.lock")
		if err = os.Mkdir(r.lock, 0700); err != nil {
			return nil, failure("another stack operation or retained stack lock exists; inspect before retrying")
		}
	}
	return r, nil
}
func (r *Repo) Close() {
	if r.lock != "" {
		_ = os.Remove(r.lock)
		r.lock = ""
	}
}
func (r *Repo) identity() error {
	raw, err := r.git(nil, "config", "--get-all", "remote.origin.url")
	if err != nil {
		return failure("one origin URL is required")
	}
	origin, err := gitremote.Parse(strings.TrimSuffix(raw, "\n"))
	if err != nil || origin.Host != r.host || origin.Project != r.project {
		return failure("local origin does not exactly match explicit stack host/project")
	}
	return nil
}
func (r *Repo) Load() (*Record, string, error) {
	oid, err := r.readRef(r.ref, false)
	if err != nil {
		return nil, "", err
	}
	if oid == "" {
		return nil, "", nil
	}
	body, err := r.git(nil, "cat-file", "blob", oid)
	if err != nil {
		return nil, "", failure("stack metadata must be a bounded JSON blob")
	}
	var rec Record
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if dec.Decode(&rec) != nil || dec.Decode(new(any)) != io.EOF {
		return nil, "", failure("invalid stack metadata JSON")
	}
	if err = Validate(rec); err != nil {
		return nil, "", err
	}
	canonical, _ := json.Marshal(rec)
	if !bytes.Equal([]byte(body), canonical) {
		return nil, "", failure("stack metadata must use the canonical v1 encoding; duplicate or edited JSON is refused")
	}
	if rec.Host != r.host || rec.Project != r.project {
		return nil, "", failure("stack metadata belongs to a different project")
	}
	return &rec, oid, nil
}
func (r *Repo) readRef(ref string, commit bool) (string, error) {
	// Never dereference an unexpected symbolic branch or metadata ref.
	_, e := r.git(nil, "symbolic-ref", "--quiet", ref)
	if e == nil {
		return "", failure("symbolic stack refs are not supported")
	}
	if !exit(e, 1) {
		return "", e
	}
	value, e := r.git(nil, "rev-parse", "--verify", "--quiet", ref)
	if exit(e, 1) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	value = strings.TrimSpace(value)
	if !OID(value) {
		return "", failure("invalid local ref object")
	}
	if commit {
		kind, e := r.git(nil, "cat-file", "-t", value)
		if e != nil || strings.TrimSpace(kind) != "commit" {
			return "", failure("stack branches must point directly to commits")
		}
	}
	return value, nil
}
func (r *Repo) Current() (string, error) {
	value, err := r.git(nil, "symbolic-ref", "--quiet", "HEAD")
	if exit(err, 1) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, "refs/heads/") || !ValidBranch(strings.TrimPrefix(value, "refs/heads/")) {
		return "", failure("invalid current local branch")
	}
	return strings.TrimPrefix(value, "refs/heads/"), nil
}
func (r *Repo) Inspect(rec Record) (Snapshot, error) {
	if err := Validate(rec); err != nil {
		return Snapshot{}, err
	}
	current, err := r.Current()
	if err != nil {
		return Snapshot{}, err
	}
	snap := Snapshot{Current: current, Heads: map[string]string{}, Nodes: []Node{}, Valid: true}
	names := []string{rec.Base}
	for _, b := range rec.Branches {
		names = append(names, b.Name)
	}
	for _, name := range names {
		oid, e := r.readRef("refs/heads/"+name, true)
		if e != nil {
			return snap, e
		}
		snap.Heads[name] = oid
	}
	parent := rec.Base
	if snap.Heads[parent] == "" {
		snap.Valid = false
	}
	for _, b := range rec.Branches {
		node := Node{Name: b.Name, Parent: parent, Head: snap.Heads[b.Name], MR: b.MR, Current: current == b.Name, Status: "ready"}
		if node.Head == "" {
			node.Status = "missing"
		} else if snap.Heads[parent] == "" {
			node.Status = "missing_parent"
		} else {
			bases, e := r.git(nil, "merge-base", "--all", snap.Heads[parent], node.Head)
			if e != nil && !exit(e, 1) {
				return snap, e
			}
			if strings.TrimSpace(bases) != snap.Heads[parent] {
				node.Status = "diverged"
			}
		}
		if node.Status != "ready" {
			snap.Valid = false
		}
		snap.Nodes = append(snap.Nodes, node)
		parent = b.Name
	}
	return snap, nil
}

// Verify checks for changes since a snapshot. It is an observation, not a lock
// over arbitrary external Git operations or a claim of a global atomic read.
func (r *Repo) Verify(rec Record, oid string, snap Snapshot) error {
	if err := r.identity(); err != nil {
		return err
	}
	_, currentOID, err := r.Load()
	if err != nil {
		return err
	}
	current, err := r.Inspect(rec)
	if err != nil {
		return err
	}
	if currentOID != oid || !reflect.DeepEqual(current, snap) {
		return uxv1.NewError(uxv1.CodeConflict, "stack metadata or local branches changed during observation; read again")
	}
	return nil
}

func Compatible(old *Record, next Record) bool {
	if old == nil {
		return true
	}
	if old.Version != next.Version || old.Host != next.Host || old.Project != next.Project || old.Base != next.Base || len(old.Branches) != len(next.Branches) {
		return false
	}
	for i, b := range old.Branches {
		n := next.Branches[i]
		if b.Name != n.Name || b.MR != 0 && b.MR != n.MR {
			return false
		}
	}
	return true
}

// Save publishes one atomic metadata CAS plus exact branch-OID verifies. It
// cannot lose another writer's registration or publish a graph from changed
// branch tips. An interrupted hash-object may leave a harmless unreachable blob.
func (r *Repo) Save(old *Record, oldOID string, next Record, snap Snapshot) (string, error) {
	if r.lock == "" {
		return "", failure("metadata write requires a stack operation lock")
	}
	if err := Validate(next); err != nil {
		return "", err
	}
	if !Compatible(old, next) || !snap.Valid {
		return "", failure("stack conflicts with existing registration or local ancestry")
	}
	if err := r.identity(); err != nil {
		return "", err
	}
	body, _ := json.Marshal(next)
	if len(body) > MaxBytes {
		return "", failure("stack metadata exceeds size bound")
	}
	oid, err := r.git(body, "hash-object", "-w", "--stdin")
	if err != nil {
		return "", err
	}
	oid = strings.TrimSpace(oid)
	if !OID(oid) {
		return "", failure("invalid metadata object identity")
	}
	var tx strings.Builder
	tx.WriteString("start\n")
	for _, name := range append([]string{next.Base}, branchNames(next)...) {
		head := snap.Heads[name]
		if !OID(head) {
			return "", failure("missing verified branch head")
		}
		// update-ref's option applies to the next command only.
		tx.WriteString("option no-deref\nverify refs/heads/" + name + " " + head + "\n")
	}
	if oldOID == "" {
		tx.WriteString("option no-deref\ncreate " + r.ref + " " + oid + "\n")
	} else {
		if !OID(oldOID) {
			return "", failure("invalid previous metadata identity")
		}
		tx.WriteString("option no-deref\nupdate " + r.ref + " " + oid + " " + oldOID + "\n")
	}
	tx.WriteString("prepare\n")
	guards := map[string]string{r.ref: oldOID}
	for _, name := range append([]string{next.Base}, branchNames(next)...) {
		guards["refs/heads/"+name] = snap.Heads[name]
	}
	if err = r.commitMetadata(tx.String(), guards); err != nil {
		return "", err
	}
	if reflect.DeepEqual(old, &next) {
		return "unchanged", nil
	}
	return "registered", nil
}
func branchNames(rec Record) []string {
	names := make([]string, 0, len(rec.Branches))
	for _, b := range rec.Branches {
		names = append(names, b.Name)
	}
	return names
}

func (r *Repo) SafeTree() error {
	for _, name := range []string{"index.lock", "HEAD.lock", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "REBASE_HEAD", "rebase-merge", "rebase-apply", "sequencer", "BISECT_START"} {
		for _, dir := range []string{r.gitdir, r.common} {
			if _, e := os.Lstat(filepath.Join(dir, name)); !os.IsNotExist(e) {
				return failure("Git operation/lock is present; checkout refused")
			}
		}
	}
	// Custom filters, sparse and hidden index entries prevent a truthful clean
	// tree check. Submodules also have independent local work and hook surfaces.
	if v, e := r.git(nil, "config", "--get-regexp", `^(filter\.|core\.sparsecheckout|extensions\.partialclone)`); e == nil && v != "" {
		return failure("custom filters, sparse or partial checkout configuration is not supported")
	} else if e != nil && !exit(e, 1) {
		return e
	}
	files, e := r.git(nil, "ls-files", "-v", "-z")
	if e != nil {
		return e
	}
	for _, entry := range strings.Split(files, "\x00") {
		if entry != "" && !strings.HasPrefix(entry, "H ") {
			return failure("hidden or unmerged index entries prevent safe checkout")
		}
	}
	stages, e := r.git(nil, "ls-files", "--stage", "-z")
	if e != nil {
		return e
	}
	for _, entry := range strings.Split(stages, "\x00") {
		if strings.HasPrefix(entry, "160000 ") {
			return failure("stack checkout does not recurse into submodules")
		}
	}
	status, e := r.git(nil, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--ignored=matching")
	if e != nil {
		return e
	}
	if status != "" {
		return failure("dirty, untracked or ignored local files exist; checkout refused without changing them")
	}
	return nil
}

// Checkout never stashes, cleans, resets, creates a branch or forces a switch.
// Git does not offer a worktree-wide CAS with arbitrary external processes. We
// recheck inputs immediately before switching and verify afterward; a race or
// cancellation after the attempt returns uncertainty, never destructive rollback.
func (r *Repo) Checkout(rec Record, metadataOID string, snap Snapshot, destination, expectedCurrent, expectedHead, expectedTarget string) (string, error) {
	if r.lock == "" || !snap.Valid || snap.Current == "" || snap.Current != expectedCurrent || snap.Heads[snap.Current] != expectedHead || snap.Heads[destination] != expectedTarget || !OID(expectedHead) || !OID(expectedTarget) {
		return "", failure("checkout expectations do not match the selected stack/current branch/heads")
	}
	if err := r.SafeTree(); err != nil {
		return "", err
	}
	if err := r.identity(); err != nil {
		return "", err
	}
	fresh, err := r.Inspect(rec)
	if err != nil {
		return "", err
	}
	_, freshOID, err := r.Load()
	if err != nil {
		return "", err
	}
	if metadataOID != freshOID || !reflect.DeepEqual(snap, fresh) {
		return "", uxv1.NewError(uxv1.CodeConflict, "stack or branch changed before checkout")
	}
	if destination == snap.Current {
		return "unchanged", nil
	}
	_, err = r.git(nil, "switch", "--no-guess", "--no-overwrite-ignore", "--", destination)
	if err != nil {
		return "", uxv1.NewError(uxv1.CodeConflict, "checkout was attempted but not confirmed; inspect current branch and local files before retrying; no rollback attempted")
	}
	after, e := r.Inspect(rec)
	_, finalOID, metadataErr := r.Load()
	if e != nil || metadataErr != nil || finalOID != metadataOID || after.Current != destination || !reflect.DeepEqual(after.Heads, snap.Heads) || r.SafeTree() != nil {
		return "", uxv1.NewError(uxv1.CodeConflict, "checkout postcondition is uncertain; inspect current branch and local files; no rollback attempted")
	}
	return "checked_out", nil
}

// Each invocation is shell-free, cancellation-bound and byte-bounded. Git
// diagnostics are deliberately not exposed: they can contain local paths or
// credential-bearing configuration. Repository routing variables cannot escape
// the explicitly selected cwd, and lazy object fetching is disabled.
func (r *Repo) git(input []byte, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(r.ctx, 3*time.Second)
	defer cancel()
	cmd, err := r.gitCommand(ctx, args...)
	if err != nil {
		return "", err
	}
	cmd.Stdin = bytes.NewReader(input)
	stdout := &bounded{}
	cmd.Stdout = stdout
	err = cmd.Run()
	if ctx.Err() != nil {
		return "", uxv1.NewError(uxv1.CodeConflict, "stack Git operation cancelled or timed out; inspect local state before retrying")
	}
	if stdout.overflow {
		return "", failure("stack Git output exceeds bound")
	}
	if err != nil {
		return "", err
	}
	return stdout.String(), nil
}

func (r *Repo) gitCommand(ctx context.Context, args ...string) (*exec.Cmd, error) {
	r.calls++
	if r.calls > 1000 {
		return nil, failure("stack Git command budget exceeded")
	}
	prefix := []string{"-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-c", "submodule.recurse=false", "-c", "protocol.allow=never", "--no-optional-locks"}
	cmd := exec.CommandContext(ctx, "git", append(prefix, args...)...)
	cmd.Dir = r.dir
	for _, v := range os.Environ() {
		key, _, _ := strings.Cut(v, "=")
		if !strings.HasPrefix(strings.ToUpper(key), "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	cmd.Env = append(cmd.Env, "GIT_TERMINAL_PROMPT=0", "GIT_NO_REPLACE_OBJECTS=1", "GIT_NO_LAZY_FETCH=1")
	cmd.Stderr = io.Discard
	cmd.WaitDelay = time.Second
	return cmd, nil
}
func exit(err error, code int) bool {
	var e *exec.ExitError
	return errors.As(err, &e) && e.ExitCode() == code
}

type bounded struct {
	bytes.Buffer
	overflow bool
}

func (b *bounded) Write(p []byte) (int, error) {
	n := len(p)
	if n > MaxBytes-b.Len() {
		b.overflow = true
		p = p[:MaxBytes-b.Len()]
	}
	_, _ = b.Buffer.Write(p)
	return n, nil
}
