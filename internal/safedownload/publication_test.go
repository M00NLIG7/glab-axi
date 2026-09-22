package safedownload

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func prepareNestedArchive(t *testing.T) (*Transaction, string, string) {
	t.Helper()
	parent := parentDir(t)
	destination := filepath.Join(parent, "result")
	tx, err := Prepare(destination)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Close() })
	receipt, err := tx.Extract(context.Background(), archiveBytes(t,
		zipEntry{"bin/app.txt", "payload", 0o644},
		zipEntry{"bin/lib/data.txt", "data", 0o644},
		zipEntry{"owned.txt", "owned", 0o644},
		zipEntry{"empty/", "", os.ModeDir | 0o755},
	))
	if err != nil || receipt.Files != 3 || receipt.Bytes != 16 {
		t.Fatalf("receipt=%+v err=%v", receipt, err)
	}
	return tx, parent, destination
}

func TestNestedArchivePublication(t *testing.T) {
	tx, parent, destination := prepareNestedArchive(t)
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatal("destination visible before publication")
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"bin/app.txt": "payload", "bin/lib/data.txt": "data", "owned.txt": "owned"} {
		body, err := os.ReadFile(filepath.Join(destination, filepath.FromSlash(name)))
		if err != nil || string(body) != want {
			t.Fatalf("%s: body=%q err=%v", name, body, err)
		}
	}
	entries, err := os.ReadDir(filepath.Join(destination, "empty"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty directory: entries=%v err=%v", entries, err)
	}
	noStaging(t, parent)
}

func TestSingleCharacterDestinationPublication(t *testing.T) {
	parent := parentDir(t)
	destination := filepath.Join(parent, "x")
	tx, err := Prepare(destination)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { tx.Close() })
	if err := tx.Write(context.Background(), "asset", strings.NewReader("payload"), 7); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(destination, "asset"))
	if err != nil || string(body) != "payload" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	entries, err := os.ReadDir(parent)
	if err != nil || len(entries) != 1 || entries[0].Name() != "x" {
		t.Fatalf("destination name changed: entries=%v err=%v", entries, err)
	}
	noStaging(t, parent)
}

func TestNestedPublicationFailureCleansOwnedFiles(t *testing.T) {
	for _, failure := range []string{"file-collision", "directory-collision", "cancellation-after-refusal"} {
		t.Run(failure, func(t *testing.T) {
			tx, parent, destination := prepareNestedArchive(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			caller := destination
			if failure != "file-collision" {
				if err := os.Mkdir(destination, 0o700); err != nil {
					t.Fatal(err)
				}
				caller = filepath.Join(destination, "caller")
			}
			if failure == "cancellation-after-refusal" {
				if err := tx.Commit(ctx); err == nil {
					t.Fatal("raced destination replaced")
				}
				if err := os.Remove(destination); err != nil {
					t.Fatal(err)
				}
				cancel()
			} else if err := os.WriteFile(caller, []byte("caller"), 0o600); err != nil {
				t.Fatal(err)
			}
			err := tx.Commit(ctx)
			if err == nil {
				t.Fatal("publication succeeded despite collision or cancellation")
			}
			if failure == "cancellation-after-refusal" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancellation cause lost: %v", err)
			}
			if err := tx.Close(); err != nil {
				t.Fatal(err)
			}
			noStaging(t, parent)
			if failure == "cancellation-after-refusal" {
				if _, err := os.Stat(destination); !os.IsNotExist(err) {
					t.Fatal("cancellation published destination")
				}
			} else {
				body, err := os.ReadFile(caller)
				if err != nil || string(body) != "caller" {
					t.Fatalf("caller file changed: body=%q err=%v", body, err)
				}
			}
		})
	}
}

func TestNestedPublicationRetry(t *testing.T) {
	tx, parent, destination := prepareNestedArchive(t)
	if err := os.Mkdir(destination, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err == nil {
		t.Fatal("raced destination replaced")
	}
	if err := os.Remove(destination); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tx.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(destination, "bin", "lib", "data.txt"))
	if err != nil || string(body) != "data" {
		t.Fatalf("body=%q err=%v", body, err)
	}
	noStaging(t, parent)
}

func TestNestedRollbackPreservesForeignEntries(t *testing.T) {
	for _, mutation := range []string{"added-file", "replaced-file", "replaced-directory", "symlink-directory"} {
		t.Run(mutation, func(t *testing.T) {
			tx, parent, destination := prepareNestedArchive(t)
			if err := os.Mkdir(destination, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := tx.Commit(context.Background()); err == nil {
				t.Fatal("raced destination replaced")
			}
			stage := filepath.Join(parent, tx.stageName)
			bin := filepath.Join(stage, "bin")
			app := filepath.Join(bin, "app.txt")
			preserved := map[string]string{}
			removed := []string{filepath.Join(stage, "owned.txt"), filepath.Join(stage, "empty")}
			switch mutation {
			case "added-file":
				caller := filepath.Join(bin, "caller")
				if err := os.WriteFile(caller, []byte("caller"), 0o600); err != nil {
					t.Fatal(err)
				}
				preserved[caller] = "caller"
				removed = append(removed, app, filepath.Join(bin, "lib"))
			case "replaced-file":
				saved := filepath.Join(parent, "saved-app")
				if err := os.Rename(app, saved); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(app, []byte("caller"), 0o600); err != nil {
					t.Fatal(err)
				}
				preserved[app] = "caller"
				preserved[saved] = "payload"
				removed = append(removed, filepath.Join(bin, "lib"))
			case "replaced-directory", "symlink-directory":
				moved := filepath.Join(parent, "moved-bin")
				if err := os.Rename(bin, moved); err != nil {
					t.Fatal(err)
				}
				preserved[filepath.Join(moved, "app.txt")] = "payload"
				preserved[filepath.Join(moved, "lib", "data.txt")] = "data"
				if mutation == "symlink-directory" {
					if err := os.Symlink(moved, bin); err != nil {
						t.Skipf("symlink creation unavailable: %v", err)
					}
				} else {
					if err := os.Mkdir(bin, 0o700); err != nil {
						t.Fatal(err)
					}
					if err := os.Link(filepath.Join(moved, "app.txt"), app); err != nil {
						t.Fatal(err)
					}
				}
				preserved[app] = "payload"
			}
			if err := tx.Close(); err == nil {
				t.Fatal("cleanup accepted foreign staged entries")
			}
			for name, want := range preserved {
				body, err := os.ReadFile(name)
				if err != nil || string(body) != want {
					t.Fatalf("caller content changed at %s: body=%q err=%v", name, body, err)
				}
			}
			for _, name := range removed {
				if _, err := os.Stat(name); !os.IsNotExist(err) {
					t.Fatalf("owned entry not cleaned: %s: %v", name, err)
				}
			}
			entries, err := os.ReadDir(destination)
			if err != nil || len(entries) != 0 {
				t.Fatalf("raced destination changed: entries=%v err=%v", entries, err)
			}
		})
	}
}
