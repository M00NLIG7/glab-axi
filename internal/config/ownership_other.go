//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd && !dragonfly

package config

import "os"

func verifyOwner(os.FileInfo) error { return nil }
