//go:build darwin || linux || freebsd || netbsd || openbsd || dragonfly

package config

import (
	"os"
	"syscall"

	"glab-axi/internal/contract/v1"
)

func verifyOwner(info os.FileInfo) error {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Getuid()) {
		return v1.NewError(v1.CodeSafety, "glab-axi config must be owned by the current user")
	}
	return nil
}
