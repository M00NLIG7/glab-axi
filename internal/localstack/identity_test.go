package localstack

import "testing"

func TestExactRefDisappearanceInvalidatesObservations(t *testing.T) {
	for _, ref := range []string{"refs/heads/main", "refs/heads/one", "refs/heads/two", "refs/glab-axi/stacks/demo"} {
		t.Run(ref, func(t *testing.T) {
			dir, rec := fixture(t)
			r := openFixture(t, dir, true)
			snap, metadata := register(t, r, rec)
			oid := testGit(t, dir, "show-ref", "--verify", "--hash", ref)
			testGit(t, dir, "update-ref", "refs/tags/"+ref, oid)
			testGit(t, dir, "update-ref", "-d", ref)
			if err := r.Verify(rec, metadata, snap); err == nil {
				t.Fatal("tag hid disappearance during observation")
			}
			if _, err := r.Checkout(rec, metadata, snap, "one", "two", snap.Heads["two"], snap.Heads["one"]); err == nil {
				t.Fatal("tag hid disappearance before checkout")
			}
			if _, err := r.Save(&rec, metadata, rec, snap); err == nil {
				t.Fatal("tag hid disappearance before publication")
			}
			if got := testGit(t, dir, "symbolic-ref", "HEAD"); got != "refs/heads/two" {
				t.Fatalf("checkout changed HEAD: %q", got)
			}
			if got := testGit(t, dir, "show-ref", "--verify", "--hash", "refs/tags/"+ref); got != oid {
				t.Fatal("tag was changed")
			}
			_, got, err := r.Load()
			want := metadata
			if ref == r.ref {
				want = ""
			}
			if err != nil || got != want {
				t.Fatalf("metadata changed after refusal: %q %v", got, err)
			}
		})
	}
}
