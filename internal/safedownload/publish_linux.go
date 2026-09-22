package safedownload

import (
	"golang.org/x/sys/unix"
	"os"
)

func publishDirectory(parent, stage *os.File, from, to string) error {
	if err := verifyStage(parent, stage, from); err != nil {
		return err
	}
	return unix.Renameat2(int(parent.Fd()), from, int(parent.Fd()), to, unix.RENAME_NOREPLACE)
}
