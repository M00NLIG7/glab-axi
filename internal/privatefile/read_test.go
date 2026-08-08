package privatefile

import (
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
)

func TestReadUsesOpenedPrivateRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "input")
	if err := os.WriteFile(path, []byte("title\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Read(path, 64, true)
	if err != nil || got != "title" {
		t.Fatalf("got=%q err=%v", got, err)
	}

	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(path, 64, true); err == nil {
			t.Fatal("group-readable input was accepted")
		}
	}
}

func TestReadNeverFollowsFinalSymlinkDuringSwap(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires optional Windows privileges")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "input")
	secret := filepath.Join(dir, "must-not-read")
	if err := os.WriteFile(secret, []byte("forbidden-target"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("allowed"), 0o600); err != nil {
		t.Fatal(err)
	}

	stop := make(chan struct{})
	var swaps sync.WaitGroup
	swaps.Add(1)
	go func() {
		defer swaps.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = os.Remove(path)
			_ = os.Symlink(secret, path)
			next := path + ".next"
			_ = os.WriteFile(next, []byte("allowed"), 0o600)
			_ = os.Rename(next, path)
		}
	}()

	for i := 0; i < 2000; i++ {
		value, err := Read(path, 64, false)
		if err == nil && value != "allowed" {
			close(stop)
			swaps.Wait()
			t.Fatalf("path swap exposed %q", value)
		}
	}
	close(stop)
	swaps.Wait()
}
