//go:build windows

package privatefile

import (
	"os"
	"syscall"
)

func openNoFollow(path string) (*os.File, os.FileInfo, bool, error) {
	name, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, nil, false, err
	}
	handle, err := syscall.CreateFile(
		name,
		syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ,
		nil,
		syscall.OPEN_EXISTING,
		syscall.FILE_ATTRIBUTE_NORMAL|syscall.FILE_FLAG_OPEN_REPARSE_POINT,
		0,
	)
	if err != nil {
		return nil, nil, false, err
	}
	var byHandle syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(handle, &byHandle); err != nil {
		syscall.CloseHandle(handle)
		return nil, nil, false, err
	}
	if byHandle.FileAttributes&syscall.FILE_ATTRIBUTE_REPARSE_POINT != 0 || byHandle.FileAttributes&syscall.FILE_ATTRIBUTE_DIRECTORY != 0 {
		syscall.CloseHandle(handle)
		return nil, nil, false, syscall.ERROR_ACCESS_DENIED
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		syscall.CloseHandle(handle)
		return nil, nil, false, syscall.Errno(6) // ERROR_INVALID_HANDLE
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, false, err
	}
	return file, info, false, nil
}
