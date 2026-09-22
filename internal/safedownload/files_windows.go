package safedownload

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

func ntOpen(parent windows.Handle, name string, create, directory bool) (*os.File, error) {
	object, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return nil, err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: parent, ObjectName: object, Attributes: windows.OBJ_CASE_INSENSITIVE}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	var allocation int64
	access := uint32(windows.FILE_GENERIC_READ | windows.SYNCHRONIZE)
	disposition := uint32(windows.FILE_OPEN)
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	if create {
		access |= windows.FILE_GENERIC_WRITE | windows.DELETE
		disposition = windows.FILE_CREATE
		user, err := windows.GetCurrentProcessToken().GetTokenUser()
		if err != nil {
			return nil, err
		}
		security, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")")
		if err != nil {
			return nil, err
		}
		oa.SecurityDescriptor = security
	}
	err = windows.NtCreateFile(&handle, access, &oa, &status, &allocation, windows.FILE_ATTRIBUTE_NORMAL, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, disposition, options, 0, 0)
	if err != nil {
		return nil, err
	}
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
		windows.CloseHandle(handle)
		return nil, errors.New("reparse point or invalid directory handle")
	}
	return os.NewFile(uintptr(handle), name), nil
}
func openAbsoluteDirectory(path string) (*os.File, error) {
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return nil, errors.New("only local drive destinations are supported")
	}
	root, err := ntOpen(0, `\??\`+volume+`\`, false, true)
	if err != nil {
		return nil, err
	}
	for _, part := range strings.Split(strings.TrimPrefix(path[len(volume):], `\`), `\`) {
		if part == "" {
			continue
		}
		next, err := ntOpen(windows.Handle(root.Fd()), part, false, true)
		root.Close()
		if err != nil {
			return nil, err
		}
		root = next
	}
	return root, nil
}
func ensureAbsent(parent *os.File, name string) error {
	for _, directory := range []bool{false, true} {
		file, err := ntOpen(windows.Handle(parent.Fd()), name, false, directory)
		if err == nil {
			file.Close()
			return os.ErrExist
		}
		if errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) {
			return nil
		}
	}
	return errors.New("destination exists or cannot be inspected")
}
func makeDirectory(parent *os.File, name string) (*os.File, error) {
	return ntOpen(windows.Handle(parent.Fd()), name, true, true)
}
func createFile(parent *os.File, name string) (*os.File, error) {
	return ntOpen(windows.Handle(parent.Fd()), name, true, false)
}
func removeOwned(parent *os.File, name string, expected os.FileInfo, directory bool) error {
	object, err := windows.NewNTUnicodeString(name)
	if err != nil {
		return err
	}
	oa := windows.OBJECT_ATTRIBUTES{RootDirectory: windows.Handle(parent.Fd()), ObjectName: object, Attributes: windows.OBJ_CASE_INSENSITIVE}
	oa.Length = uint32(unsafe.Sizeof(oa))
	var handle windows.Handle
	var status windows.IO_STATUS_BLOCK
	options := uint32(windows.FILE_OPEN_REPARSE_POINT | windows.FILE_SYNCHRONOUS_IO_NONALERT)
	if directory {
		options |= windows.FILE_DIRECTORY_FILE
	} else {
		options |= windows.FILE_NON_DIRECTORY_FILE
	}
	err = windows.NtCreateFile(&handle, windows.FILE_GENERIC_READ|windows.DELETE|windows.SYNCHRONIZE, &oa, &status, nil, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, windows.FILE_OPEN, options, 0, 0)
	if errors.Is(err, windows.STATUS_OBJECT_NAME_NOT_FOUND) {
		return nil
	}
	if err != nil {
		return err
	}
	file := os.NewFile(uintptr(handle), name)
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(info, expected) {
		return errors.New("staged entry was replaced")
	}
	remove := byte(1)
	return windows.NtSetInformationFile(handle, &status, &remove, 1, windows.FileDispositionInformation)
}

type renameInformation struct {
	Flags          uint32
	RootDirectory  windows.Handle
	FileNameLength uint32
	FileName       [1]uint16
}

func publishDirectory(parent, stage *os.File, from, to string) error {
	name, err := windows.UTF16FromString(to)
	if err != nil {
		return err
	}
	var shape renameInformation
	size := int(unsafe.Offsetof(shape.FileName)) + (len(name)-1)*2
	buffer := make([]byte, size)
	info := (*renameInformation)(unsafe.Pointer(&buffer[0]))
	info.RootDirectory = windows.Handle(parent.Fd())
	info.FileNameLength = uint32((len(name) - 1) * 2)
	copy(unsafe.Slice(&info.FileName[0], len(name)-1), name[:len(name)-1])
	var status windows.IO_STATUS_BLOCK
	// Flags=0 refuses any existing destination. Rename the opened stage, not
	// a re-resolved path, so a replaced stage name cannot substitute content.
	return windows.NtSetInformationFile(windows.Handle(stage.Fd()), &status, &buffer[0], uint32(size), windows.FileRenameInformation)
}
