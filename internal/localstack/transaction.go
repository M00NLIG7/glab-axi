package localstack

import (
	"bufio"
	"context"
	"io"
	"sort"
	"strings"
	"time"

	"gl-axi/internal/contract/uxv1"
)

// commitMetadata uses update-ref's supported prepare/commit protocol. OID
// verification alone accepts a symbolic ref resolving to the same object, even
// with no-deref. Prepare holds Git's own locks on every no-deref guarded name;
// we inspect ref kind and OID while those locks are held, then commit. A
// cooperating Git writer cannot change kind between that inspection and commit.
// No branch is updated, no external lock file is managed, and no post-write
// repair is used as a substitute for the pre-publication check.
func (r *Repo) commitMetadata(prepare string, guards map[string]string) error {
	uncertain := func() error {
		return uxv1.NewError(uxv1.CodeConflict, "stack publication did not confirm; re-read stack view before retrying (no branch tips were written)")
	}
	ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
	defer cancel()
	cmd, err := r.gitCommand(ctx, "update-ref", "--stdin")
	if err != nil {
		return err
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return uncertain()
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return uncertain()
	}
	// EOF aborts an uncommitted explicit transaction. Prefer this graceful
	// cancellation to SIGKILL so Git releases its prepared locks. WaitDelay still
	// bounds a misbehaving process, without attempting to remove its lock files.
	cmd.Cancel = func() error { return stdin.Close() }
	if err = cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return uncertain()
	}
	waited := false
	defer func() {
		_ = stdin.Close()
		if !waited {
			_ = cmd.Wait()
		}
	}()
	reader := bufio.NewReader(io.LimitReader(stdout, MaxBytes+1))
	acknowledge := func(want string) bool { line, e := reader.ReadString('\n'); return e == nil && line == want+": ok\n" }
	if _, err = io.Copy(stdin, strings.NewReader(prepare)); err != nil || !acknowledge("start") || !acknowledge("prepare") {
		return uncertain()
	}
	// The values in guards are exactly those already locked by prepare; the
	// metadata ref is included to catch an ordinary-to-symbolic replacement too.
	names := make([]string, 0, len(guards))
	for name := range guards {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		oid, e := r.readRef(name, false)
		if e != nil {
			return e
		}
		if oid != guards[name] {
			return uxv1.NewError(uxv1.CodeConflict, "locked stack ref identity differs from the inspected ordinary ref; no metadata published")
		}
	}
	if ctx.Err() != nil {
		return uncertain()
	}
	if _, err = io.WriteString(stdin, "commit\n"); err != nil {
		return uncertain()
	}
	_ = stdin.Close()
	confirmed := acknowledge("commit")
	rest, readErr := io.ReadAll(reader)
	err = cmd.Wait()
	waited = true
	if !confirmed || readErr != nil || len(rest) != 0 || err != nil {
		return uncertain()
	}
	return nil
}
