//go:build darwin || linux

package safedownload

import (
	"errors"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

func openAbsoluteDirectory(path string) (*os.File, error) {
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path, "/"), "/") {
		if part == "" {
			continue
		}
		next, openErr := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, openErr
		}
		fd = next
	}
	return os.NewFile(uintptr(fd), path), nil
}
func verifyStage(parent, stage *os.File, name string) error {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	current := os.NewFile(uintptr(fd), name)
	defer current.Close()
	a, err := current.Stat()
	if err != nil {
		return err
	}
	b, err := stage.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(a, b) {
		return errors.New("staging directory changed")
	}
	return nil
}

func ensureAbsent(parent *os.File, name string) error {
	var stat unix.Stat_t
	err := unix.Fstatat(int(parent.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	return os.ErrExist
}
func makeDirectory(parent *os.File, name string) (*os.File, error) {
	if err := unix.Mkdirat(int(parent.Fd()), name, 0o700); err != nil {
		return nil, err
	}
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
func createFile(parent *os.File, name string) (*os.File, error) {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}
func removeOwned(parent *os.File, name string, expected os.FileInfo, directory bool) error {
	fd, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	file.Close()
	if err != nil {
		return err
	}
	if !os.SameFile(info, expected) {
		return errors.New("staged entry was replaced")
	}
	flags := 0
	if directory {
		flags = unix.AT_REMOVEDIR
	}
	return unix.Unlinkat(int(parent.Fd()), name, flags)
}
