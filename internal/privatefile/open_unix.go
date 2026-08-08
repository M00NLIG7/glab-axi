//go:build darwin || linux || freebsd || openbsd || netbsd

package privatefile

import (
	"os"
	"syscall"
)

func openNoFollow(path string) (*os.File, os.FileInfo, bool, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, true, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = syscall.Close(fd)
		return nil, nil, true, syscall.EBADF
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, true, err
	}
	return file, info, true, nil
}
